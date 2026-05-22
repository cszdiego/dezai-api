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

type bloqueosHandler struct{ db *pgxpool.Pool }

func RegisterBloqueos(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &bloqueosHandler{db: db}
	r.Route("/api/bloqueos", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Delete("/{id}", h.delete)
	})
}

type bloqueoRow struct {
	ID          int       `json:"id"`
	NegocioID   int       `json:"negocio_id"`
	FechaInicio time.Time `json:"fecha_inicio"`
	FechaFin    time.Time `json:"fecha_fin"`
	Motivo      *string   `json:"motivo,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

const bloqueoColumns = `id, negocio_id, fecha_inicio, fecha_fin, motivo, created_at`

func scanBloqueo(row interface{ Scan(...any) error }) (bloqueoRow, error) {
	var b bloqueoRow
	return b, row.Scan(&b.ID, &b.NegocioID, &b.FechaInicio, &b.FechaFin, &b.Motivo, &b.CreatedAt)
}

func (h *bloqueosHandler) list(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT `+bloqueoColumns+` FROM bloqueos WHERE negocio_id=$1 ORDER BY fecha_inicio ASC`, nid)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := []bloqueoRow{}
	for rows.Next() {
		b, err := scanBloqueo(rows)
		if err != nil {
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		list = append(list, b)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

type bloqueoRequest struct {
	FechaInicio string  `json:"fecha_inicio"`
	FechaFin    string  `json:"fecha_fin"`
	Motivo      *string `json:"motivo"`
}

func (h *bloqueosHandler) create(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req bloqueoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.FechaInicio == "" || req.FechaFin == "" {
		http.Error(w, `{"error":"fecha_inicio and fecha_fin are required"}`, http.StatusBadRequest)
		return
	}

	b, err := scanBloqueo(h.db.QueryRow(r.Context(),
		`INSERT INTO bloqueos (negocio_id, fecha_inicio, fecha_fin, motivo)
		 VALUES ($1,$2,$3,$4) RETURNING `+bloqueoColumns,
		nid, req.FechaInicio, req.FechaFin, req.Motivo))
	if err != nil {
		http.Error(w, `{"error":"failed to create bloqueo"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(b)
}

func (h *bloqueosHandler) delete(w http.ResponseWriter, r *http.Request) {
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
		`DELETE FROM bloqueos WHERE id=$1 AND negocio_id=$2`, id, nid)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, `{"error":"bloqueo not found"}`, http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
