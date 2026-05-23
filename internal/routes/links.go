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

type linksHandler struct{ db *pgxpool.Pool }

func RegisterLinks(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &linksHandler{db: db}
	r.Route("/api/links", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Put("/{id}", h.update)
		r.Delete("/{id}", h.delete)
	})
}

type link struct {
	ID        int       `json:"id"`
	NegocioID int       `json:"negocio_id"`
	Etiqueta  string    `json:"etiqueta"`
	URL       string    `json:"url"`
	Activo    bool      `json:"activo"`
	CreatedAt time.Time `json:"created_at"`
}

const linkColumns = `id, negocio_id, etiqueta, url, activo, created_at`

func scanLink(row interface{ Scan(...any) error }) (link, error) {
	var l link
	return l, row.Scan(&l.ID, &l.NegocioID, &l.Etiqueta, &l.URL, &l.Activo, &l.CreatedAt)
}

func (h *linksHandler) list(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT `+linkColumns+` FROM links WHERE negocio_id = $1 ORDER BY created_at ASC`, nid)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := []link{}
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		list = append(list, l)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

type linkRequest struct {
	Etiqueta string `json:"etiqueta"`
	URL      string `json:"url"`
	Activo   *bool  `json:"activo"`
}

func (h *linksHandler) create(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req linkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Etiqueta == "" || req.URL == "" {
		http.Error(w, `{"error":"etiqueta and url are required"}`, http.StatusBadRequest)
		return
	}

	activo := true
	if req.Activo != nil {
		activo = *req.Activo
	}

	l, err := scanLink(h.db.QueryRow(r.Context(),
		`INSERT INTO links (negocio_id, etiqueta, url, activo)
		 VALUES ($1,$2,$3,$4) RETURNING `+linkColumns,
		nid, req.Etiqueta, req.URL, activo))
	if err != nil {
		http.Error(w, `{"error":"failed to create link"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(l)
}

func (h *linksHandler) update(w http.ResponseWriter, r *http.Request) {
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

	var req linkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Etiqueta == "" || req.URL == "" {
		http.Error(w, `{"error":"etiqueta and url are required"}`, http.StatusBadRequest)
		return
	}

	l, err := scanLink(h.db.QueryRow(r.Context(),
		`UPDATE links SET etiqueta=$3, url=$4, activo=COALESCE($5, activo)
		 WHERE id=$1 AND negocio_id=$2 RETURNING `+linkColumns,
		id, nid, req.Etiqueta, req.URL, req.Activo))
	if err != nil {
		http.Error(w, `{"error":"link not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(l)
}

func (h *linksHandler) delete(w http.ResponseWriter, r *http.Request) {
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
		`DELETE FROM links WHERE id=$1 AND negocio_id=$2`, id, nid)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, `{"error":"link not found"}`, http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
