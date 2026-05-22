package routes

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type fidelidadHandler struct{ db *pgxpool.Pool }

func RegisterConfiguracionFidelidad(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &fidelidadHandler{db: db}
	r.Route("/api/configuracion-fidelidad", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.getCfg)
		r.Put("/", h.putCfg)
	})
	r.Route("/api/fidelidad", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/cliente/{id}", h.getCliente)
		r.Put("/cliente/{id}/usar-recompensa", h.usarRecompensa)
	})
}

// ── Plan guard ───────────────────────────────────────────────────

func requirePro(r *http.Request, db *pgxpool.Pool, nid int, w http.ResponseWriter) bool {
	var plan string
	db.QueryRow(r.Context(),
		`SELECT u.plan FROM usuarios u
		 JOIN negocios n ON u.uid = n.uid
		 WHERE n.id = $1`, nid).Scan(&plan)
	if plan != "pro" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "Esta función requiere DEZAI Agent"})
		return false
	}
	return true
}

// ── Configuración ─────────────────────────────────────────────────

const defaultMensajeFidelidad = "{nombre} completó su cita #{numero} y tiene un descuento pendiente 🎉"

type configFidelidadRow struct {
	NegocioID            int     `json:"negocio_id"`
	Activo               bool    `json:"activo"`
	CitasParaRecompensa  int     `json:"citas_para_recompensa"`
	TipoDescuento        string  `json:"tipo_descuento"`
	ValorDescuento       float64 `json:"valor_descuento"`
	MensajeRecompensa    string  `json:"mensaje_recompensa"`
}

const cfgFidCols = `negocio_id, activo, citas_para_recompensa, tipo_descuento, valor_descuento, mensaje_recompensa`

func scanCfgFidelidad(row interface{ Scan(...any) error }) (configFidelidadRow, error) {
	var c configFidelidadRow
	return c, row.Scan(&c.NegocioID, &c.Activo, &c.CitasParaRecompensa,
		&c.TipoDescuento, &c.ValorDescuento, &c.MensajeRecompensa)
}

func (h *fidelidadHandler) getCfg(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}
	if !requirePro(r, h.db, nid, w) {
		return
	}

	cfg, err := scanCfgFidelidad(h.db.QueryRow(r.Context(),
		`SELECT `+cfgFidCols+` FROM configuracion_fidelidad WHERE negocio_id=$1`, nid))
	if err != nil {
		cfg = configFidelidadRow{
			NegocioID:           nid,
			Activo:              false,
			CitasParaRecompensa: 10,
			TipoDescuento:       "porcentaje",
			ValorDescuento:      10,
			MensajeRecompensa:   defaultMensajeFidelidad,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

type putCfgFidelidadRequest struct {
	Activo              bool    `json:"activo"`
	CitasParaRecompensa int     `json:"citas_para_recompensa"`
	TipoDescuento       string  `json:"tipo_descuento"`
	ValorDescuento      float64 `json:"valor_descuento"`
	MensajeRecompensa   string  `json:"mensaje_recompensa"`
}

func (h *fidelidadHandler) putCfg(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}
	if !requirePro(r, h.db, nid, w) {
		return
	}

	var req putCfgFidelidadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}
	if req.CitasParaRecompensa <= 0 {
		req.CitasParaRecompensa = 10
	}
	if strings.TrimSpace(req.MensajeRecompensa) == "" {
		req.MensajeRecompensa = defaultMensajeFidelidad
	}
	if req.TipoDescuento != "porcentaje" && req.TipoDescuento != "monto" {
		req.TipoDescuento = "porcentaje"
	}
	if req.ValorDescuento < 0 {
		req.ValorDescuento = 0
	}

	cfg, err := scanCfgFidelidad(h.db.QueryRow(r.Context(),
		`INSERT INTO configuracion_fidelidad (`+cfgFidCols+`)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (negocio_id) DO UPDATE SET
		   activo                = EXCLUDED.activo,
		   citas_para_recompensa = EXCLUDED.citas_para_recompensa,
		   tipo_descuento        = EXCLUDED.tipo_descuento,
		   valor_descuento       = EXCLUDED.valor_descuento,
		   mensaje_recompensa    = EXCLUDED.mensaje_recompensa
		 RETURNING `+cfgFidCols,
		nid, req.Activo, req.CitasParaRecompensa,
		req.TipoDescuento, req.ValorDescuento, req.MensajeRecompensa))
	if err != nil {
		http.Error(w, `{"error":"failed to save"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

// ── Recompensas cliente ───────────────────────────────────────────

type recompensaClienteRow struct {
	ClienteID           int        `json:"cliente_id"`
	CitasCompletadas    int        `json:"citas_completadas"`
	RecompensaPendiente bool       `json:"recompensa_pendiente"`
	UltimaRecompensaAt  *time.Time `json:"ultima_recompensa_at"`
}

func (h *fidelidadHandler) getCliente(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}
	if !requirePro(r, h.db, nid, w) {
		return
	}

	clienteID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}

	rc := recompensaClienteRow{ClienteID: clienteID}
	err = h.db.QueryRow(r.Context(),
		`SELECT citas_completadas, recompensa_pendiente, ultima_recompensa_at
		 FROM recompensas_clientes
		 WHERE negocio_id=$1 AND cliente_id=$2`, nid, clienteID).
		Scan(&rc.CitasCompletadas, &rc.RecompensaPendiente, &rc.UltimaRecompensaAt)
	if err != nil {
		rc.CitasCompletadas = 0
		rc.RecompensaPendiente = false
		rc.UltimaRecompensaAt = nil
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rc)
}

func (h *fidelidadHandler) usarRecompensa(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}
	if !requirePro(r, h.db, nid, w) {
		return
	}

	clienteID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}

	tag, err := h.db.Exec(r.Context(),
		`UPDATE recompensas_clientes
		 SET recompensa_pendiente = false,
		     ultima_recompensa_at = NOW(),
		     updated_at           = NOW()
		 WHERE negocio_id=$1 AND cliente_id=$2`, nid, clienteID)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, `{"error":"recompensa not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true})
}
