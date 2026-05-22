package routes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type ventasHandler struct{ db *pgxpool.Pool }

func RegisterVentas(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &ventasHandler{db: db}
	r.Route("/api/ventas", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/citas-pendientes", h.citasPendientes)
		r.Get("/", h.list)
		r.Post("/", h.create)
	})
}

type ventaRow struct {
	ID               int       `json:"id"`
	NegocioID        int       `json:"negocio_id"`
	CitaID           *int      `json:"cita_id,omitempty"`
	ClienteID        *int      `json:"cliente_id,omitempty"`
	ServicioID       *int      `json:"servicio_id,omitempty"`
	TrabajadorID     *int      `json:"trabajador_id,omitempty"`
	Monto            float64   `json:"monto"`
	Propina          float64   `json:"propina"`
	PropinaTipo      string    `json:"propina_tipo"`
	MetodoPago       string    `json:"metodo_pago"`
	Descuento        float64   `json:"descuento"`
	DescuentoTipo    string    `json:"descuento_tipo"`
	HoraVenta        time.Time `json:"hora_venta"`
	CreatedAt        time.Time `json:"created_at"`
	NombreCliente    *string   `json:"nombre_cliente,omitempty"`
	NombreServicio   *string   `json:"nombre_servicio,omitempty"`
	NombreTrabajador *string   `json:"nombre_trabajador,omitempty"`
	Total            float64   `json:"total"`
}

const selectVentaFull = `
	SELECT v.id, v.negocio_id, v.cita_id, v.cliente_id, v.servicio_id, v.trabajador_id,
	       v.monto, v.propina, v.propina_tipo, v.metodo_pago, v.descuento, v.descuento_tipo,
	       v.hora_venta, v.created_at,
	       CASE WHEN c.id IS NOT NULL THEN c.nombre || COALESCE(' ' || c.apellido, '') ELSE NULL END,
	       s.nombre,
	       CASE WHEN t.id IS NOT NULL THEN t.nombre || COALESCE(' ' || t.apellido, '') ELSE NULL END
	FROM ventas v
	LEFT JOIN clientes c ON c.id = v.cliente_id
	LEFT JOIN servicios s ON s.id = v.servicio_id
	LEFT JOIN trabajadores t ON t.id = v.trabajador_id`

func scanVentaFull(row interface{ Scan(...any) error }) (ventaRow, error) {
	var v ventaRow
	err := row.Scan(
		&v.ID, &v.NegocioID, &v.CitaID, &v.ClienteID, &v.ServicioID, &v.TrabajadorID,
		&v.Monto, &v.Propina, &v.PropinaTipo, &v.MetodoPago, &v.Descuento, &v.DescuentoTipo,
		&v.HoraVenta, &v.CreatedAt,
		&v.NombreCliente, &v.NombreServicio, &v.NombreTrabajador,
	)
	if err != nil {
		return v, err
	}
	// BUG 3: calcular total con propina real y descuento real
	propina := v.Propina
	if v.PropinaTipo == "porcentaje" {
		propina = v.Monto * v.Propina / 100.0
	}
	descuento := v.Descuento
	if v.DescuentoTipo == "porcentaje" {
		descuento = v.Monto * v.Descuento / 100.0
	}
	v.Total = v.Monto + propina - descuento
	return v, nil
}

