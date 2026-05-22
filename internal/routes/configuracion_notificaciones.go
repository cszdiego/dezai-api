package routes

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type configNotifHandler struct{ db *pgxpool.Pool }

func RegisterConfiguracionNotificaciones(r chi.Router, db *pgxpool.Pool, authMiddleware func(http.Handler) http.Handler) {
	h := &configNotifHandler{db: db}
	r.Route("/api/configuracion-notificaciones", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/", h.get)
		r.Put("/", h.put)
	})
}

type configNotifRow struct {
	NegocioID         int  `json:"negocio_id"`
	RecordatorioCita  bool `json:"recordatorio_cita"`
	HorasAntes        int  `json:"horas_antes"`
	NotifCitaNueva    bool `json:"notif_cita_nueva"`
	NotifCancelacion  bool `json:"notif_cancelacion"`
	NotifConfirmacion bool `json:"notif_confirmacion"`
	NotifCalificacion bool `json:"notif_calificacion"`
}

const configNotifCols = `negocio_id, recordatorio_cita, horas_antes,
	notif_cita_nueva, notif_cancelacion, notif_confirmacion, notif_calificacion`

func scanConfigNotif(row interface{ Scan(...any) error }) (configNotifRow, error) {
	var c configNotifRow
	return c, row.Scan(&c.NegocioID, &c.RecordatorioCita, &c.HorasAntes,
		&c.NotifCitaNueva, &c.NotifCancelacion, &c.NotifConfirmacion, &c.NotifCalificacion)
}

func (h *configNotifHandler) get(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	cfg, err := scanConfigNotif(h.db.QueryRow(r.Context(),
		`SELECT `+configNotifCols+` FROM configuracion_notificaciones WHERE negocio_id=$1`, nid))
	if err != nil {
		// Row not found — return defaults
		cfg = configNotifRow{
			NegocioID:         nid,
			RecordatorioCita:  true,
			HorasAntes:        2,
			NotifCitaNueva:    true,
			NotifCancelacion:  true,
			NotifConfirmacion: true,
			NotifCalificacion: true,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

type putConfigNotifRequest struct {
	RecordatorioCita  bool `json:"recordatorio_cita"`
	HorasAntes        int  `json:"horas_antes"`
	NotifCitaNueva    bool `json:"notif_cita_nueva"`
	NotifCancelacion  bool `json:"notif_cancelacion"`
	NotifConfirmacion bool `json:"notif_confirmacion"`
	NotifCalificacion bool `json:"notif_calificacion"`
}

func (h *configNotifHandler) put(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())
	nid, ok := negocioIDForUID(r.Context(), h.db, uid, w)
	if !ok {
		return
	}

	var req putConfigNotifRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}

	cfg, err := scanConfigNotif(h.db.QueryRow(r.Context(),
		`INSERT INTO configuracion_notificaciones (`+configNotifCols+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (negocio_id) DO UPDATE SET
		   recordatorio_cita  = EXCLUDED.recordatorio_cita,
		   horas_antes        = EXCLUDED.horas_antes,
		   notif_cita_nueva   = EXCLUDED.notif_cita_nueva,
		   notif_cancelacion  = EXCLUDED.notif_cancelacion,
		   notif_confirmacion = EXCLUDED.notif_confirmacion,
		   notif_calificacion = EXCLUDED.notif_calificacion
		 RETURNING `+configNotifCols,
		nid, req.RecordatorioCita, req.HorasAntes,
		req.NotifCitaNueva, req.NotifCancelacion, req.NotifConfirmacion, req.NotifCalificacion))
	if err != nil {
		http.Error(w, `{"error":"failed to save"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}
