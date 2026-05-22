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

type trabajadoresHandler struct{ db *pgxpool.Pool }

func RegisterTrabajadores(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &trabajadoresHandler{db: db}
	r.Route("/api/trabajadores", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Put("/{id}", h.update)
		r.Delete("/{id}", h.delete)
	})
}

type trabajadorRow struct {
	ID        int             `json:"id"`
	NegocioID int             `json:"negocio_id"`
	Nombre    string          `json:"nombre"`
	Apellido  *string         `json:"apellido,omitempty"`
	Telefono  *string         `json:"telefono,omitempty"`
	Activo    bool            `json:"activo"`
	Horario   json.RawMessage `json:"horario"`
	CreatedAt time.Time       `json:"created_at"`
}

const trabajadorColumns = `id, negocio_id, nombre, apellido, telefono, activo, horario, created_at`

func scanTrabajador(row interface{ Scan(...any) error }) (trabajadorRow, error) {
	var t trabajadorRow
	return t, row.Scan(&t.ID, &t.NegocioID, &t.Nombre, &t.Apellido, &t.Telefono,
		&t.Activo, &t.Horario, &t.CreatedAt)
}

func (h *trabajadoresHandler) list(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT `+trabajadorColumns+` FROM trabajadores
		 WHERE negocio_id=$1 AND activo=true ORDER BY nombre ASC`, nid)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := []trabajadorRow{}
	for rows.Next() {
		t, err := scanTrabajador(rows)
		if err != nil {
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		list = append(list, t)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

type createTrabajadorRequest struct {
	Nombre   string          `json:"nombre"`
	Apellido *string         `json:"apellido"`
	Telefono *string         `json:"telefono"`
	Horario  json.RawMessage `json:"horario"`
}

func (h *trabajadoresHandler) create(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req createTrabajadorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Nombre == "" {
		http.Error(w, `{"error":"nombre is required"}`, http.StatusBadRequest)
		return
	}

	var horario interface{}
	if len(req.Horario) > 0 && string(req.Horario) != "null" {
		horario = []byte(req.Horario)
	}

	t, err := scanTrabajador(h.db.QueryRow(r.Context(),
		`INSERT INTO trabajadores (negocio_id, nombre, apellido, telefono, horario)
		 VALUES ($1,$2,$3,$4,$5) RETURNING `+trabajadorColumns,
		nid, req.Nombre, req.Apellido, req.Telefono, horario))
	if err != nil {
		http.Error(w, `{"error":"failed to create trabajador"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(t)
}

type updateTrabajadorRequest struct {
	Nombre   *string         `json:"nombre"`
	Apellido *string         `json:"apellido"`
	Telefono *string         `json:"telefono"`
	Activo   *bool           `json:"activo"`
	Horario  json.RawMessage `json:"horario"`
}

func (h *trabajadoresHandler) update(w http.ResponseWriter, r *http.Request) {
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

	var req updateTrabajadorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	var horario interface{}
	if len(req.Horario) > 0 && string(req.Horario) != "null" {
		horario = []byte(req.Horario)
	}

	t, err := scanTrabajador(h.db.QueryRow(r.Context(),
		`UPDATE trabajadores SET
		   nombre    = COALESCE($3, nombre),
		   apellido  = COALESCE($4, apellido),
		   telefono  = COALESCE($5, telefono),
		   activo    = COALESCE($6, activo),
		   horario   = COALESCE($7, horario)
		 WHERE id=$1 AND negocio_id=$2 RETURNING `+trabajadorColumns,
		id, nid, req.Nombre, req.Apellido, req.Telefono, req.Activo, horario))
	if err != nil {
		http.Error(w, `{"error":"trabajador not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(t)
}

func (h *trabajadoresHandler) delete(w http.ResponseWriter, r *http.Request) {
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

	t, err := scanTrabajador(h.db.QueryRow(r.Context(),
		`UPDATE trabajadores SET activo=false
		 WHERE id=$1 AND negocio_id=$2 RETURNING `+trabajadorColumns,
		id, nid))
	if err != nil {
		http.Error(w, `{"error":"trabajador not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(t)
}
