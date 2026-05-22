package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type notificacionesHandler struct{ db *pgxpool.Pool }

func RegisterNotificaciones(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &notificacionesHandler{db: db}
	r.Route("/api/notificaciones", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Put("/leer-todas", h.leerTodas)
		r.Put("/{id}/leer", h.leer)
	})
}

// ── Preferencias de notificación ──────────────────────────────────

func debeNotificar(ctx context.Context, db *pgxpool.Pool, negocioID int, tipo string) bool {
	var nueva, cancelacion, confirmacion bool
	err := db.QueryRow(ctx,
		`SELECT notif_cita_nueva, notif_cancelacion, notif_confirmacion
		 FROM configuracion_notificaciones WHERE negocio_id=$1`, negocioID).
		Scan(&nueva, &cancelacion, &confirmacion)
	if err != nil {
		return true // sin config → permite todas
	}
	switch tipo {
	case "cita_nueva":
		return nueva
	case "cita_cancelada":
		return cancelacion
	case "cita_confirmada", "cita_reagendada":
		return confirmacion
	default:
		return true
	}
}

// ── Envío Expo Push Notification ──────────────────────────────────

func sendExpoPush(token, title, body string) {
	payload := map[string]string{
		"to":    token,
		"title": title,
		"body":  body,
		"sound": "default",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	resp, err := http.Post(
		"https://exp.host/--/api/v2/push/send",
		"application/json",
		bytes.NewReader(data),
	)
	if err == nil {
		resp.Body.Close()
	}
}

func pushToNegocio(db *pgxpool.Pool, negocioID int, titulo, mensaje string) {
	rows, err := db.Query(context.Background(),
		`SELECT push_token FROM dispositivos WHERE negocio_id=$1`, negocioID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var token string
		if rows.Scan(&token) == nil {
			sendExpoPush(token, titulo, mensaje)
		}
	}
}

// ── Tipos ─────────────────────────────────────────────────────────

type createNotifRequest struct {
	Titulo         string  `json:"titulo"`
	Mensaje        string  `json:"mensaje"`
	Tipo           string  `json:"tipo"`
	ReferenciaID   *int    `json:"referencia_id"`
	ReferenciaTipo *string `json:"referencia_tipo"`
}

type notificacionRow struct {
	ID             int       `json:"id"`
	NegocioID      int       `json:"negocio_id"`
	Titulo         string    `json:"titulo"`
	Mensaje        string    `json:"mensaje"`
	Tipo           string    `json:"tipo"`
	ReferenciaID   *int      `json:"referencia_id,omitempty"`
	ReferenciaTipo *string   `json:"referencia_tipo,omitempty"`
	Leida          bool      `json:"leida"`
	CreatedAt      time.Time `json:"created_at"`
}

const notifColumns = `id, negocio_id, titulo, mensaje, tipo, referencia_id, referencia_tipo, leida, created_at`

func scanNotif(row interface{ Scan(...any) error }) (notificacionRow, error) {
	var n notificacionRow
	return n, row.Scan(&n.ID, &n.NegocioID, &n.Titulo, &n.Mensaje, &n.Tipo,
		&n.ReferenciaID, &n.ReferenciaTipo, &n.Leida, &n.CreatedAt)
}

// ── Recordatorios programados ────────────────────────────────────

func recordatorioVentana(horasAntes int) (ventana, mensaje string) {
	switch horasAntes {
	case 0:
		return "30 minutes", "en 30 minutos"
	case 1:
		return "1 hour", "en 1 hora"
	case 3:
		return "3 hours", "en 3 horas"
	case 24:
		return "24 hours", "mañana"
	case 48:
		return "48 hours", "en 2 días"
	default:
		return fmt.Sprintf("%d hours", horasAntes), fmt.Sprintf("en %d horas", horasAntes)
	}
}

// EnviarRecordatoriosPendientes se llama cada minuto desde main.go.
// Para cada negocio con recordatorios activos busca citas dentro de
// la ventana configurada que no hayan sido notificadas aún.
func EnviarRecordatoriosPendientes(db *pgxpool.Pool) {
	ctx := context.Background()

	type negocioCfg struct{ negocioID, horasAntes int }
	var configs []negocioCfg

	cfgRows, err := db.Query(ctx,
		`SELECT negocio_id, horas_antes FROM configuracion_notificaciones WHERE recordatorio_cita = true`)
	if err != nil {
		return
	}
	for cfgRows.Next() {
		var c negocioCfg
		cfgRows.Scan(&c.negocioID, &c.horasAntes)
		configs = append(configs, c)
	}
	cfgRows.Close()

	for _, cfg := range configs {
		ventana, msg := recordatorioVentana(cfg.horasAntes)

		citaRows, err := db.Query(ctx,
			`SELECT id FROM citas
			 WHERE negocio_id = $1
			   AND status != 'cancelada'
			   AND recordatorio_enviado = false
			   AND fecha_hora >= NOW()
			   AND fecha_hora < NOW() + $2::interval`,
			cfg.negocioID, ventana)
		if err != nil {
			continue
		}

		var ids []int
		for citaRows.Next() {
			var id int
			citaRows.Scan(&id)
			ids = append(ids, id)
		}
		citaRows.Close()

		for _, citaID := range ids {
			titulo := "Recordatorio de cita"
			cuerpo := "Tienes una cita " + msg

			db.Exec(ctx,
				`INSERT INTO notificaciones (negocio_id, titulo, mensaje, tipo, referencia_id, referencia_tipo)
				 VALUES ($1,$2,$3,'cita_recordatorio',$4,'cita')
				 ON CONFLICT DO NOTHING`,
				cfg.negocioID, titulo, cuerpo, citaID)

			db.Exec(ctx,
				`UPDATE citas SET recordatorio_enviado = true WHERE id = $1`, citaID)

			go pushToNegocio(db, cfg.negocioID, titulo, cuerpo)
		}
	}
}

// ── Handlers ──────────────────────────────────────────────────────

func (h *notificacionesHandler) create(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req createNotifRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Titulo == "" {
		http.Error(w, `{"error":"titulo is required"}`, http.StatusBadRequest)
		return
	}

	// Respetar preferencias del usuario
	if !debeNotificar(r.Context(), h.db, nid, req.Tipo) {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	n, err := scanNotif(h.db.QueryRow(r.Context(),
		`INSERT INTO notificaciones (negocio_id, titulo, mensaje, tipo, referencia_id, referencia_tipo)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+notifColumns,
		nid, req.Titulo, req.Mensaje, req.Tipo, req.ReferenciaID, req.ReferenciaTipo))
	if err != nil {
		http.Error(w, `{"error":"failed to create notificacion"}`, http.StatusInternalServerError)
		return
	}

	// Enviar push en paralelo (fire-and-forget)
	go pushToNegocio(h.db, nid, req.Titulo, req.Mensaje)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(n)
}

func (h *notificacionesHandler) list(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT `+notifColumns+` FROM notificaciones
		 WHERE negocio_id=$1 ORDER BY leida ASC, created_at DESC`, nid)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := []notificacionRow{}
	for rows.Next() {
		n, err := scanNotif(rows)
		if err != nil {
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		list = append(list, n)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func (h *notificacionesHandler) leer(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}

	n, err := scanNotif(h.db.QueryRow(r.Context(),
		`UPDATE notificaciones SET leida=true WHERE id=$1 AND negocio_id=$2 RETURNING `+notifColumns,
		id, nid))
	if err != nil {
		http.Error(w, `{"error":"notificacion not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(n)
}

func (h *notificacionesHandler) leerTodas(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	if _, err := h.db.Exec(r.Context(),
		`UPDATE notificaciones SET leida=true WHERE negocio_id=$1`, nid); err != nil {
		http.Error(w, `{"error":"update failed"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
