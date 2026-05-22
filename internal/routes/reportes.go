package routes

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

const n8nReporteURL = "https://dezai-automatization-n8n.rj929r.easypanel.host/webhook/reporte-ingresos"

type reportesHandler struct{ db *pgxpool.Pool }

func RegisterReportes(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &reportesHandler{db: db}
	r.Route("/api/reportes", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/ingresos", h.ingresosReport)
	})
}

func fixDate(d string) string {
	if len(d) == 10 {
		return d + "T00:00:00-06:00"
	}
	return d
}

func (h *reportesHandler) ingresosReport(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	fechaInicio := fixDate(r.URL.Query().Get("fecha_inicio"))
	fechaFin := fixDate(r.URL.Query().Get("fecha_fin"))

	if fechaInicio == "" || fechaFin == "" {
		http.Error(w, `{"error":"fecha_inicio y fecha_fin son requeridos"}`, http.StatusBadRequest)
		return
	}

	// Negocio data
	var nombreNegocio, direccion string
	h.db.QueryRow(r.Context(),
		`SELECT nombre_negocio, COALESCE(direccion,'') FROM negocios WHERE id=$1`, nid).
		Scan(&nombreNegocio, &direccion)

	// Raw API key (empty string if the request used Firebase Bearer auth)
	apiKey := r.Header.Get("X-API-Key")

	// Build n8n payload
	payload := map[string]any{
		"negocio_id":    nid,
		"fecha_inicio":  fechaInicio,
		"fecha_fin":     fechaFin,
		"api_key":       apiKey,
		"nombre_negocio": nombreNegocio,
		"direccion":     direccion,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, n8nReporteURL, bytes.NewReader(body))
	if err != nil {
		log.Printf("[reportes] build request: %v", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[reportes] n8n call: %v", err)
		http.Error(w, `{"error":"no se pudo generar el reporte"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[reportes] n8n status %d", resp.StatusCode)
		http.Error(w, `{"error":"error al generar el reporte"}`, http.StatusBadGateway)
		return
	}

	pdfBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[reportes] read pdf: %v", err)
		http.Error(w, `{"error":"error al leer el reporte"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="reporte-ingresos.pdf"`)
	w.Write(pdfBytes)
}
