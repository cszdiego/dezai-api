package routes

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type agentHandler struct{ db *pgxpool.Pool }

func RegisterAgent(r chi.Router, db *pgxpool.Pool) {
	h := &agentHandler{db: db}
	r.Get("/api/agent/info/{negocio_id}", h.info)
}

type negocioPublico struct {
	ID            int             `json:"id"`
	NombreNegocio string          `json:"nombre_negocio"`
	Nombre        string          `json:"nombre"`
	Apellido      *string         `json:"apellido,omitempty"`
	Telefono      string          `json:"telefono"`
	Direccion     *string         `json:"direccion,omitempty"`
	Horarios      json.RawMessage `json:"horarios,omitempty"`
	Reglas        *string         `json:"reglas,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

type agentImagen struct {
	ID                   int     `json:"id"`
	URL                  string  `json:"url"`
	Descripcion          *string `json:"descripcion,omitempty"`
	DescripcionIntencion *string `json:"descripcion_intencion,omitempty"`
}

type agentConfiguracion struct {
	NombreAgente             string `json:"nombre_agente"`
	Tono                     string `json:"tono"`
	MensajeBienvenida        string `json:"mensaje_bienvenida"`
	MensajeFueraHorario      string `json:"mensaje_fuera_horario"`
	InstruccionesAdicionales string `json:"instrucciones_adicionales"`
}

type agentInfoResponse struct {
	Negocio              negocioPublico     `json:"negocio"`
	Servicios            []servicio         `json:"servicios"`
	Promociones          []promocion        `json:"promociones"`
	FAQs                 []faq              `json:"faqs"`
	Links                []link             `json:"links"`
	AgenteImagenes       []agentImagen      `json:"agente_imagenes"`
	AgenteConfiguracion  agentConfiguracion `json:"agente_configuracion"`
}

func (h *agentHandler) info(w http.ResponseWriter, r *http.Request) {
	nid, err := strconv.Atoi(chi.URLParam(r, "negocio_id"))
	if err != nil {
		http.Error(w, `{"error":"invalid negocio_id"}`, http.StatusBadRequest)
		return
	}

	// Negocio
	var neg negocioPublico
	var horariosBuf []byte
	if err := h.db.QueryRow(r.Context(),
		`SELECT id, nombre_negocio, nombre, apellido, telefono, direccion, horarios, reglas, created_at
		 FROM negocios WHERE id = $1`, nid,
	).Scan(&neg.ID, &neg.NombreNegocio, &neg.Nombre, &neg.Apellido,
		&neg.Telefono, &neg.Direccion, &horariosBuf, &neg.Reglas, &neg.CreatedAt); err != nil {
		http.Error(w, `{"error":"negocio not found"}`, http.StatusNotFound)
		return
	}
	if len(horariosBuf) > 0 {
		neg.Horarios = json.RawMessage(horariosBuf)
	}

	// Servicios activos
	servicios := []servicio{}
	servRows, err := h.db.Query(r.Context(),
		`SELECT `+servicioColumns+` FROM servicios WHERE negocio_id = $1 AND activo = true ORDER BY nombre`, nid)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	for servRows.Next() {
		s, err := scanServicio(servRows)
		if err != nil {
			servRows.Close()
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		servicios = append(servicios, s)
	}
	servRows.Close()

	// Promociones activas y vigentes
	promociones := []promocion{}
	promRows, err := h.db.Query(r.Context(),
		`SELECT `+promocionColumns+` FROM promociones
		 WHERE negocio_id = $1 AND activo = true
		   AND (fecha_fin IS NULL OR fecha_fin >= CURRENT_DATE)
		 ORDER BY created_at DESC`, nid)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	for promRows.Next() {
		p, err := scanPromocion(promRows)
		if err != nil {
			promRows.Close()
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		promociones = append(promociones, p)
	}
	promRows.Close()

	// FAQs activas
	faqs := []faq{}
	faqRows, err := h.db.Query(r.Context(),
		`SELECT `+faqColumns+` FROM faqs WHERE negocio_id = $1 AND activo = true ORDER BY created_at ASC`, nid)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	for faqRows.Next() {
		f, err := scanFAQ(faqRows)
		if err != nil {
			faqRows.Close()
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		faqs = append(faqs, f)
	}
	faqRows.Close()

	// Links activos
	links := []link{}
	linkRows, err := h.db.Query(r.Context(),
		`SELECT `+linkColumns+` FROM links WHERE negocio_id = $1 AND activo = true ORDER BY created_at ASC`, nid)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	for linkRows.Next() {
		l, err := scanLink(linkRows)
		if err != nil {
			linkRows.Close()
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		links = append(links, l)
	}
	linkRows.Close()

	// Imágenes del agente (solo activas)
	imagenes := []agentImagen{}
	imgRows, err := h.db.Query(r.Context(),
		`SELECT id, url, descripcion, descripcion_intencion
		 FROM agente_imagenes WHERE negocio_id = $1 AND activo = true ORDER BY created_at DESC`, nid)
	if err != nil {
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	for imgRows.Next() {
		var img agentImagen
		if err := imgRows.Scan(&img.ID, &img.URL, &img.Descripcion, &img.DescripcionIntencion); err != nil {
			imgRows.Close()
			http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
			return
		}
		imagenes = append(imagenes, img)
	}
	imgRows.Close()

	// Configuración del agente (defaults si no existe)
	var cfg agentConfiguracion
	if err := h.db.QueryRow(r.Context(),
		`SELECT nombre_agente, tono,
		        COALESCE(mensaje_bienvenida,''),
		        COALESCE(mensaje_fuera_horario,''),
		        COALESCE(instrucciones_adicionales,'')
		 FROM agente_configuracion WHERE negocio_id = $1`, nid,
	).Scan(&cfg.NombreAgente, &cfg.Tono, &cfg.MensajeBienvenida, &cfg.MensajeFueraHorario, &cfg.InstruccionesAdicionales); err != nil {
		cfg = agentConfiguracion{
			NombreAgente: "Asistente virtual",
			Tono:         "amigable",
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(agentInfoResponse{
		Negocio:             neg,
		Servicios:           servicios,
		Promociones:         promociones,
		FAQs:                faqs,
		Links:               links,
		AgenteImagenes:      imagenes,
		AgenteConfiguracion: cfg,
	})
}
