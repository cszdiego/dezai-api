package routes

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type configCalificacionesHandler struct{ db *pgxpool.Pool }

func RegisterConfiguracionCalificaciones(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &configCalificacionesHandler{db: db}
	r.Route("/api/configuracion-calificaciones", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.get)
		r.Put("/", h.put)
	})
}

const defaultMensajeCalificacion = "¿Cómo fue tu experiencia con {servicio} en {negocio}, {nombre}? Tu opinión nos ayuda a mejorar."

type configCalificacionesRow struct {
	NegocioID             int    `json:"negocio_id"`
	Activo                bool   `json:"activo"`
	TiempoDespuesMinutos  int    `json:"tiempo_despues_minutos"`
	Mensaje               string `json:"mensaje"`
}

const configCalificacionesCols = `negocio_id, activo, tiempo_despues_minutos, mensaje`

func scanConfigCalificaciones(row interface{ Scan(...any) error }) (configCalificacionesRow, error) {
	var c configCalificacionesRow
	return c, row.Scan(&c.NegocioID, &c.Activo, &c.TiempoDespuesMinutos, &c.Mensaje)
}

func (h *configCalificacionesHandler) get(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	cfg, err := scanConfigCalificaciones(h.db.QueryRow(r.Context(),
		`SELECT `+configCalificacionesCols+` FROM configuracion_calificaciones WHERE negocio_id=$1`, nid))
	if err != nil {
		cfg = configCalificacionesRow{
			NegocioID:            nid,
			Activo:               false,
			TiempoDespuesMinutos: 1440,
			Mensaje:              defaultMensajeCalificacion,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

type putConfigCalificacionesRequest struct {
	Activo               bool   `json:"activo"`
	TiempoDespuesMinutos int    `json:"tiempo_despues_minutos"`
	Mensaje              string `json:"mensaje"`
}

func (h *configCalificacionesHandler) put(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req putConfigCalificacionesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Mensaje) == "" {
		http.Error(w, `{"error":"mensaje no puede estar vacío"}`, http.StatusBadRequest)
		return
	}
	if req.TiempoDespuesMinutos <= 0 {
		req.TiempoDespuesMinutos = 1440
	}

	cfg, err := scanConfigCalificaciones(h.db.QueryRow(r.Context(),
		`INSERT INTO configuracion_calificaciones (`+configCalificacionesCols+`)
		 VALUES ($1,$2,$3,$4)
		 ON CONFLICT (negocio_id) DO UPDATE SET
		   activo                = EXCLUDED.activo,
		   tiempo_despues_minutos = EXCLUDED.tiempo_despues_minutos,
		   mensaje               = EXCLUDED.mensaje
		 RETURNING `+configCalificacionesCols,
		nid, req.Activo, req.TiempoDespuesMinutos, req.Mensaje))
	if err != nil {
		http.Error(w, `{"error":"failed to save"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}
