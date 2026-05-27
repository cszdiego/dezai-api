package routes

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type apiKeysHandler struct{ db *pgxpool.Pool }

func RegisterAPIKeys(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &apiKeysHandler{db: db}
	r.Route("/api/api-keys", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Delete("/{id}", h.delete)
	})
}

type apiKeyRow struct {
	ID         int        `json:"id"`
	NegocioID  int        `json:"negocio_id"`
	Nombre     string     `json:"nombre"`
	KeyPreview string     `json:"key_preview"`
	Activo     bool       `json:"activo"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// ── GET /api/api-keys ─────────────────────────────────────────────

func (h *apiKeysHandler) list(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, negocio_id, nombre, key_preview, activo, created_at, last_used_at
		 FROM api_keys WHERE negocio_id = $1 AND activo = true ORDER BY created_at DESC`, nid)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := []apiKeyRow{}
	for rows.Next() {
		var k apiKeyRow
		if err := rows.Scan(&k.ID, &k.NegocioID, &k.Nombre, &k.KeyPreview,
			&k.Activo, &k.CreatedAt, &k.LastUsedAt); err != nil {
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		list = append(list, k)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

// ── POST /api/api-keys ────────────────────────────────────────────

type createAPIKeyRequest struct {
	Nombre string `json:"nombre"`
}

type createAPIKeyResponse struct {
	apiKeyRow
	Key string `json:"key"`
}

func (h *apiKeysHandler) create(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req createAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Nombre) == "" {
		http.Error(w, `{"error":"nombre is required"}`, http.StatusBadRequest)
		return
	}

	// "dzai_" + 32 random bytes as hex = 69 chars total
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		http.Error(w, `{"error":"failed to generate key"}`, http.StatusInternalServerError)
		return
	}
	key := "dzai_" + hex.EncodeToString(rawBytes)
	keyPreview := key[:12] // "dzai_" + 7 hex chars

	hashBytes := sha256.Sum256([]byte(key))
	keyHash := fmt.Sprintf("%x", hashBytes)

	var row apiKeyRow
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO api_keys (negocio_id, nombre, key_hash, key_preview, key_plain)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, negocio_id, nombre, key_preview, activo, created_at, last_used_at`,
		nid, strings.TrimSpace(req.Nombre), keyHash, keyPreview, key,
	).Scan(&row.ID, &row.NegocioID, &row.Nombre, &row.KeyPreview,
		&row.Activo, &row.CreatedAt, &row.LastUsedAt)
	if err != nil {
		http.Error(w, `{"error":"failed to create key"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(createAPIKeyResponse{apiKeyRow: row, Key: key})
}

// ── DELETE /api/api-keys/:id ──────────────────────────────────────

func (h *apiKeysHandler) delete(w http.ResponseWriter, r *http.Request) {
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
		`UPDATE api_keys SET activo = false WHERE id = $1 AND negocio_id = $2`, id, nid)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, `{"error":"key not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true})
}
