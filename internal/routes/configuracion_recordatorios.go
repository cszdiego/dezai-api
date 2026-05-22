package routes

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type configRecordatoriosHandler struct{ db *pgxpool.Pool }

func RegisterConfiguracionRecordatorios(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &configRecordatoriosHandler{db: db}
	r.Route("/api/configuracion-recordatorios", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.get)
		r.Put("/", h.put)
	})
}

const defaultMensajeRecordatorio = "Hola {nombre}, te recordamos tu cita de {servicio} el {fecha} a las {hora}. ¡Te esperamos!"

type configRecordatoriosRow struct {
	NegocioID           int    `json:"negocio_id"`
	Activo              bool   `json:"activo"`
	TiempoAntesMinutos  int    `json:"tiempo_antes_minutos"`
	Mensaje             string `json:"mensaje"`
}

const configRecordatoriosCols = `negocio_id, activo, tiempo_antes_minutos, mensaje`

func scanConfigRecordatorios(row interface{ Scan(...any) error }) (configRecordatoriosRow, error) {
	var c configRecordatoriosRow
	return c, row.Scan(&c.NegocioID, &c.Activo, &c.TiempoAntesMinutos, &c.Mensaje)
}

func (h *configRecordatoriosHandler) get(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	cfg, err := scanConfigRecordatorios(h.db.QueryRow(r.Context(),
		`SELECT `+configRecordatoriosCols+` FROM configuracion_recordatorios WHERE negocio_id=$1`, nid))
	if err != nil {
		cfg = configRecordatoriosRow{
			NegocioID:          nid,
			Activo:             false,
			TiempoAntesMinutos: 120,
			Mensaje:            defaultMensajeRecordatorio,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

type putConfigRecordatoriosRequest struct {
	Activo             bool   `json:"activo"`
	TiempoAntesMinutos int    `json:"tiempo_antes_minutos"`
	Mensaje            string `json:"mensaje"`
}

func (h *configRecordatoriosHandler) put(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req putConfigRecordatoriosRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Mensaje) == "" {
		http.Error(w, `{"error":"mensaje no puede estar vacío"}`, http.StatusBadRequest)
		return
	}
	if req.TiempoAntesMinutos <= 0 {
		req.TiempoAntesMinutos = 120
	}

	cfg, err := scanConfigRecordatorios(h.db.QueryRow(r.Context(),
		`INSERT INTO configuracion_recordatorios (`+configRecordatoriosCols+`)
		 VALUES ($1,$2,$3,$4)
		 ON CONFLICT (negocio_id) DO UPDATE SET
		   activo               = EXCLUDED.activo,
		   tiempo_antes_minutos = EXCLUDED.tiempo_antes_minutos,
		   mensaje              = EXCLUDED.mensaje
		 RETURNING `+configRecordatoriosCols,
		nid, req.Activo, req.TiempoAntesMinutos, req.Mensaje))
	if err != nil {
		http.Error(w, `{"error":"failed to save"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}
