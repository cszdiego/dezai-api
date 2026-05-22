package routes

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type clientesHandler struct{ db *pgxpool.Pool }

func RegisterClientes(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &clientesHandler{db: db}
	r.Route("/api/clientes", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Get("/{id}", h.detail)
		r.Put("/{id}", h.update)
		r.Delete("/{id}", h.delete)
	})
}

type clienteRow struct {
	ID              int             `json:"id"`
	NegocioID       int             `json:"negocio_id"`
	Nombre          string          `json:"nombre"`
	Apellido        *string         `json:"apellido,omitempty"`
	Telefono        string          `json:"telefono"`
	FechaNacimiento *time.Time      `json:"fecha_nacimiento,omitempty"`
	NotasInternas   *string         `json:"notas_internas,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	ProximaCita     json.RawMessage `json:"proxima_cita"`
}

type citaResumenCliente struct {
	ID        int       `json:"id"`
	FechaHora time.Time `json:"fecha_hora"`
	Status    string    `json:"status"`
}

type clienteDetalle struct {
	clienteRow
	UltimaCita  *citaResumenCliente `json:"ultima_cita"`
	ProximaCita *citaResumenCliente `json:"proxima_cita"`
}

const clienteColumns = `id, negocio_id, nombre, apellido, telefono, fecha_nacimiento, notas_internas, created_at`

func scanCliente(row interface{ Scan(...any) error }) (clienteRow, error) {
	var c clienteRow
	return c, row.Scan(&c.ID, &c.NegocioID, &c.Nombre, &c.Apellido,
		&c.Telefono, &c.FechaNacimiento, &c.NotasInternas, &c.CreatedAt)
}

func (h *clientesHandler) list(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	const baseSelect = `SELECT c.id, c.negocio_id, c.nombre, c.apellido, c.telefono,
		        c.fecha_nacimiento, c.notas_internas, c.created_at,
		        (SELECT json_build_object('id', ci.id, 'fecha_hora', ci.fecha_hora, 'status', ci.status)
		         FROM citas ci
		         WHERE ci.cliente_id = c.id
		           AND ci.fecha_hora >= NOW()
		           AND ci.status NOT IN ('cancelada')
		         ORDER BY ci.fecha_hora ASC LIMIT 1) AS proxima_cita
		 FROM clientes c`

	var rows pgx.Rows
	var err error

	if search := r.URL.Query().Get("search"); search != "" {
		pattern := "%" + search + "%"
		rows, err = h.db.Query(r.Context(),
			baseSelect+`
			 WHERE c.negocio_id = $1
			   AND (LOWER(c.nombre)   LIKE LOWER($2)
			    OR  LOWER(c.apellido) LIKE LOWER($2)
			    OR  c.telefono        LIKE $2)
			 ORDER BY c.apellido, c.nombre`, nid, pattern)
	} else {
		rows, err = h.db.Query(r.Context(),
			baseSelect+` WHERE c.negocio_id = $1 ORDER BY c.apellido, c.nombre`, nid)
	}
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := []clienteRow{}
	for rows.Next() {
		var c clienteRow
		var proximaBuf []byte
		if err := rows.Scan(&c.ID, &c.NegocioID, &c.Nombre, &c.Apellido,
			&c.Telefono, &c.FechaNacimiento, &c.NotasInternas, &c.CreatedAt,
			&proximaBuf); err != nil {
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		if len(proximaBuf) > 0 {
			c.ProximaCita = json.RawMessage(proximaBuf)
		} else {
			c.ProximaCita = json.RawMessage("null")
		}
		list = append(list, c)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func (h *clientesHandler) detail(w http.ResponseWriter, r *http.Request) {
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

	c, err := scanCliente(h.db.QueryRow(r.Context(),
		`SELECT `+clienteColumns+` FROM clientes WHERE id=$1 AND negocio_id=$2`, id, nid))
	if err != nil {
		http.Error(w, `{"error":"cliente not found"}`, http.StatusNotFound)
		return
	}

	det := clienteDetalle{clienteRow: c}

	var u citaResumenCliente
	if err := h.db.QueryRow(r.Context(),
		`SELECT id, fecha_hora, status FROM citas
		 WHERE cliente_id=$1 AND fecha_hora < NOW() ORDER BY fecha_hora DESC LIMIT 1`, id,
	).Scan(&u.ID, &u.FechaHora, &u.Status); err == nil {
		det.UltimaCita = &u
	}

	var p citaResumenCliente
	if err := h.db.QueryRow(r.Context(),
		`SELECT id, fecha_hora, status FROM citas
		 WHERE cliente_id=$1 AND fecha_hora >= NOW() AND status NOT IN ('cancelada')
		 ORDER BY fecha_hora ASC LIMIT 1`, id,
	).Scan(&p.ID, &p.FechaHora, &p.Status); err == nil {
		det.ProximaCita = &p
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(det)
}

type clienteRequest struct {
	Nombre          string  `json:"nombre"`
	Apellido        *string `json:"apellido"`
	Telefono        string  `json:"telefono"`
	FechaNacimiento *string `json:"fecha_nacimiento"`
	NotasInternas   *string `json:"notas_internas"`
}

func (h *clientesHandler) create(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req clienteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Nombre == "" || req.Telefono == "" {
		http.Error(w, `{"error":"nombre and telefono are required"}`, http.StatusBadRequest)
		return
	}

	c, err := scanCliente(h.db.QueryRow(r.Context(),
		`INSERT INTO clientes (negocio_id, nombre, apellido, telefono, fecha_nacimiento, notas_internas)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+clienteColumns,
		nid, req.Nombre, req.Apellido, req.Telefono, req.FechaNacimiento, req.NotasInternas))
	if err != nil {
		http.Error(w, `{"error":"failed to create cliente"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(c)
}

func (h *clientesHandler) update(w http.ResponseWriter, r *http.Request) {
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

	var req clienteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Nombre == "" || req.Telefono == "" {
		http.Error(w, `{"error":"nombre and telefono are required"}`, http.StatusBadRequest)
		return
	}

	c, err := scanCliente(h.db.QueryRow(r.Context(),
		`UPDATE clientes SET nombre=$3, apellido=$4, telefono=$5, fecha_nacimiento=$6, notas_internas=$7
		 WHERE id=$1 AND negocio_id=$2 RETURNING `+clienteColumns,
		id, nid, req.Nombre, req.Apellido, req.Telefono, req.FechaNacimiento, req.NotasInternas))
	if err != nil {
		http.Error(w, `{"error":"cliente not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(c)
}

func (h *clientesHandler) delete(w http.ResponseWriter, r *http.Request) {
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
		`DELETE FROM clientes WHERE id=$1 AND negocio_id=$2`, id, nid)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, `{"error":"cliente not found"}`, http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
