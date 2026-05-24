package routes

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type agenteConfiguracionHandler struct{ db *pgxpool.Pool }

func RegisterAgenteConfiguracion(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &agenteConfiguracionHandler{db: db}
	r.Route("/api/agente/configuracion", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.get)
		r.Put("/", h.upsert)
	})
}

type agenteConfiguracionBody struct {
	NombreAgente             string `json:"nombre_agente"`
	Tono                     string `json:"tono"`
	MensajeBienvenida        string `json:"mensaje_bienvenida"`
	MensajeFueraHorario      string `json:"mensaje_fuera_horario"`
	InstruccionesAdicionales string `json:"instrucciones_adicionales"`
}

func (h *agenteConfiguracionHandler) get(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var cfg agenteConfiguracionBody
	err := h.db.QueryRow(r.Context(),
		`SELECT nombre_agente, tono,
		        COALESCE(mensaje_bienvenida,''),
		        COALESCE(mensaje_fuera_horario,''),
		        COALESCE(instrucciones_adicionales,'')
		 FROM agente_configuracion WHERE negocio_id = $1`, nid,
	).Scan(&cfg.NombreAgente, &cfg.Tono, &cfg.MensajeBienvenida, &cfg.MensajeFueraHorario, &cfg.InstruccionesAdicionales)
	if err != nil {
		cfg = agenteConfiguracionBody{
			NombreAgente:             "Asistente virtual",
			Tono:                     "amigable",
			MensajeBienvenida:        "",
			MensajeFueraHorario:      "",
			InstruccionesAdicionales: "",
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

func (h *agenteConfiguracionHandler) upsert(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req agenteConfiguracionBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}
	if req.NombreAgente == "" {
		req.NombreAgente = "Asistente virtual"
	}
	if req.Tono == "" {
		req.Tono = "amigable"
	}

	var cfg agenteConfiguracionBody
	if err := h.db.QueryRow(r.Context(),
		`INSERT INTO agente_configuracion
		   (negocio_id, nombre_agente, tono, mensaje_bienvenida, mensaje_fuera_horario, instrucciones_adicionales, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,NOW())
		 ON CONFLICT (negocio_id) DO UPDATE SET
		   nombre_agente=$2, tono=$3,
		   mensaje_bienvenida=$4, mensaje_fuera_horario=$5,
		   instrucciones_adicionales=$6, updated_at=NOW()
		 RETURNING nombre_agente, tono,
		           COALESCE(mensaje_bienvenida,''),
		           COALESCE(mensaje_fuera_horario,''),
		           COALESCE(instrucciones_adicionales,'')`,
		nid, req.NombreAgente, req.Tono, req.MensajeBienvenida, req.MensajeFueraHorario, req.InstruccionesAdicionales,
	).Scan(&cfg.NombreAgente, &cfg.Tono, &cfg.MensajeBienvenida, &cfg.MensajeFueraHorario, &cfg.InstruccionesAdicionales); err != nil {
		http.Error(w, `{"error":"failed to save configuracion"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}