func (h *ventasHandler) list(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	q := r.URL.Query()
	args := []any{nid}
	where := "v.negocio_id = $1"

	if v := q.Get("cliente_id"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			args = append(args, id)
			where += fmt.Sprintf(" AND v.cliente_id = $%d", len(args))
		}
	}
	if v := q.Get("servicio_id"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			args = append(args, id)
			where += fmt.Sprintf(" AND v.servicio_id = $%d", len(args))
		}
	}
	if v := q.Get("trabajador_id"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			args = append(args, id)
			where += fmt.Sprintf(" AND v.trabajador_id = $%d", len(args))
		}
	}
	if v := q.Get("metodo_pago"); v != "" {
		args = append(args, v)
		where += fmt.Sprintf(" AND v.metodo_pago = $%d", len(args))
	}
	if v := q.Get("fecha_inicio"); v != "" {
		if len(v) == 10 {
			v = v + "T00:00:00-06:00"
		}
		args = append(args, v)
		where += fmt.Sprintf(" AND v.hora_venta >= $%d", len(args))
	}
	if v := q.Get("fecha_fin"); v != "" {
		if len(v) == 10 {
			v = v + "T23:59:59-06:00"
		}
		args = append(args, v)
		where += fmt.Sprintf(" AND v.hora_venta <= $%d", len(args))
	}

	rows, err := h.db.Query(r.Context(),
		selectVentaFull+" WHERE "+where+" ORDER BY v.hora_venta DESC", args...)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := []ventaRow{}
	for rows.Next() {
		v, err := scanVentaFull(rows)
		if err != nil {
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		list = append(list, v)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

type ventaRequest struct {
	CitaID        *int    `json:"cita_id"`
	ClienteID     *int    `json:"cliente_id"`
	ServicioID    *int    `json:"servicio_id"`
	TrabajadorID  *int    `json:"trabajador_id"`
	Monto         float64 `json:"monto"`
	Propina       float64 `json:"propina"`
	PropinaTipo   string  `json:"propina_tipo"`
	MetodoPago    string  `json:"metodo_pago"`
	Descuento     float64 `json:"descuento"`
	DescuentoTipo string  `json:"descuento_tipo"`
	HoraVenta     *string `json:"hora_venta"`
}

func (h *ventasHandler) create(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req ventaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	// Auto-fill monto from service when not provided
	monto := req.Monto
	if monto == 0 && req.ServicioID != nil {
		var precio float64
		if err := h.db.QueryRow(r.Context(),
			`SELECT precio FROM servicios WHERE id=$1 AND negocio_id=$2`,
			req.ServicioID, nid).Scan(&precio); err == nil {
			monto = precio
		}
	}

	// Apply defaults
	metodoPago := req.MetodoPago
	if metodoPago == "" {
		metodoPago = "efectivo"
	}
	propinaTipo := req.PropinaTipo
	if propinaTipo == "" {
		propinaTipo = "monto"
	}
	descuentoTipo := req.DescuentoTipo
	if descuentoTipo == "" {
		descuentoTipo = "monto"
	}

	// Build INSERT — include hora_venta only when provided
	baseArgs := []any{nid, req.CitaID, req.ClienteID, req.ServicioID, req.TrabajadorID,
		monto, req.Propina, propinaTipo, metodoPago, req.Descuento, descuentoTipo}

	var insertSQL string
	var insertArgs []any

	if req.HoraVenta != nil && *req.HoraVenta != "" {
		insertSQL = `INSERT INTO ventas
			(negocio_id, cita_id, cliente_id, servicio_id, trabajador_id, monto, propina, propina_tipo,
			 metodo_pago, descuento, descuento_tipo, hora_venta)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`
		insertArgs = append(baseArgs, *req.HoraVenta)
	} else {
		insertSQL = `INSERT INTO ventas
			(negocio_id, cita_id, cliente_id, servicio_id, trabajador_id, monto, propina, propina_tipo,
			 metodo_pago, descuento, descuento_tipo)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`
		insertArgs = baseArgs
	}

	var newID int
	if err := h.db.QueryRow(r.Context(), insertSQL, insertArgs...).Scan(&newID); err != nil {
		http.Error(w, `{"error":"failed to create venta"}`, http.StatusInternalServerError)
		return
	}

	v, err := scanVentaFull(h.db.QueryRow(r.Context(),
		selectVentaFull+" WHERE v.id=$1", newID))
	if err != nil {
		http.Error(w, `{"error":"failed to fetch created venta"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(v)
}

// citasPendientes returns completed citas without a registered venta.
func (h *ventasHandler) citasPendientes(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT `+citaColumns+` FROM citas c
		 WHERE c.negocio_id=$1 AND c.status='completada'
		   AND NOT EXISTS (SELECT 1 FROM ventas v WHERE v.cita_id = c.id)
		 ORDER BY c.fecha_hora DESC`, nid)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := []citaRow{}
	for rows.Next() {
		c, err := scanCita(rows)
		if err != nil {
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		list = append(list, c)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}
