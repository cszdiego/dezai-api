package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type estadisticasHandler struct{ db *pgxpool.Pool }

func RegisterEstadisticas(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &estadisticasHandler{db: db}
	r.Route("/api/estadisticas", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/citas", h.citas)
		r.Get("/clientes", h.clientes)
		r.Get("/ingresos", h.ingresos)
		r.Get("/trabajador/{id}", h.trabajador)
		r.Get("/calificaciones", h.calificaciones)
	})
}

func parseFechasEst(r *http.Request) (inicio, fin string) {
	inicio = r.URL.Query().Get("fecha_inicio")
	fin = r.URL.Query().Get("fecha_fin")
	now := time.Now()
	if inicio == "" {
		inicio = fmt.Sprintf("%d-%02d-01", now.Year(), now.Month())
	}
	if fin == "" {
		fin = fmt.Sprintf("%d-%02d-%02d", now.Year(), now.Month(), now.Day())
	}
	// Mantener como fechas planas YYYY-MM-DD para que mxS/mxE las conviertan
	// correctamente mediante ::date::timestamp AT TIME ZONE 'America/Mexico_City'
	return
}

// tsStart/tsEnd convierten fechas YYYY-MM-DD a timestamps con timezone México
func tsStart(d string) string {
	if len(d) == 10 { return d + "T00:00:00-06:00" }
	return d
}
func tsEnd(d string) string {
	if len(d) == 10 { return d + "T23:59:59-06:00" }
	return d
}

// ── Agrupación adaptativa ─────────────────────────────────────────

type groupBy int

const (
	gHour  groupBy = iota
	gDay
	gWeek
	gMonth
)

func determineGroup(r *http.Request, inicio, fin string) groupBy {
	switch r.URL.Query().Get("periodo") {
	case "hoy":
		return gHour
	case "semana":
		return gDay
	case "año":
		return gMonth
	case "personalizado":
		ini, _ := time.Parse("2006-01-02", inicio)
		finT, _ := time.Parse("2006-01-02", fin)
		days := int(finT.Sub(ini).Hours()/24) + 1
		if days <= 7 {
			return gDay
		} else if days <= 31 {
			return gWeek
		}
		return gMonth
	default:
		return gWeek
	}
}

// ── Helpers de etiquetas ──────────────────────────────────────────

var diasNombres = []string{"Lun", "Mar", "Mié", "Jue", "Vie", "Sáb", "Dom"}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func mesCorto(m time.Month) string {
	return []string{"", "ene", "feb", "mar", "abr", "may", "jun",
		"jul", "ago", "sep", "oct", "nov", "dic"}[int(m)]
}

func allWeekStarts(inicio, fin string) []string {
	ini, _ := time.Parse("2006-01-02", inicio)
	finT, _ := time.Parse("2006-01-02", fin)
	offset := (int(ini.Weekday()) + 6) % 7
	cur := ini.AddDate(0, 0, -offset)
	var labels []string
	for !cur.After(finT) {
		labels = append(labels, fmt.Sprintf("%d %s", cur.Day(), mesCorto(cur.Month())))
		cur = cur.AddDate(0, 0, 7)
	}
	return labels
}

func monthsInRange(inicio, fin string) []string {
	ini, _ := time.Parse("2006-01-02", inicio)
	finT, _ := time.Parse("2006-01-02", fin)
	multiYear := finT.Year() > ini.Year()
	cur := time.Date(ini.Year(), ini.Month(), 1, 0, 0, 0, 0, time.UTC)
	var labels []string
	for !cur.After(finT) {
		l := mesCorto(cur.Month())
		if multiYear {
			l += fmt.Sprintf(" %d", cur.Year())
		}
		labels = append(labels, l)
		cur = cur.AddDate(0, 1, 0)
	}
	return labels
}

func genLabels(gb groupBy, inicio, fin string) []string {
	switch gb {
	case gHour:
		labels := make([]string, 24)
		for i := 0; i < 24; i++ {
			labels[i] = fmt.Sprintf("%02d:00", i)
		}
		return labels
	case gDay:
		return append([]string{}, diasNombres...)
	case gWeek:
		return allWeekStarts(inicio, fin)
	default:
		return monthsInRange(inicio, fin)
	}
}

