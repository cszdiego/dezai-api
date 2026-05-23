package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	cldapi "github.com/cloudinary/cloudinary-go/v2/api"
	"github.com/cloudinary/cloudinary-go/v2/api/uploader"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type galeriaHandler struct{ db *pgxpool.Pool }

func RegisterGaleria(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &galeriaHandler{db: db}
	r.Route("/api/galeria", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.list)
		r.Post("/", h.upload)
		r.Delete("/{id}", h.delete)
	})
}

type galeriaItem struct {
	ID          int       `json:"id"`
	ImagenURL   string    `json:"imagen_url"`
	Descripcion *string   `json:"descripcion,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func (h *galeriaHandler) list(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, imagen_url, descripcion, created_at FROM galeria
		 WHERE negocio_id = $1 ORDER BY created_at DESC`, nid)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := []galeriaItem{}
	for rows.Next() {
		var item galeriaItem
		if err := rows.Scan(&item.ID, &item.ImagenURL, &item.Descripcion, &item.CreatedAt); err != nil {
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		list = append(list, item)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func (h *galeriaHandler) upload(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

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

	descripcionStr := r.FormValue("descripcion")
	var descripcion *string
	if descripcionStr != "" {
		descripcion = &descripcionStr
	}

	cld, err := newUploaderAPI()
	if err != nil {
		http.Error(w, `{"error":"cloudinary init failed"}`, http.StatusInternalServerError)
		return
	}

	publicID := fmt.Sprintf("dezai/negocio_%d/galeria/img_%d", nid, time.Now().UnixMilli())

	resp, err := cld.Upload(context.Background(), file, uploader.UploadParams{
		PublicID:       publicID,
		Overwrite:      cldapi.Bool(true),
		UniqueFilename: cldapi.Bool(false),
		Transformation: "w_1200,c_limit,q_auto",
	})
	if err != nil {
		http.Error(w, `{"error":"upload failed"}`, http.StatusInternalServerError)
		return
	}

	var item galeriaItem
	if err := h.db.QueryRow(r.Context(),
		`INSERT INTO galeria (negocio_id, imagen_url, descripcion)
		 VALUES ($1,$2,$3) RETURNING id, imagen_url, descripcion, created_at`,
		nid, resp.SecureURL, descripcion,
	).Scan(&item.ID, &item.ImagenURL, &item.Descripcion, &item.CreatedAt); err != nil {
		http.Error(w, `{"error":"failed to save image"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(item)
}

func (h *galeriaHandler) delete(w http.ResponseWriter, r *http.Request) {
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

	var imagenURL string
	if err := h.db.QueryRow(r.Context(),
		`SELECT imagen_url FROM galeria WHERE id=$1 AND negocio_id=$2`,
		id, nid,
	).Scan(&imagenURL); err != nil {
		http.Error(w, `{"error":"imagen not found"}`, http.StatusNotFound)
		return
	}

	cld, err := newUploaderAPI()
	if err != nil {
		http.Error(w, `{"error":"cloudinary init failed"}`, http.StatusInternalServerError)
		return
	}
	if pid := galeriaPublicIDFromURL(imagenURL); pid != "" {
		cld.Destroy(context.Background(), uploader.DestroyParams{PublicID: pid})
	}

	if _, err := h.db.Exec(r.Context(),
		`DELETE FROM galeria WHERE id=$1 AND negocio_id=$2`, id, nid,
	); err != nil {
		http.Error(w, `{"error":"delete failed"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true})
}

// galeriaPublicIDFromURL extracts the Cloudinary public_id from a secure URL.
// URL format: https://res.cloudinary.com/{cloud}/image/upload/[v{ver}/]{public_id}.{ext}
func galeriaPublicIDFromURL(rawURL string) string {
	const marker = "/upload/"
	idx := strings.Index(rawURL, marker)
	if idx < 0 {
		return ""
	}
	path := rawURL[idx+len(marker):]
	// Skip optional version segment "v{digits}/"
	if len(path) > 1 && path[0] == 'v' {
		if slash := strings.Index(path, "/"); slash > 1 {
			onlyDigits := true
			for _, ch := range path[1:slash] {
				if ch < '0' || ch > '9' {
					onlyDigits = false
					break
				}
			}
			if onlyDigits {
				path = path[slash+1:]
			}
		}
	}
	// Strip file extension
	if dot := strings.LastIndex(path, "."); dot >= 0 {
		path = path[:dot]
	}
	return path
}
