package routes

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type configAgendaHandler struct{ db *pgxpool.Pool }

func RegisterConfiguracionAgenda(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &configAgendaHandler{db: db}
	r.Route("/api/configuracion-agenda", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.get)
		r.Put("/", h.put)
	})
}

type configAgendaRow struct {
	NegocioID              int  `json:"negocio_id"`
	IntervaloMinutos       int  `json:"intervalo_minutos"`
	DiaInicioSemana        int  `json:"dia_inicio_semana"`
	ConfirmacionAutomatica bool `json:"autocompletar_citas"`
	MaxCitasPorHora        int  `json:"max_citas_por_hora"`
}

const configAgendaCols = `negocio_id, intervalo_minutos, dia_inicio_semana, confirmacion_automatica, max_citas_por_hora`

func scanConfigAgenda(row interface{ Scan(...any) error }) (configAgendaRow, error) {
	var c configAgendaRow
	return c, row.Scan(&c.NegocioID, &c.IntervaloMinutos, &c.DiaInicioSemana,
		&c.ConfirmacionAutomatica, &c.MaxCitasPorHora)
}

func (h *configAgendaHandler) get(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	cfg, err := scanConfigAgenda(h.db.QueryRow(r.Context(),
		`SELECT `+configAgendaCols+` FROM configuracion_agenda WHERE negocio_id=$1`, nid))
	if err != nil {
		cfg = configAgendaRow{
			NegocioID:              nid,
			IntervaloMinutos:       30,
			DiaInicioSemana:        1,
			ConfirmacionAutomatica: false,
			MaxCitasPorHora:        1,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

type putConfigAgendaRequest struct {
	IntervaloMinutos       int  `json:"intervalo_minutos"`
	DiaInicioSemana        int  `json:"dia_inicio_semana"`
	ConfirmacionAutomatica bool `json:"autocompletar_citas"`
	MaxCitasPorHora        int  `json:"max_citas_por_hora"`
}

func (h *configAgendaHandler) put(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req putConfigAgendaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}
	if req.IntervaloMinutos == 0 {
		req.IntervaloMinutos = 30
	}
	if req.MaxCitasPorHora == 0 {
		req.MaxCitasPorHora = 1
	}

	cfg, err := scanConfigAgenda(h.db.QueryRow(r.Context(),
		`INSERT INTO configuracion_agenda (`+configAgendaCols+`)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (negocio_id) DO UPDATE SET
		   intervalo_minutos       = EXCLUDED.intervalo_minutos,
		   dia_inicio_semana       = EXCLUDED.dia_inicio_semana,
		   confirmacion_automatica = EXCLUDED.confirmacion_automatica,
		   max_citas_por_hora      = EXCLUDED.max_citas_por_hora
		 RETURNING `+configAgendaCols,
		nid, req.IntervaloMinutos, req.DiaInicioSemana, req.ConfirmacionAutomatica, req.MaxCitasPorHora))
	if err != nil {
		http.Error(w, `{"error":"failed to save"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}