func weekLabelFromTime(t time.Time) string {
	return fmt.Sprintf("%d %s", t.Day(), mesCorto(t.Month()))
}

// dayIdx convierte un time.Weekday a índice Lun=0…Dom=6
func dayIdx(wd time.Weekday) int { return (int(wd) + 6) % 7 }

// ── Tipos de respuesta ────────────────────────────────────────────

type diaStat   struct { Dia      string `json:"dia"`; Cantidad int `json:"cantidad"` }
type horaStat  struct { Hora     string `json:"hora"`; Cantidad int `json:"cantidad"` }
type svcCitaSt struct { Nombre string `json:"nombre"`; Cantidad int `json:"cantidad"`; Porcentaje float64 `json:"porcentaje"` }

type estCitas struct {
	TotalCompletadas  int         `json:"total_completadas"`
	ProximasAgendadas int         `json:"proximas_agendadas"`
	PorDiaSemana      []diaStat   `json:"por_dia_semana"`
	PorHora           []horaStat  `json:"por_hora"`
	PorServicio       []svcCitaSt `json:"por_servicio"`
}

type periodoClienteSt struct {
	Label    string `json:"label"`
	Cantidad int    `json:"cantidad"`
}

type estClientes struct {
	TotalClientes  int                `json:"total_clientes"`
	ClientesNuevos int                `json:"clientes_nuevos"`
	PorPeriodo     []periodoClienteSt `json:"por_periodo"`
}

type periodoStat  struct { Label string `json:"label"`; Monto float64 `json:"monto"` }
type svcIngrSt    struct { Nombre string `json:"nombre"`; Monto float64 `json:"monto"`; Porcentaje float64 `json:"porcentaje"` }
type metodoPagoSt struct {
	Metodo   string  `json:"metodo"`
	Total    float64 `json:"total"`
	Cantidad int     `json:"cantidad"`
}

type estIngresos struct {
	Total         float64        `json:"total"`
	Propinas      float64        `json:"propinas"`
	PorPeriodo    []periodoStat  `json:"por_periodo"`
	PorServicio   []svcIngrSt    `json:"por_servicio"`
	PorMetodoPago []metodoPagoSt `json:"por_metodo_pago"`
}

// ── GET /api/estadisticas/citas ───────────────────────────────────

