package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"

	cldapi "github.com/cloudinary/cloudinary-go/v2/api"
	"github.com/cloudinary/cloudinary-go/v2/api/uploader"
	"github.com/cloudinary/cloudinary-go/v2/config"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type imagenesHandler struct{ db *pgxpool.Pool }

func RegisterImagenes(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &imagenesHandler{db: db}
	r.Route("/api/imagenes", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Post("/servicio/{id}", h.uploadServicio)
		r.Delete("/servicio/{id}", h.deleteServicio)
	})
}

func newUploaderAPI() (*uploader.API, error) {
	cfg, err := config.NewFromParams(
		os.Getenv("CLOUDINARY_CLOUD_NAME"),
		os.Getenv("CLOUDINARY_API_KEY"),
		os.Getenv("CLOUDINARY_API_SECRET"),
	)
	if err != nil {
		return nil, err
	}
	return uploader.NewWithConfiguration(cfg)
}

// ── POST /api/imagenes/servicio/:id ──────────────────────────────

func (h *imagenesHandler) uploadServicio(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	sid, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}

	// Verify service belongs to this negocio
	var exists bool
	h.db.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM servicios WHERE id=$1 AND negocio_id=$2 AND activo=true)`,
		sid, nid).Scan(&exists)
	if !exists {
		http.Error(w, `{"error":"servicio not found"}`, http.StatusNotFound)
		return
	}

	// Parse multipart (max 10 MB)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, `{"error":"invalid multipart form"}`, http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("imagen")
	if err != nil {
		http.Error(w, `{"error":"campo 'imagen' requerido"}`, http.StatusBadRequest)
		return
	}
	defer file.Close()

	cld, err := newUploaderAPI()
	if err != nil {
		http.Error(w, `{"error":"cloudinary init failed"}`, http.StatusInternalServerError)
		return
	}

	publicID := fmt.Sprintf("dezai/negocio_%d/servicio_%d", nid, sid)
	folder := fmt.Sprintf("dezai/negocio_%d", nid)

	resp, err := cld.Upload(context.Background(), file, uploader.UploadParams{
		PublicID:       publicID,
		Folder:         folder,
		Overwrite:      cldapi.Bool(true),
		UniqueFilename: cldapi.Bool(false),
		Transformation: "w_800,c_limit,q_auto",
	})
	if err != nil {
		http.Error(w, `{"error":"upload failed"}`, http.StatusInternalServerError)
		return
	}

	if _, err := h.db.Exec(r.Context(),
		`UPDATE servicios SET imagen_url=$1 WHERE id=$2 AND negocio_id=$3`,
		resp.SecureURL, sid, nid); err != nil {
		http.Error(w, `{"error":"failed to save image url"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"imagen_url": resp.SecureURL})
}

// ── DELETE /api/imagenes/servicio/:id ────────────────────────────

func (h *imagenesHandler) deleteServicio(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	sid, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}

	var imagenURL *string
	err = h.db.QueryRow(r.Context(),
		`SELECT imagen_url FROM servicios WHERE id=$1 AND negocio_id=$2 AND activo=true`,
		sid, nid).Scan(&imagenURL)
	if err != nil {
		http.Error(w, `{"error":"servicio not found"}`, http.StatusNotFound)
		return
	}

	if imagenURL != nil {
		cld, err := newUploaderAPI()
		if err != nil {
			http.Error(w, `{"error":"cloudinary init failed"}`, http.StatusInternalServerError)
			return
		}
		publicID := fmt.Sprintf("dezai/negocio_%d/servicio_%d", nid, sid)
		cld.Destroy(context.Background(), uploader.DestroyParams{PublicID: publicID})
	}

	if _, err := h.db.Exec(r.Context(),
		`UPDATE servicios SET imagen_url=NULL WHERE id=$1 AND negocio_id=$2`,
		sid, nid); err != nil {
		http.Error(w, `{"error":"failed to clear image url"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true})
}
