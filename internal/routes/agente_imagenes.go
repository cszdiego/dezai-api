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

type agenteImagenesHandler struct{ db *pgxpool.Pool }

func RegisterAgenteImagenes(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &agenteImagenesHandler{db: db}
	r.Route("/api/agente/imagenes", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.list)
		r.Post("/", h.upload)
		r.Put("/{id}", h.update)
		r.Delete("/{id}", h.delete)
	})
}

type agenteImagen struct {
	ID                   int       `json:"id"`
	URL                  string    `json:"url"`
	Descripcion          *string   `json:"descripcion,omitempty"`
	DescripcionIntencion *string   `json:"descripcion_intencion,omitempty"`
	Activo               bool      `json:"activo"`
	CreatedAt            time.Time `json:"created_at"`
}

func (h *agenteImagenesHandler) list(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, url, descripcion, descripcion_intencion, activo, created_at
		 FROM agente_imagenes WHERE negocio_id = $1 ORDER BY created_at DESC`, nid)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := []agenteImagen{}
	for rows.Next() {
		var item agenteImagen
		if err := rows.Scan(&item.ID, &item.URL, &item.Descripcion, &item.DescripcionIntencion, &item.Activo, &item.CreatedAt); err != nil {
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		list = append(list, item)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func (h *agenteImagenesHandler) upload(w http.ResponseWriter, r *http.Request) {
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

	var descripcion *string
	if v := r.FormValue("descripcion"); v != "" {
		descripcion = &v
	}
	var descripcionIntencion *string
	if v := r.FormValue("descripcion_intencion"); v != "" {
		descripcionIntencion = &v
	}

	cld, err := newUploaderAPI()
	if err != nil {
		http.Error(w, `{"error":"cloudinary init failed"}`, http.StatusInternalServerError)
		return
	}

	publicID := fmt.Sprintf("dezai/negocio_%d/agente/img_%d", nid, time.Now().UnixMilli())

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

	var item agenteImagen
	if err := h.db.QueryRow(r.Context(),
		`INSERT INTO agente_imagenes (negocio_id, url, descripcion, descripcion_intencion)
		 VALUES ($1,$2,$3,$4) RETURNING id, url, descripcion, descripcion_intencion, activo, created_at`,
		nid, resp.SecureURL, descripcion, descripcionIntencion,
	).Scan(&item.ID, &item.URL, &item.Descripcion, &item.DescripcionIntencion, &item.Activo, &item.CreatedAt); err != nil {
		http.Error(w, `{"error":"failed to save image"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(item)
}

func (h *agenteImagenesHandler) update(w http.ResponseWriter, r *http.Request) {
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

	var req struct {
		Descripcion          *string `json:"descripcion"`
		DescripcionIntencion *string `json:"descripcion_intencion"`
		Activo               *bool   `json:"activo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}

	var item agenteImagen
	if err := h.db.QueryRow(r.Context(),
		`UPDATE agente_imagenes
		 SET descripcion=$3, descripcion_intencion=$4, activo=COALESCE($5, activo)
		 WHERE id=$1 AND negocio_id=$2
		 RETURNING id, url, descripcion, descripcion_intencion, activo, created_at`,
		id, nid, req.Descripcion, req.DescripcionIntencion, req.Activo,
	).Scan(&item.ID, &item.URL, &item.Descripcion, &item.DescripcionIntencion, &item.Activo, &item.CreatedAt); err != nil {
		http.Error(w, `{"error":"imagen not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(item)
}

func (h *agenteImagenesHandler) delete(w http.ResponseWriter, r *http.Request) {
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

	var imageURL string
	if err := h.db.QueryRow(r.Context(),
		`SELECT url FROM agente_imagenes WHERE id=$1 AND negocio_id=$2`, id, nid,
	).Scan(&imageURL); err != nil {
		http.Error(w, `{"error":"imagen not found"}`, http.StatusNotFound)
		return
	}

	cld, err := newUploaderAPI()
	if err != nil {
		http.Error(w, `{"error":"cloudinary init failed"}`, http.StatusInternalServerError)
		return
	}
	if pid := agentePublicIDFromURL(imageURL); pid != "" {
		cld.Destroy(context.Background(), uploader.DestroyParams{PublicID: pid})
	}

	if _, err := h.db.Exec(r.Context(),
		`DELETE FROM agente_imagenes WHERE id=$1 AND negocio_id=$2`, id, nid,
	); err != nil {
		http.Error(w, `{"error":"delete failed"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true})
}

// agentePublicIDFromURL extracts the Cloudinary public_id from a secure URL.
func agentePublicIDFromURL(rawURL string) string {
	const marker = "/upload/"
	idx := strings.Index(rawURL, marker)
	if idx < 0 {
		return ""
	}
	path := rawURL[idx+len(marker):]
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
	if dot := strings.LastIndex(path, "."); dot >= 0 {
		path = path[:dot]
	}
	return path
}
