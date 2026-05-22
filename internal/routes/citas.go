package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type citasHandler struct{ db *pgxpool.Pool }

func RegisterCitas(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &citasHandler{db: db}
	r.Route("/api/citas", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Post("/calificacion", h.registrarCalificacion)
		r.Get("/mis-citas", h.misCitas)
		r.Get("/{id}", h.detail)
		r.Put("/{id}", h.update)
		r.Delete("/{id}", h.cancel)
	})
}

type citaRow struct {
	ID               int        `json:"id"`
	NegocioID        int        `json:"negocio_id"`
	ClienteID        *int       `json:"cliente_id,omitempty"`
	ServicioID       *int       `json:"servicio_id,omitempty"`
	FechaHora        time.Time  `json:"fecha_hora"`
	DuracionMinutos  int        `json:"duracion_minutos"`
	Precio           *float64   `json:"precio,omitempty"`
	Status           string     `json:"status"`
	Notas            *string    `json:"notas,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	TrabajadorID     *int       `json:"trabajador_id,omitempty"`
	ClienteNombre    *string    `json:"cliente_nombre,omitempty"`
	ClienteApellido  *string    `json:"cliente_apellido,omitempty"`
	NombreTrabajador *string    `json:"nombre_trabajador,omitempty"`
}

const citaColumns = `id, negocio_id, cliente_id, servicio_id, fecha_hora,
	duracion_minutos, precio, status, notas, created_at, updated_at, trabajador_id`

func scanCita(row interface{ Scan(...any) error }) (citaRow, error) {
	var c citaRow
	return c, row.Scan(&c.ID, &c.NegocioID, &c.ClienteID, &c.ServicioID,
		&c.FechaHora, &c.DuracionMinutos, &c.Precio, &c.Status, &c.Notas,
		&c.CreatedAt, &c.UpdatedAt, &c.TrabajadorID)
}

func scanCitaWithCliente(row interface{ Scan(...any) error }) (citaRow, error) {
	var c citaRow
	return c, row.Scan(&c.ID, &c.NegocioID, &c.ClienteID, &c.ServicioID,
		&c.FechaHora, &c.DuracionMinutos, &c.Precio, &c.Status, &c.Notas,
		&c.CreatedAt, &c.UpdatedAt, &c.TrabajadorID,
		&c.ClienteNombre, &c.ClienteApellido, &c.NombreTrabajador)
}

func (h *citasHandler) list(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	q := r.URL.Query()
	args := []any{nid}
	where := "c.negocio_id = $1"

	if v := q.Get("fecha_inicio"); v != "" {
		if len(v) == 10 {
			v = v + "T00:00:00-06:00"
		}
		args = append(args, v)
		where += fmt.Sprintf(" AND c.fecha_hora >= $%d", len(args))
	}
	if v := q.Get("fecha_fin"); v != "" {
		if len(v) == 10 {
			v = v + "T23:59:59-06:00"
		}
		args = append(args, v)
		where += fmt.Sprintf(" AND c.fecha_hora <= $%d", len(args))
	}
	if v := q.Get("trabajador_id"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			args = append(args, id)
			where += fmt.Sprintf(" AND c.trabajador_id = $%d", len(args))
		}
	}
	if v := q.Get("status"); v != "" {
		statuses := strings.Split(v, ",")
		phs := make([]string, len(statuses))
		for i, s := range statuses {
			args = append(args, strings.TrimSpace(s))
			phs[i] = fmt.Sprintf("$%d", len(args))
		}
		where += " AND c.status IN (" + strings.Join(phs, ",") + ")"
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT c.id, c.negocio_id, c.cliente_id, c.servicio_id, c.fecha_hora,
		        c.duracion_minutos, c.precio, c.status, c.notas, c.created_at, c.updated_at,
		        c.trabajador_id,
		        cl.nombre, cl.apellido,
		        t.nombre || COALESCE(' ' || t.apellido, '')
		 FROM citas c
		 LEFT JOIN clientes cl ON cl.id = c.cliente_id
		 LEFT JOIN trabajadores t ON t.id = c.trabajador_id
		 WHERE `+where+` ORDER BY c.fecha_hora ASC`, args...)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := []citaRow{}
	for rows.Next() {
		c, err := scanCitaWithCliente(rows)
		if err != nil {
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		list = append(list, c)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func (h *citasHandler) detail(w http.ResponseWriter, r *http.Request) {
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

	c, err := scanCita(h.db.QueryRow(r.Context(),
		`SELECT `+citaColumns+` FROM citas WHERE id=$1 AND negocio_id=$2`, id, nid))
	if err != nil {
		http.Error(w, `{"error":"cita not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(c)
}

type createCitaRequest struct {
	ClienteID       *int     `json:"cliente_id"`
	ServicioID      *int     `json:"servicio_id"`
	TrabajadorID    *int     `json:"trabajador_id"`
	FechaHora       string   `json:"fecha_hora"`
	DuracionMinutos *int     `json:"duracion_minutos"`
	Precio          *float64 `json:"precio"`
	Notas           *string  `json:"notas"`
	Status          *string  `json:"status"`
}

func (h *citasHandler) create(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req createCitaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.FechaHora == "" {
		http.Error(w, `{"error":"fecha_hora is required"}`, http.StatusBadRequest)
		return
	}

	dur := 60
	var precio *float64

	if req.ServicioID != nil {
		var d int
		var p float64
		if err := h.db.QueryRow(r.Context(),
			`SELECT duracion_minutos, precio FROM servicios WHERE id=$1 AND negocio_id=$2`,
			req.ServicioID, nid).Scan(&d, &p); err == nil {
			dur = d
			precio = &p
		}
	}
	if req.DuracionMinutos != nil {
		dur = *req.DuracionMinutos
	}
	if req.Precio != nil {
		precio = req.Precio
	}

	// Leer límite por hora desde configuración de agenda
	var maxCitasPorHora int
	h.db.QueryRow(r.Context(),
		`SELECT max_citas_por_hora FROM configuracion_agenda WHERE negocio_id=$1`, nid).
		Scan(&maxCitasPorHora)
	if maxCitasPorHora == 0 {
		maxCitasPorHora = 1
	}

	initialStatus := "agendada"
	if req.Status != nil && *req.Status != "" {
		initialStatus = *req.Status
	}

	// Verificar límite de citas por hora para el trabajador
	if req.TrabajadorID != nil {
		var count int
		h.db.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM citas
			 WHERE trabajador_id=$1 AND negocio_id=$2 AND status != 'cancelada'
			   AND fecha_hora >= DATE_TRUNC('hour', $3::timestamptz)
			   AND fecha_hora <  DATE_TRUNC('hour', $3::timestamptz) + INTERVAL '1 hour'`,
			req.TrabajadorID, nid, req.FechaHora).Scan(&count)
		if count >= maxCitasPorHora {
			http.Error(w, `{"error":"trabajador no disponible en ese horario"}`, http.StatusConflict)
			return
		}
	}

	c, err := scanCita(h.db.QueryRow(r.Context(),
		`INSERT INTO citas
		   (negocio_id, cliente_id, servicio_id, fecha_hora, duracion_minutos, precio, notas, trabajador_id, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+citaColumns,
		nid, req.ClienteID, req.ServicioID, req.FechaHora, dur, precio, req.Notas, req.TrabajadorID, initialStatus))
	if err != nil {
		http.Error(w, `{"error":"failed to create cita"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(c)
}

type updateCitaRequest struct {
	ClienteID       *int     `json:"cliente_id"`
	ServicioID      *int     `json:"servicio_id"`
	TrabajadorID    *int     `json:"trabajador_id"`
	FechaHora       *string  `json:"fecha_hora"`
	DuracionMinutos *int     `json:"duracion_minutos"`
	Precio          *float64 `json:"precio"`
	Status          *string  `json:"status"`
	Notas           *string  `json:"notas"`
}

func (h *citasHandler) update(w http.ResponseWriter, r *http.Request) {
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

	var req updateCitaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	resetRecordatorio := ""
	if req.FechaHora != nil {
		resetRecordatorio = ",\n\t\t   recordatorio_enviado = false"
	}

	c, err := scanCita(h.db.QueryRow(r.Context(),
		`UPDATE citas SET
		   cliente_id       = COALESCE($3,  cliente_id),
		   servicio_id      = COALESCE($4,  servicio_id),
		   fecha_hora       = COALESCE($5::timestamptz, fecha_hora),
		   duracion_minutos = COALESCE($6,  duracion_minutos),
		   precio           = COALESCE($7,  precio),
		   status           = COALESCE($8,  status),
		   notas            = COALESCE($9,  notas),
		   trabajador_id    = COALESCE($10, trabajador_id),
		   updated_at       = NOW()`+resetRecordatorio+`
		 WHERE id=$1 AND negocio_id=$2 RETURNING `+citaColumns,
		id, nid, req.ClienteID, req.ServicioID, req.FechaHora,
		req.DuracionMinutos, req.Precio, req.Status, req.Notas, req.TrabajadorID))
	if err != nil {
		http.Error(w, `{"error":"cita not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(c)
}

func (h *citasHandler) cancel(w http.ResponseWriter, r *http.Request) {
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

	c, err := scanCita(h.db.QueryRow(r.Context(),
		`UPDATE citas SET status='cancelada', updated_at=NOW()
		 WHERE id=$1 AND negocio_id=$2 RETURNING `+citaColumns, id, nid))
	if err != nil {
		http.Error(w, `{"error":"cita not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(c)
}

// ── POST /api/citas/calificacion ──────────────────────────────────

type calificacionRequest struct {
	Telefono     string  `json:"telefono"`
	Calificacion int     `json:"calificacion"`
	Comentario   *string `json:"comentario"`
}

func (h *citasHandler) registrarCalificacion(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req calificacionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Telefono == "" {
		http.Error(w, `{"error":"telefono y calificacion son requeridos"}`, http.StatusBadRequest)
		return
	}
	if req.Calificacion < 1 || req.Calificacion > 5 {
		http.Error(w, `{"error":"calificacion debe ser entre 1 y 5"}`, http.StatusBadRequest)
		return
	}

	// Cita más reciente completada sin calificar del cliente con ese teléfono
	var citaID int
	err := h.db.QueryRow(r.Context(),
		`SELECT ci.id
		 FROM citas ci
		 JOIN clientes cl ON cl.id = ci.cliente_id
		 WHERE ci.negocio_id   = $1
		   AND cl.telefono     = $2
		   AND ci.status       = 'completada'
		   AND ci.calificacion IS NULL
		 ORDER BY ci.fecha_hora DESC
		 LIMIT 1`,
		nid, req.Telefono).Scan(&citaID)
	if err != nil {
		http.Error(w, `{"error":"No se encontró cita pendiente de calificación"}`, http.StatusNotFound)
		return
	}

	if _, err := h.db.Exec(r.Context(),
		`UPDATE citas
		 SET calificacion                 = $1,
		     calificacion_fecha_respuesta = NOW(),
		     comentario                   = $2,
		     updated_at                   = NOW()
		 WHERE id = $3`,
		req.Calificacion, req.Comentario, citaID); err != nil {
		http.Error(w, `{"error":"failed to save calificacion"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true, "cita_id": citaID})
}

// ── AutocompletarCitas (job periódico) ────────────────────────────
// Marca como 'completada' toda cita cuya fecha_hora + duración ya pasó,
// para negocios con confirmacion_automatica = true en configuracion_agenda.

func AutocompletarCitas(db *pgxpool.Pool) {
	ctx := context.Background()

	rows, err := db.Query(ctx,
		`SELECT negocio_id FROM configuracion_agenda WHERE confirmacion_automatica = true`)
	if err != nil {
		log.Printf("[AutocompletarCitas] query negocios: %v", err)
		return
	}

	var negocioIDs []int
	for rows.Next() {
		var nid int
		if rows.Scan(&nid) == nil {
			negocioIDs = append(negocioIDs, nid)
		}
	}
	rows.Close()

	for _, nid := range negocioIDs {
		// Mark citas as completed; get back IDs and cliente_ids for fidelidad
		citaRows, err := db.Query(ctx,
			`UPDATE citas
			 SET status     = 'completada',
			     updated_at = NOW()
			 WHERE negocio_id = $1
			   AND status IN ('agendada', 'confirmada', 'reagendada')
			   AND fecha_hora + (duracion_minutos * INTERVAL '1 minute') < NOW()
			 RETURNING id, cliente_id`,
			nid)
		if err != nil {
			log.Printf("[AutocompletarCitas] negocio %d: %v", nid, err)
			continue
		}

		type completedCita struct {
			id        int
			clienteID *int
		}
		var completed []completedCita
		for citaRows.Next() {
			var cc completedCita
			if citaRows.Scan(&cc.id, &cc.clienteID) == nil {
				completed = append(completed, cc)
			}
		}
		citaRows.Close()

		if len(completed) == 0 {
			continue
		}
		log.Printf("[AutocompletarCitas] negocio %d: %d citas completadas", nid, len(completed))

		// Fidelidad: skip if not configured/active
		var fidActivo bool
		var citasPara int
		db.QueryRow(ctx,
			`SELECT cf.activo, cf.citas_para_recompensa
			 FROM configuracion_fidelidad cf
			 JOIN negocios n ON n.id = cf.negocio_id
			 JOIN usuarios u ON u.uid = n.uid
			 WHERE cf.negocio_id=$1 AND u.plan = 'pro'`, nid).
			Scan(&fidActivo, &citasPara)

		if !fidActivo || citasPara <= 0 {
			continue
		}

		for _, cc := range completed {
			if cc.clienteID == nil {
				continue
			}

			var nuevasCitas int
			if err := db.QueryRow(ctx,
				`INSERT INTO recompensas_clientes (negocio_id, cliente_id, citas_completadas, updated_at)
				 VALUES ($1, $2, 1, NOW())
				 ON CONFLICT (negocio_id, cliente_id) DO UPDATE
				 SET citas_completadas = recompensas_clientes.citas_completadas + 1,
				     updated_at        = NOW()
				 RETURNING citas_completadas`,
				nid, *cc.clienteID).Scan(&nuevasCitas); err != nil {
				continue
			}

			if nuevasCitas%citasPara != 0 {
				continue
			}

			// Threshold reached — mark reward pending
			db.Exec(ctx,
				`UPDATE recompensas_clientes SET recompensa_pendiente=true, updated_at=NOW()
				 WHERE negocio_id=$1 AND cliente_id=$2`, nid, *cc.clienteID)

			// Get client name
			var nombre, apellido string
			db.QueryRow(ctx,
				`SELECT nombre, COALESCE(apellido,'') FROM clientes WHERE id=$1`,
				*cc.clienteID).Scan(&nombre, &apellido)
			nombreCompleto := nombre
			if apellido != "" {
				nombreCompleto = nombre + " " + apellido
			}

			titulo := "🎉 Recompensa desbloqueada"
			msg := fmt.Sprintf("🎉 %s completó su cita #%d y tiene una recompensa disponible", nombreCompleto, nuevasCitas)

			db.Exec(ctx,
				`INSERT INTO notificaciones (negocio_id, titulo, mensaje, tipo)
				 VALUES ($1,$2,$3,'fidelidad_recompensa')`,
				nid, titulo, msg)

			go pushToNegocio(db, nid, titulo, msg)
		}
	}
}

// ── GET /api/citas/mis-citas?telefono=XXXXXXXXXX ──────────────────

type misCitaRow struct {
	ID             int    `json:"id"`
	FechaHora      string `json:"fecha_hora"`
	Status         string `json:"status"`
	ServicioNombre string `json:"servicio_nombre"`
	ServicioID     *int   `json:"servicio_id"`
	ClienteID      *int   `json:"cliente_id"`
	ClienteNombre  string `json:"cliente_nombre"`
}

func (h *citasHandler) misCitas(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	telefono := strings.TrimSpace(r.URL.Query().Get("telefono"))
	if telefono == "" {
		http.Error(w, `{"error":"telefono es requerido"}`, http.StatusBadRequest)
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT c.id, c.fecha_hora, c.status,
		        COALESCE(s.nombre, '') AS servicio_nombre,
		        c.servicio_id,
		        c.cliente_id,
		        COALESCE(cl.nombre || ' ' || COALESCE(cl.apellido,''), cl.nombre) AS cliente_nombre
		 FROM citas c
		 JOIN clientes cl ON cl.id = c.cliente_id AND cl.negocio_id = $1
		 LEFT JOIN servicios s ON s.id = c.servicio_id
		 WHERE c.negocio_id = $1
		   AND cl.telefono = $2
		   AND c.fecha_hora >= NOW()
		   AND c.status IN ('agendada', 'confirmada', 'reagendada')
		 ORDER BY c.fecha_hora ASC
		 LIMIT 5`,
		nid, telefono)
	if err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	result := []misCitaRow{}
	for rows.Next() {
		var row misCitaRow
		var fh time.Time
		if err := rows.Scan(&row.ID, &fh, &row.Status,
			&row.ServicioNombre, &row.ServicioID,
			&row.ClienteID, &row.ClienteNombre); err != nil {
			continue
		}
		row.FechaHora = fh.Format(time.RFC3339)
		result = append(result, row)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
