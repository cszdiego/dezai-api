package routes

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type pushHandler struct{ db *pgxpool.Pool }

func RegisterPush(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &pushHandler{db: db}

	// Público — sin autenticación
	r.Get("/api/notificaciones/vapid-public-key", h.vapidPublicKey)

	// Con autenticación (Firebase o API key)
	r.Route("/api/push", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Post("/subscribe", h.subscribe)
		r.Post("/send", h.send)
	})
}

// GET /api/notificaciones/vapid-public-key
func (h *pushHandler) vapidPublicKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"public_key": os.Getenv("VAPID_PUBLIC_KEY"),
	})
}

// POST /api/push/subscribe
func (h *pushHandler) subscribe(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Endpoint == "" {
		http.Error(w, `{"error":"invalid subscription"}`, http.StatusBadRequest)
		return
	}

	_, err := h.db.Exec(r.Context(),
		`INSERT INTO push_subscriptions (negocio_id, endpoint, p256dh, auth)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (endpoint) DO UPDATE SET p256dh = $3, auth = $4`,
		nid, req.Endpoint, req.Keys.P256dh, req.Keys.Auth)
	if err != nil {
		http.Error(w, `{"error":"failed to save subscription"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// POST /api/push/send
func (h *pushHandler) send(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NegocioID int    `json:"negocio_id"`
		Titulo    string `json:"titulo"`
		Mensaje   string `json:"mensaje"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	// negocio_id en body es opcional: si no viene, se deriva del auth
	nid := req.NegocioID
	if nid == 0 {
		uid := mw.UIDFromContext(r.Context())
		var ok bool
		nid, ok = negocioIDForUID(r.Context(), h.db, uid, w)
		if !ok {
			return
		}
	}

	go webPushToNegocio(h.db, nid, req.Titulo, req.Mensaje)

	h.db.Exec(r.Context(),
		`INSERT INTO notificaciones (negocio_id, titulo, mensaje, tipo) VALUES ($1, $2, $3, 'sistema')`,
		nid, req.Titulo, req.Mensaje)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// webPushToNegocio envía Web Push a todas las subscriptions del negocio.
// Se llama en goroutine (fire-and-forget). Las subscriptions expiradas (410)
// se eliminan automáticamente.
func webPushToNegocio(db *pgxpool.Pool, negocioID int, titulo, mensaje string) {
	ctx := context.Background()

	rows, err := db.Query(ctx,
		`SELECT endpoint, p256dh, auth FROM push_subscriptions WHERE negocio_id = $1`,
		negocioID)
	if err != nil {
		return
	}
	defer rows.Close()

	type sub struct{ endpoint, p256dh, auth string }
	var subs []sub
	for rows.Next() {
		var s sub
		if rows.Scan(&s.endpoint, &s.p256dh, &s.auth) == nil {
			subs = append(subs, s)
		}
	}
	rows.Close()

	if len(subs) == 0 {
		return
	}

	payload, _ := json.Marshal(map[string]string{
		"title": titulo,
		"body":  mensaje,
	})

	vapidPublic  := os.Getenv("VAPID_PUBLIC_KEY")
	vapidPrivate := os.Getenv("VAPID_PRIVATE_KEY")
	vapidEmail   := os.Getenv("VAPID_EMAIL")

	for _, s := range subs {
		log.Printf("Enviando push a endpoint: %s", s.endpoint)
		resp, err := webpush.SendNotification(payload, &webpush.Subscription{
			Endpoint: s.endpoint,
			Keys: webpush.Keys{
				P256dh: s.p256dh,
				Auth:   s.auth,
			},
		}, &webpush.Options{
			VAPIDPublicKey:  vapidPublic,
			VAPIDPrivateKey: vapidPrivate,
			Subscriber:      vapidEmail,
			TTL:             30,
		})
		if err != nil {
			log.Printf("Error enviando push: %v", err)
			continue
		}
		log.Printf("Status respuesta: %d", resp.StatusCode)
		if resp.StatusCode != 201 {
			body, _ := io.ReadAll(resp.Body)
			log.Printf("Error push status %d, body: %s, endpoint: %s",
				resp.StatusCode, string(body), s.endpoint)
		}
		if resp.StatusCode == http.StatusGone {
			// Subscription expirada — limpiar
			db.Exec(ctx, `DELETE FROM push_subscriptions WHERE endpoint = $1`, s.endpoint)
		}
		resp.Body.Close()
	}
}