func (h *estadisticasHandler) citas(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}
	inicio, fin := parseFechasEst(r)
	baseArgs := []any{nid, tsStart(inicio), tsEnd(fin)}

	pastWhere := `negocio_id=$1
		AND fecha_hora >= $2 AND fecha_hora <= $3
		AND status != 'cancelada' AND fecha_hora < NOW()`

	res := estCitas{
		PorDiaSemana: make([]diaStat, 7),
		PorHora:      make([]horaStat, 24),
		PorServicio:  []svcCitaSt{},
	}

	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM citas WHERE `+pastWhere,
		baseArgs...).Scan(&res.TotalCompletadas)

	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM citas
		 WHERE negocio_id=$1 AND fecha_hora >= NOW()
		   AND status IN ('agendada','confirmada','reagendada')`,
		nid).Scan(&res.ProximasAgendadas)

	// Por día de semana — respetar dia_inicio_semana del negocio
	diaInicioSemana := 1
	if r.URL.Query().Get("dia_inicio_semana") == "0" {
		diaInicioSemana = 0
	}
	var diasOrdenados []string
	var dowToIdx func(int) int
	if diaInicioSemana == 1 { // lunes primero
		diasOrdenados = []string{"Lun", "Mar", "Mié", "Jue", "Vie", "Sáb", "Dom"}
		dowToIdx = func(dow int) int {
			if dow == 0 {
				return 6
			}
			return dow - 1
		}
	} else { // domingo primero
		diasOrdenados = []string{"Dom", "Lun", "Mar", "Mié", "Jue", "Vie", "Sáb"}
		dowToIdx = func(dow int) int { return dow }
	}
	for i, d := range diasOrdenados {
		res.PorDiaSemana[i] = diaStat{Dia: d}
	}
	dowMap := map[int]int{}
	if rows, err := h.db.Query(r.Context(),
		`SELECT EXTRACT(DOW FROM fecha_hora AT TIME ZONE 'America/Mexico_City')::int, COUNT(*)
		 FROM citas WHERE `+pastWhere+` GROUP BY 1`, baseArgs...); err == nil {
		defer rows.Close()
		for rows.Next() {
			var dow, cnt int
			rows.Scan(&dow, &cnt)
			dowMap[dow] = cnt
		}
	}
	for dow, cnt := range dowMap {
		res.PorDiaSemana[dowToIdx(dow)].Cantidad = cnt
	}

	// Por hora — 24 slots, todos con ceros (BUG 2: AT TIME ZONE ya presente)
	hourMap := map[int]int{}
	if rows, err := h.db.Query(r.Context(),
		`SELECT EXTRACT(HOUR FROM fecha_hora AT TIME ZONE 'America/Mexico_City')::int, COUNT(*)
		 FROM citas WHERE `+pastWhere+` GROUP BY 1`, baseArgs...); err == nil {
		defer rows.Close()
		for rows.Next() {
			var h2, cnt int
			rows.Scan(&h2, &cnt)
			hourMap[h2] = cnt
		}
	}
	for i := 0; i < 24; i++ {
		res.PorHora[i] = horaStat{Hora: fmt.Sprintf("%02d:00", i), Cantidad: hourMap[i]}
	}

	if rows, err := h.db.Query(r.Context(),
		`SELECT COALESCE(s.nombre, 'Sin servicio'), COUNT(*)
		 FROM citas c LEFT JOIN servicios s ON s.id = c.servicio_id
		 WHERE c.negocio_id=$1
		   AND c.fecha_hora >= $2 AND c.fecha_hora <= $3
		   AND c.status != 'cancelada' AND c.fecha_hora < NOW()
		 GROUP BY 1 ORDER BY 2 DESC`, baseArgs...); err == nil {
		defer rows.Close()
		for rows.Next() {
			var nombre string
			var cnt int
			rows.Scan(&nombre, &cnt)
			pct := 0.0
			if res.TotalCompletadas > 0 {
				pct = round1(float64(cnt) / float64(res.TotalCompletadas) * 100)
			}
			res.PorServicio = append(res.PorServicio, svcCitaSt{Nombre: nombre, Cantidad: cnt, Porcentaje: pct})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// ── buildClientesMap ──────────────────────────────────────────────

func (h *estadisticasHandler) buildClientesMap(ctx context.Context, nid int, inicio, fin string, gb groupBy) map[string]int {
	m := map[string]int{}
	ini, _ := time.Parse("2006-01-02", inicio)
	finT, _ := time.Parse("2006-01-02", fin)
	multiYear := finT.Year() > ini.Year()
	args := []any{nid, tsStart(inicio), tsEnd(fin)}
	base := `FROM clientes WHERE negocio_id=$1 AND created_at >= $2 AND created_at <= $3`

	switch gb {
	case gHour:
		if rows, err := h.db.Query(ctx,
			`SELECT EXTRACT(HOUR FROM (created_at AT TIME ZONE 'UTC') AT TIME ZONE 'America/Mexico_City')::int, COUNT(*) `+base+` GROUP BY 1`,
			args...); err == nil {
			defer rows.Close()
			for rows.Next() {
				var k, v int
				rows.Scan(&k, &v)
				m[fmt.Sprintf("%02d:00", k)] = v
			}
		}
	case gDay: // BUG 5: usar DATE() para agrupar por día en MX
		if rows, err := h.db.Query(ctx,
			`SELECT DATE((created_at AT TIME ZONE 'UTC') AT TIME ZONE 'America/Mexico_City'), COUNT(*) `+base+` GROUP BY 1`,
			args...); err == nil {
			defer rows.Close()
			for rows.Next() {
				var fecha time.Time
				var v int
				rows.Scan(&fecha, &v)
				m[diasNombres[dayIdx(fecha.Weekday())]] = v
			}
		}
	case gWeek:
		if rows, err := h.db.Query(ctx,
			`SELECT DATE_TRUNC('week', (created_at AT TIME ZONE 'UTC') AT TIME ZONE 'America/Mexico_City')::date, COUNT(*) `+base+` GROUP BY 1`,
			args...); err == nil {
			defer rows.Close()
			for rows.Next() {
				var t time.Time
				var v int
				rows.Scan(&t, &v)
				m[weekLabelFromTime(t)] = v
			}
		}
	default:
		if rows, err := h.db.Query(ctx,
			`SELECT EXTRACT(YEAR  FROM (created_at AT TIME ZONE 'UTC') AT TIME ZONE 'America/Mexico_City')::int,
			        EXTRACT(MONTH FROM (created_at AT TIME ZONE 'UTC') AT TIME ZONE 'America/Mexico_City')::int, COUNT(*) `+base+` GROUP BY 1,2`,
			args...); err == nil {
			defer rows.Close()
			for rows.Next() {
				var year, month, v int
				rows.Scan(&year, &month, &v)
				l := mesCorto(time.Month(month))
				if multiYear {
					l += fmt.Sprintf(" %d", year)
				}
				m[l] = v
			}
		}
	}
	return m
}

// ── GET /api/estadisticas/clientes ───────────────────────────────

func (h *estadisticasHandler) clientes(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}
	inicio, fin := parseFechasEst(r)
	gb := determineGroup(r, inicio, fin)

	res := estClientes{PorPeriodo: []periodoClienteSt{}}

	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM clientes WHERE negocio_id=$1`,
		nid).Scan(&res.TotalClientes)

	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM clientes WHERE negocio_id=$1 AND created_at >= $2 AND created_at <= $3`,
		nid, tsStart(inicio), tsEnd(fin)).Scan(&res.ClientesNuevos)

	labels := genLabels(gb, inicio, fin)
	cMap := h.buildClientesMap(r.Context(), nid, inicio, fin, gb)
	for _, l := range labels {
		res.PorPeriodo = append(res.PorPeriodo, periodoClienteSt{Label: l, Cantidad: cMap[l]})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// ── buildIngresosMap ──────────────────────────────────────────────

func (h *estadisticasHandler) buildIngresosMap(ctx context.Context, nid int, inicio, fin string, gb groupBy) map[string]float64 {
	m := map[string]float64{}
	ini, _ := time.Parse("2006-01-02", inicio)
	finT, _ := time.Parse("2006-01-02", fin)
	multiYear := finT.Year() > ini.Year()
	args := []any{nid, tsStart(inicio), tsEnd(fin)}
	base := `FROM ventas WHERE negocio_id=$1 AND hora_venta >= $2 AND hora_venta <= $3`

	switch gb {
	case gHour:
		if rows, err := h.db.Query(ctx,
			`SELECT EXTRACT(HOUR FROM hora_venta AT TIME ZONE 'America/Mexico_City')::int, COALESCE(SUM(monto),0) `+base+` GROUP BY 1`,
			args...); err == nil {
			defer rows.Close()
			for rows.Next() {
				var k int
				var v float64
				rows.Scan(&k, &v)
				m[fmt.Sprintf("%02d:00", k)] = v
			}
		}
	case gDay:
		if rows, err := h.db.Query(ctx,
			`SELECT DATE(hora_venta AT TIME ZONE 'America/Mexico_City'), COALESCE(SUM(monto),0) `+base+` GROUP BY 1`,
			args...); err == nil {
			defer rows.Close()
			for rows.Next() {
				var fecha time.Time
				var v float64
				rows.Scan(&fecha, &v)
				m[diasNombres[dayIdx(fecha.Weekday())]] = v
			}
		}
	case gWeek:
		if rows, err := h.db.Query(ctx,
			`SELECT DATE_TRUNC('week', hora_venta AT TIME ZONE 'America/Mexico_City')::date, COALESCE(SUM(monto),0) `+base+` GROUP BY 1`,
			args...); err == nil {
			defer rows.Close()
			for rows.Next() {
				var t time.Time
				var v float64
				rows.Scan(&t, &v)
				m[weekLabelFromTime(t)] = v
			}
		}
	default:
		if rows, err := h.db.Query(ctx,
			`SELECT EXTRACT(YEAR  FROM hora_venta AT TIME ZONE 'America/Mexico_City')::int,
			        EXTRACT(MONTH FROM hora_venta AT TIME ZONE 'America/Mexico_City')::int, COALESCE(SUM(monto),0) `+base+` GROUP BY 1,2`,
			args...); err == nil {
			defer rows.Close()
			for rows.Next() {
				var year, month int
				var v float64
				rows.Scan(&year, &month, &v)
				l := mesCorto(time.Month(month))
				if multiYear {
					l += fmt.Sprintf(" %d", year)
				}
				m[l] = v
			}
		}
	}
	return m
}

// ── GET /api/estadisticas/ingresos ───────────────────────────────

// BUG 3: expresión de propina real (resuelve porcentaje)
const realPropina = `CASE WHEN propina_tipo='porcentaje' THEN monto*propina/100.0 ELSE propina END`

func (h *estadisticasHandler) ingresos(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}
	inicio, fin := parseFechasEst(r)
	gb := determineGroup(r, inicio, fin)
	baseArgs := []any{nid, tsStart(inicio), tsEnd(fin)}

	rangeWhere := `negocio_id=$1 AND hora_venta >= $2 AND hora_venta <= $3`

	res := estIngresos{
		PorPeriodo:    []periodoStat{},
		PorServicio:   []svcIngrSt{},
		PorMetodoPago: []metodoPagoSt{},
	}

	// BUG 3: propinas reales
	h.db.QueryRow(r.Context(),
		`SELECT COALESCE(SUM(monto),0),
		        COALESCE(SUM(`+realPropina+`),0)
		 FROM ventas WHERE `+rangeWhere,
		baseArgs...).Scan(&res.Total, &res.Propinas)

	labels := genLabels(gb, inicio, fin)
	iMap := h.buildIngresosMap(r.Context(), nid, inicio, fin, gb)
	for _, l := range labels {
		res.PorPeriodo = append(res.PorPeriodo, periodoStat{Label: l, Monto: iMap[l]})
	}

	if rows, err := h.db.Query(r.Context(),
		`SELECT COALESCE(s.nombre,'Sin servicio'),
		        COALESCE(SUM(v.monto + CASE WHEN v.propina_tipo='porcentaje' THEN v.monto*v.propina/100.0 ELSE v.propina END),0)
		 FROM ventas v LEFT JOIN servicios s ON s.id = v.servicio_id
		 WHERE v.negocio_id=$1 AND v.hora_venta >= $2 AND v.hora_venta <= $3
		 GROUP BY 1 ORDER BY 2 DESC`, baseArgs...); err == nil {
		defer rows.Close()
		for rows.Next() {
			var nombre string
			var monto float64
			rows.Scan(&nombre, &monto)
			pct := 0.0
			if res.Total+res.Propinas > 0 {
				pct = round1(monto / (res.Total + res.Propinas) * 100)
			}
			res.PorServicio = append(res.PorServicio, svcIngrSt{Nombre: nombre, Monto: monto, Porcentaje: pct})
		}
	}

	if rows, err := h.db.Query(r.Context(),
		`SELECT metodo_pago, COALESCE(SUM(monto),0), COUNT(*)
		 FROM ventas WHERE `+rangeWhere+` GROUP BY 1 ORDER BY 2 DESC`, baseArgs...); err == nil {
		defer rows.Close()
		for rows.Next() {
			var metodo string
			var total float64
			var cnt int
			rows.Scan(&metodo, &total, &cnt)
			res.PorMetodoPago = append(res.PorMetodoPago, metodoPagoSt{Metodo: metodo, Total: total, Cantidad: cnt})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// ── GET /api/estadisticas/trabajador/:id ─────────────────────────

type estTrabSvc struct {
	Nombre     string  `json:"nombre"`
	Cantidad   int     `json:"cantidad"`
	Porcentaje float64 `json:"porcentaje"`
	Color      string  `json:"color"`
}

type estTrabajadorResp struct {
	TotalCitas    int          `json:"total_citas"`
	TotalIngresos float64      `json:"total_ingresos"`
	Servicios     []estTrabSvc `json:"servicios"`
}

func (h *estadisticasHandler) trabajador(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	tid, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}

	var exists bool
	h.db.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM trabajadores WHERE id=$1 AND negocio_id=$2)`,
		tid, nid).Scan(&exists)
	if !exists {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	res := estTrabajadorResp{Servicios: []estTrabSvc{}}

	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM citas
		 WHERE trabajador_id=$1 AND negocio_id=$2 AND status != 'cancelada'`,
		tid, nid).Scan(&res.TotalCitas)

	h.db.QueryRow(r.Context(),
		`SELECT COALESCE(SUM(
			monto + CASE WHEN propina_tipo='porcentaje' THEN monto*propina/100.0 ELSE propina END
		), 0) FROM ventas WHERE trabajador_id=$1 AND negocio_id=$2`,
		tid, nid).Scan(&res.TotalIngresos)

	if rows, err := h.db.Query(r.Context(),
		`SELECT COALESCE(s.nombre,'Sin servicio'), COUNT(*), COALESCE(s.color,'#000080')
		 FROM citas c LEFT JOIN servicios s ON s.id = c.servicio_id
		 WHERE c.trabajador_id=$1 AND c.negocio_id=$2 AND c.status != 'cancelada'
		 GROUP BY 1, 3 ORDER BY 2 DESC`, tid, nid); err == nil {
		defer rows.Close()
		for rows.Next() {
			var nombre, color string
			var cnt int
			rows.Scan(&nombre, &cnt, &color)
			pct := 0.0
			if res.TotalCitas > 0 {
				pct = round1(float64(cnt) / float64(res.TotalCitas) * 100)
			}
			res.Servicios = append(res.Servicios, estTrabSvc{nombre, cnt, pct, color})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// ── GET /api/estadisticas/calificaciones ─────────────────────────

type resenaItem struct {
	ClienteNombre string  `json:"cliente_nombre"`
	Calificacion  int     `json:"calificacion"`
	Comentario    *string `json:"comentario,omitempty"`
	Fecha         string  `json:"fecha"`
	Servicio      string  `json:"servicio"`
}

type estCalificaciones struct {
	Promedio   float64      `json:"promedio"`
	Total      int          `json:"total"`
	Excelentes int          `json:"excelentes"`
	Regulares  int          `json:"regulares"`
	Malas      int          `json:"malas"`
	Resenas    []resenaItem `json:"resenas"`
}

func (h *estadisticasHandler) calificaciones(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}
	inicio, fin := parseFechasEst(r)

	res := estCalificaciones{Resenas: []resenaItem{}}

	h.db.QueryRow(r.Context(),
		`SELECT COALESCE(AVG(calificacion),0),
		        COUNT(*),
		        SUM(CASE WHEN calificacion >= 4 THEN 1 ELSE 0 END),
		        SUM(CASE WHEN calificacion = 3  THEN 1 ELSE 0 END),
		        SUM(CASE WHEN calificacion <= 2 THEN 1 ELSE 0 END)
		 FROM citas
		 WHERE negocio_id = $1
		   AND calificacion IS NOT NULL
		   AND calificacion_fecha_respuesta >= $2
		   AND calificacion_fecha_respuesta <= $3`,
		nid, tsStart(inicio), tsEnd(fin),
	).Scan(&res.Promedio, &res.Total, &res.Excelentes, &res.Regulares, &res.Malas)

	if rows, err := h.db.Query(r.Context(),
		`SELECT COALESCE(cl.nombre || ' ' || COALESCE(cl.apellido,''), 'Cliente'),
		        c.calificacion,
		        c.comentario,
		        TO_CHAR(c.calificacion_fecha_respuesta AT TIME ZONE 'America/Mexico_City', 'YYYY-MM-DD'),
		        COALESCE(s.nombre, 'Sin servicio')
		 FROM citas c
		 LEFT JOIN clientes cl ON cl.id = c.cliente_id
		 LEFT JOIN servicios s  ON s.id  = c.servicio_id
		 WHERE c.negocio_id = $1
		   AND c.calificacion IS NOT NULL
		   AND c.calificacion_fecha_respuesta >= $2
		   AND c.calificacion_fecha_respuesta <= $3
		 ORDER BY c.calificacion_fecha_respuesta DESC
		 LIMIT 50`,
		nid, tsStart(inicio), tsEnd(fin)); err == nil {
		defer rows.Close()
		for rows.Next() {
			var item resenaItem
			rows.Scan(&item.ClienteNombre, &item.Calificacion, &item.Comentario, &item.Fecha, &item.Servicio)
			res.Resenas = append(res.Resenas, item)
		}
	}

	res.Promedio = round1(res.Promedio)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
