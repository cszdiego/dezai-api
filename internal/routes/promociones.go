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

type promocionesCRUDHandler struct{ db *pgxpool.Pool }

func RegisterPromociones(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &promocionesCRUDHandler{db: db}
	r.Route("/api/promociones", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Put("/{id}", h.update)
		r.Delete("/{id}", h.delete)
	})
}

type promocion struct {
	ID          int       `json:"id"`
	NegocioID   int       `json:"negocio_id"`
	Titulo      string    `json:"titulo"`
	Descripcion *string   `json:"descripcion,omitempty"`
	ImagenURL   *string   `json:"imagen_url,omitempty"`
	Activo      bool      `json:"activo"`
	CreatedAt   time.Time `json:"created_at"`
}

const promocionColumns = `id, negocio_id, titulo, descripcion, imagen_url, activo, created_at`

func scanPromocion(row interface{ Scan(...any) error }) (promocion, error) {
	var p promocion
	return p, row.Scan(&p.ID, &p.NegocioID, &p.Titulo, &p.Descripcion, &p.ImagenURL, &p.Activo, &p.CreatedAt)
}

func (h *promocionesCRUDHandler) list(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT `+promocionColumns+` FROM promociones WHERE negocio_id = $1 ORDER BY created_at DESC`, nid)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := []promocion{}
	for rows.Next() {
		p, err := scanPromocion(rows)
		if err != nil {
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		list = append(list, p)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

type promocionRequest struct {
	Titulo      string  `json:"titulo"`
	Descripcion *string `json:"descripcion"`
	ImagenURL   *string `json:"imagen_url"`
	Activo      *bool   `json:"activo"`
}

func (h *promocionesCRUDHandler) create(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req promocionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Titulo == "" {
		http.Error(w, `{"error":"titulo is required"}`, http.StatusBadRequest)
		return
	}

	activo := true
	if req.Activo != nil {
		activo = *req.Activo
	}

	p, err := scanPromocion(h.db.QueryRow(r.Context(),
		`INSERT INTO promociones (negocio_id, titulo, descripcion, imagen_url, activo)
		 VALUES ($1,$2,$3,$4,$5) RETURNING `+promocionColumns,
		nid, req.Titulo, req.Descripcion, req.ImagenURL, activo))
	if err != nil {
		http.Error(w, `{"error":"failed to create promocion"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(p)
}

func (h *promocionesCRUDHandler) update(w http.ResponseWriter, r *http.Request) {
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

	var req promocionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Titulo == "" {
		http.Error(w, `{"error":"titulo is required"}`, http.StatusBadRequest)
		return
	}

	p, err := scanPromocion(h.db.QueryRow(r.Context(),
		`UPDATE promociones SET titulo=$3, descripcion=$4, imagen_url=$5, activo=COALESCE($6, activo)
		 WHERE id=$1 AND negocio_id=$2 RETURNING `+promocionColumns,
		id, nid, req.Titulo, req.Descripcion, req.ImagenURL, req.Activo))
	if err != nil {
		http.Error(w, `{"error":"promocion not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

func (h *promocionesCRUDHandler) delete(w http.ResponseWriter, r *http.Request) {
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
		`DELETE FROM promociones WHERE id=$1 AND negocio_id=$2`, id, nid)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, `{"error":"promocion not found"}`, http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
