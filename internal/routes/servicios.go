package routes

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type serviciosHandler struct{ db *pgxpool.Pool }

func RegisterServicios(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &serviciosHandler{db: db}
	r.Route("/api/servicios", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Put("/{id}", h.update)
		r.Delete("/{id}", h.delete)
	})
}

type servicio struct {
	ID              int       `json:"id"`
	NegocioID       int       `json:"negocio_id"`
	Nombre          string    `json:"nombre"`
	Descripcion     *string   `json:"descripcion,omitempty"`
	DuracionMinutos int       `json:"duracion_minutos"`
	Precio          float64   `json:"precio"`
	Color           string    `json:"color"`
	Activo          bool      `json:"activo"`
	ImagenURL       *string   `json:"imagen_url,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

const servicioColumns = `id, negocio_id, nombre, descripcion, duracion_minutos, precio, color, activo, imagen_url, created_at`

func scanServicio(row interface{ Scan(...any) error }) (servicio, error) {
	var s servicio
	return s, row.Scan(&s.ID, &s.NegocioID, &s.Nombre, &s.Descripcion,
		&s.DuracionMinutos, &s.Precio, &s.Color, &s.Activo, &s.ImagenURL, &s.CreatedAt)
}

func (h *serviciosHandler) list(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT `+servicioColumns+` FROM servicios WHERE negocio_id = $1 AND activo = true ORDER BY nombre`, nid)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := []servicio{}
	for rows.Next() {
		s, err := scanServicio(rows)
		if err != nil {
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		list = append(list, s)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

type servicioRequest struct {
	Nombre          string  `json:"nombre"`
	Descripcion     *string `json:"descripcion"`
	DuracionMinutos *int    `json:"duracion_minutos"`
	Precio          *float64 `json:"precio"`
	Color           *string `json:"color"`
}

func (h *serviciosHandler) create(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req servicioRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Nombre == "" {
		http.Error(w, `{"error":"nombre is required"}`, http.StatusBadRequest)
		return
	}

	dur := 60
	if req.DuracionMinutos != nil {
		dur = *req.DuracionMinutos
	}
	precio := 0.0
	if req.Precio != nil {
		precio = *req.Precio
	}
	color := "#000080"
	if req.Color != nil {
		color = *req.Color
	}

	s, err := scanServicio(h.db.QueryRow(r.Context(),
		`INSERT INTO servicios (negocio_id, nombre, descripcion, duracion_minutos, precio, color)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+servicioColumns,
		nid, req.Nombre, req.Descripcion, dur, precio, color))
	if err != nil {
		http.Error(w, `{"error":"failed to create servicio"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(s)
}

func (h *serviciosHandler) update(w http.ResponseWriter, r *http.Request) {
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

	var req servicioRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Nombre == "" {
		http.Error(w, `{"error":"nombre is required"}`, http.StatusBadRequest)
		return
	}

	s, err := scanServicio(h.db.QueryRow(r.Context(),
		`UPDATE servicios SET nombre=$3, descripcion=$4, duracion_minutos=COALESCE($5,duracion_minutos),
		 precio=COALESCE($6,precio), color=COALESCE($7,color)
		 WHERE id=$1 AND negocio_id=$2 RETURNING `+servicioColumns,
		id, nid, req.Nombre, req.Descripcion, req.DuracionMinutos, req.Precio, req.Color))
	if err != nil {
		http.Error(w, `{"error":"servicio not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}

func (h *serviciosHandler) delete(w http.ResponseWriter, r *http.Request) {
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

	tag, err := h.db.Exec(r.Context(),
		`UPDATE servicios SET activo=false WHERE id=$1 AND negocio_id=$2`, id, nid)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, `{"error":"servicio not found"}`, http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
