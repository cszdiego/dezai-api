package routes

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type negociosHandler struct{ db *pgxpool.Pool }

func RegisterNegocios(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	// Migración de columnas nuevas en tablas existentes (idempotente)
	for _, stmt := range []string{
		`ALTER TABLE negocios ADD COLUMN IF NOT EXISTS apellido  VARCHAR(100)`,
		`ALTER TABLE negocios ADD COLUMN IF NOT EXISTS direccion TEXT`,
		`ALTER TABLE negocios ADD COLUMN IF NOT EXISTS horarios  JSONB`,
		`ALTER TABLE negocios ADD COLUMN IF NOT EXISTS reglas    TEXT`,
		`ALTER TABLE usuarios ADD COLUMN IF NOT EXISTS plan VARCHAR(20) NOT NULL DEFAULT 'basico'`,
	} {
		if _, err := db.Exec(context.Background(), stmt); err != nil {
			log.Printf("warn: migration: %v", err)
		}
	}

	h := &negociosHandler{db: db}
	r.Route("/api/negocios", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/me", h.me)
		r.Post("/", h.create)
		r.Put("/me", h.update)
	})
}

// negocioFull incluye todos los campos del negocio más el plan del usuario.
type negocioFull struct {
	ID              int             `json:"id"`
	UID             string          `json:"uid"`
	Nombre          string          `json:"nombre"`
	Apellido        *string         `json:"apellido,omitempty"`
	NombreNegocio   string          `json:"nombre_negocio"`
	Telefono        string          `json:"telefono"`
	FechaNacimiento *time.Time      `json:"fecha_nacimiento,omitempty"`
	Direccion       *string         `json:"direccion,omitempty"`
	Horarios        json.RawMessage `json:"horarios,omitempty"`
	Reglas          *string         `json:"reglas,omitempty"`
	Plan            string          `json:"plan"`
	CreatedAt       time.Time       `json:"created_at"`
}

func scanNegocio(row interface{ Scan(...any) error }) (negocioFull, error) {
	var n negocioFull
	var horariosBuf []byte
	err := row.Scan(&n.ID, &n.UID, &n.Nombre, &n.Apellido, &n.NombreNegocio,
		&n.Telefono, &n.FechaNacimiento, &n.Direccion, &horariosBuf, &n.Reglas,
		&n.Plan, &n.CreatedAt)
	if len(horariosBuf) > 0 {
		n.Horarios = json.RawMessage(horariosBuf)
	}
	return n, err
}

const negocioSelectJoin = `
	SELECT n.id, n.uid, n.nombre, n.apellido, n.nombre_negocio, n.telefono,
	       n.fecha_nacimiento, n.direccion, n.horarios, n.reglas,
	       u.plan, n.created_at
	FROM negocios n JOIN usuarios u ON n.uid = u.uid`

// ── GET /api/negocios/me ─────────────────────────────────────────

func (h *negociosHandler) me(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())

	n, err := scanNegocio(h.db.QueryRow(r.Context(),
		negocioSelectJoin+` WHERE n.uid = $1`, uid))
	if err != nil {
		http.Error(w, `{"error":"negocio not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(n)
}

// ── POST /api/negocios ───────────────────────────────────────────

type createNegocioRequest struct {
	Nombre          string  `json:"nombre"`
	Apellido        *string `json:"apellido"`
	NombreNegocio   string  `json:"nombre_negocio"`
	Telefono        string  `json:"telefono"`
	FechaNacimiento *string `json:"fecha_nacimiento"`
}

func (h *negociosHandler) create(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	email := mw.EmailFromContext(r.Context())

	var req createNegocioRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Nombre == "" || req.NombreNegocio == "" || req.Telefono == "" {
		http.Error(w, `{"error":"nombre, nombre_negocio and telefono are required"}`, http.StatusBadRequest)
		return
	}

	if _, err := h.db.Exec(r.Context(),
		`INSERT INTO usuarios (uid, email, role) VALUES ($1, $2, 'client') ON CONFLICT (uid) DO NOTHING`,
		uid, email); err != nil {
		http.Error(w, `{"error":"failed to register user"}`, http.StatusInternalServerError)
		return
	}

	if _, err := h.db.Exec(r.Context(),
		`INSERT INTO negocios (uid, nombre, apellido, nombre_negocio, telefono, fecha_nacimiento)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		uid, req.Nombre, req.Apellido, req.NombreNegocio, req.Telefono, req.FechaNacimiento); err != nil {
		http.Error(w, `{"error":"failed to create negocio"}`, http.StatusInternalServerError)
		return
	}

	n, err := scanNegocio(h.db.QueryRow(r.Context(), negocioSelectJoin+` WHERE n.uid = $1`, uid))
	if err != nil {
		http.Error(w, `{"error":"negocio not found after insert"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(n)
}

// ── PUT /api/negocios/me ─────────────────────────────────────────

type updateNegocioRequest struct {
	Nombre          string          `json:"nombre"`
	Apellido        *string         `json:"apellido"`
	NombreNegocio   string          `json:"nombre_negocio"`
	Telefono        string          `json:"telefono"`
	FechaNacimiento *string         `json:"fecha_nacimiento"`
	Direccion       *string         `json:"direccion"`
	Horarios        json.RawMessage `json:"horarios"`
	Reglas          *string         `json:"reglas"`
}

func (h *negociosHandler) update(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())

	var req updateNegocioRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Nombre == "" || req.Telefono == "" {
		http.Error(w, `{"error":"nombre and telefono are required"}`, http.StatusBadRequest)
		return
	}

	var horariosArg interface{}
	if len(req.Horarios) > 0 {
		horariosArg = []byte(req.Horarios)
	}

	if _, err := h.db.Exec(r.Context(),
		`UPDATE negocios
		 SET nombre=$2, apellido=$3, nombre_negocio=$4, telefono=$5,
		     fecha_nacimiento=$6, direccion=$7, horarios=$8, reglas=$9
		 WHERE uid=$1`,
		uid, req.Nombre, req.Apellido, req.NombreNegocio, req.Telefono,
		req.FechaNacimiento, req.Direccion, horariosArg, req.Reglas); err != nil {
		http.Error(w, `{"error":"failed to update negocio"}`, http.StatusInternalServerError)
		return
	}

	n, err := scanNegocio(h.db.QueryRow(r.Context(), negocioSelectJoin+` WHERE n.uid = $1`, uid))
	if err != nil {
		http.Error(w, `{"error":"negocio not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(n)
}
