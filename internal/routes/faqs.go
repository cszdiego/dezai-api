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

type faqsHandler struct{ db *pgxpool.Pool }

func RegisterFAQs(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &faqsHandler{db: db}
	r.Route("/api/faqs", func(r chi.Router) {
		r.Use(authMiddleware)
		// Sugerencias — registradas antes de /{id} para evitar conflictos de ruta
		r.Get("/sugerencias", h.listSugerencias)
		r.Post("/sugerencias", h.createSugerencia)
		r.Delete("/sugerencias/{id}", h.deleteSugerencia)
		r.Post("/sugerencias/{id}/aprobar", h.aprobarSugerencia)
		// Rutas principales de FAQs
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Put("/{id}", h.update)
		r.Delete("/{id}", h.delete)
	})
}

type faq struct {
	ID        int       `json:"id"`
	NegocioID int       `json:"negocio_id"`
	Pregunta  string    `json:"pregunta"`
	Respuesta string    `json:"respuesta"`
	Activo    bool      `json:"activo"`
	CreatedAt time.Time `json:"created_at"`
}

const faqColumns = `id, negocio_id, pregunta, respuesta, activo, created_at`

func scanFAQ(row interface{ Scan(...any) error }) (faq, error) {
	var f faq
	return f, row.Scan(&f.ID, &f.NegocioID, &f.Pregunta, &f.Respuesta, &f.Activo, &f.CreatedAt)
}

func (h *faqsHandler) list(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT `+faqColumns+` FROM faqs WHERE negocio_id = $1 ORDER BY created_at ASC`, nid)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := []faq{}
	for rows.Next() {
		f, err := scanFAQ(rows)
		if err != nil {
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		list = append(list, f)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

type faqRequest struct {
	Pregunta  string `json:"pregunta"`
	Respuesta string `json:"respuesta"`
	Activo    *bool  `json:"activo"`
}

func (h *faqsHandler) create(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req faqRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Pregunta == "" || req.Respuesta == "" {
		http.Error(w, `{"error":"pregunta and respuesta are required"}`, http.StatusBadRequest)
		return
	}

	activo := true
	if req.Activo != nil {
		activo = *req.Activo
	}

	f, err := scanFAQ(h.db.QueryRow(r.Context(),
		`INSERT INTO faqs (negocio_id, pregunta, respuesta, activo)
		 VALUES ($1,$2,$3,$4) RETURNING `+faqColumns,
		nid, req.Pregunta, req.Respuesta, activo))
	if err != nil {
		http.Error(w, `{"error":"failed to create faq"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(f)
}

func (h *faqsHandler) update(w http.ResponseWriter, r *http.Request) {
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

	var req faqRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Pregunta == "" || req.Respuesta == "" {
		http.Error(w, `{"error":"pregunta and respuesta are required"}`, http.StatusBadRequest)
		return
	}

	f, err := scanFAQ(h.db.QueryRow(r.Context(),
		`UPDATE faqs SET pregunta=$3, respuesta=$4, activo=COALESCE($5, activo)
		 WHERE id=$1 AND negocio_id=$2 RETURNING `+faqColumns,
		id, nid, req.Pregunta, req.Respuesta, req.Activo))
	if err != nil {
		http.Error(w, `{"error":"faq not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(f)
}

func (h *faqsHandler) delete(w http.ResponseWriter, r *http.Request) {
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
		`DELETE FROM faqs WHERE id=$1 AND negocio_id=$2`, id, nid)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, `{"error":"faq not found"}`, http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ── Sugerencias de FAQs ───────────────────────────────────────────────────────

type faqSugerencia struct {
	ID        int       `json:"id"`
	NegocioID int       `json:"negocio_id"`
	Pregunta  string    `json:"pregunta"`
	CreatedAt time.Time `json:"created_at"`
}

func scanSugerencia(row interface{ Scan(...any) error }) (faqSugerencia, error) {
	var s faqSugerencia
	return s, row.Scan(&s.ID, &s.NegocioID, &s.Pregunta, &s.CreatedAt)
}

func (h *faqsHandler) listSugerencias(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, negocio_id, pregunta, created_at FROM faq_sugerencias WHERE negocio_id = $1 ORDER BY created_at DESC`, nid)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := []faqSugerencia{}
	for rows.Next() {
		s, err := scanSugerencia(rows)
		if err != nil {
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		list = append(list, s)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

type sugerenciaRequest struct {
	NegocioID int    `json:"negocio_id"`
	Pregunta  string `json:"pregunta"`
}

func (h *faqsHandler) createSugerencia(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req sugerenciaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Pregunta == "" {
		http.Error(w, `{"error":"pregunta is required"}`, http.StatusBadRequest)
		return
	}

	s, err := scanSugerencia(h.db.QueryRow(r.Context(),
		`INSERT INTO faq_sugerencias (negocio_id, pregunta) VALUES ($1,$2) RETURNING id, negocio_id, pregunta, created_at`,
		nid, req.Pregunta))
	if err != nil {
		http.Error(w, `{"error":"failed to create sugerencia"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(s)
}

func (h *faqsHandler) deleteSugerencia(w http.ResponseWriter, r *http.Request) {
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
		`DELETE FROM faq_sugerencias WHERE id=$1 AND negocio_id=$2`, id, nid)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, `{"error":"sugerencia not found"}`, http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type aprobarRequest struct {
	Respuesta string `json:"respuesta"`
}

func (h *faqsHandler) aprobarSugerencia(w http.ResponseWriter, r *http.Request) {
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

	var req aprobarRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Respuesta == "" {
		http.Error(w, `{"error":"respuesta is required"}`, http.StatusBadRequest)
		return
	}

	var pregunta string
	if err := h.db.QueryRow(r.Context(),
		`SELECT pregunta FROM faq_sugerencias WHERE id=$1 AND negocio_id=$2`, id, nid).Scan(&pregunta); err != nil {
		http.Error(w, `{"error":"sugerencia not found"}`, http.StatusNotFound)
		return
	}

	f, err := scanFAQ(h.db.QueryRow(r.Context(),
		`INSERT INTO faqs (negocio_id, pregunta, respuesta, activo) VALUES ($1,$2,$3,true) RETURNING `+faqColumns,
		nid, pregunta, req.Respuesta))
	if err != nil {
		http.Error(w, `{"error":"failed to create faq"}`, http.StatusInternalServerError)
		return
	}

	h.db.Exec(r.Context(), `DELETE FROM faq_sugerencias WHERE id=$1`, id)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(f)
}
