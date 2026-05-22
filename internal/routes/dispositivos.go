package routes

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type dispositivosHandler struct{ db *pgxpool.Pool }

func RegisterDispositivos(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &dispositivosHandler{db: db}
	r.Route("/api/dispositivos", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Post("/", h.register)
	})
}

type registerDispositivoRequest struct {
	PushToken string `json:"push_token"`
}

func (h *dispositivosHandler) register(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req registerDispositivoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PushToken == "" {
		http.Error(w, `{"error":"push_token is required"}`, http.StatusBadRequest)
		return
	}

	_, err := h.db.Exec(r.Context(),
		`INSERT INTO dispositivos (negocio_id, push_token, updated_at)
		 VALUES ($1, $2, NOW())
		 ON CONFLICT (negocio_id, push_token) DO UPDATE SET updated_at = NOW()`,
		nid, req.PushToken)
	if err != nil {
		http.Error(w, `{"error":"failed to register device"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
