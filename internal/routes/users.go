package routes

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"time"

	firebaseauth "firebase.google.com/go/v4/auth"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	resend "github.com/resend/resend-go/v3"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

type usersHandler struct {
	db         *pgxpool.Pool
	authClient *firebaseauth.Client
}

func RegisterUsers(r chi.Router, db *pgxpool.Pool, authClient *firebaseauth.Client, authMiddleware func(http.Handler) http.Handler) {
	h := &usersHandler{db: db, authClient: authClient}

	r.Route("/api/users", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/me", h.me)
		r.Post("/register", h.register)
		r.Post("/invite", h.invite)
		r.Post("/send-delete-code", h.sendDeleteCode)
		r.Post("/verify-delete-code", h.verifyDeleteCode)
		r.Delete("/me", h.deleteMe)
	})

	r.Route("/api/admin/usuarios", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Put("/{uid}/plan", h.updatePlan)
	})
}

// ── generateOTP ───────────────────────────────────────────────────

func generateOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// ── deleteCodeEmailHTML ───────────────────────────────────────────

func deleteCodeEmailHTML(code string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="es">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#F2F2F7;font-family:Arial,sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" style="padding:40px 16px;">
    <tr><td align="center">
      <table width="480" cellpadding="0" cellspacing="0" style="background:#fff;border-radius:16px;overflow:hidden;max-width:480px;width:100%%;">
        <tr>
          <td style="background:#000080;padding:28px 32px;text-align:center;">
            <span style="color:#fff;font-size:26px;font-weight:bold;letter-spacing:2px;">DEZAI</span>
          </td>
        </tr>
        <tr>
          <td style="padding:32px;">
            <h2 style="margin:0 0 12px;color:#0f172a;font-size:20px;">Eliminación de cuenta</h2>
            <p style="margin:0 0 24px;color:#64748b;font-size:15px;line-height:1.6;">
              Recibimos una solicitud para eliminar tu cuenta. Usa el siguiente código para continuar.
              Expira en <strong>15 minutos</strong>.
            </p>
            <div style="background:#f8fafc;border:2px solid #000080;border-radius:12px;padding:28px;text-align:center;margin:0 0 24px;">
              <span style="font-size:40px;font-weight:bold;color:#000080;letter-spacing:10px;">%s</span>
            </div>
            <p style="margin:0;color:#94a3b8;font-size:13px;line-height:1.5;">
              Si no solicitaste eliminar tu cuenta, ignora este correo. Tu cuenta permanecerá activa.
            </p>
          </td>
        </tr>
        <tr>
          <td style="background:#f8fafc;padding:16px 32px;text-align:center;">
            <span style="color:#cbd5e1;font-size:12px;">© 2025 DEZAI · Todos los derechos reservados</span>
          </td>
        </tr>
      </table>
    </td></tr>
  </table>
</body>
</html>`, code)
}

// ── POST /api/users/register ──────────────────────────────────────

type registerRequest struct {
	Nombre   string `json:"nombre"`
	Apellido string `json:"apellido"`
	Email    string `json:"email"`
}

func (h *usersHandler) register(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())

	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	// Idempotent: if uid already exists, return success
	_, err := h.db.Exec(r.Context(),
		`INSERT INTO usuarios (uid, email, role, plan)
		 VALUES ($1, $2, 'client', 'prueba')
		 ON CONFLICT (uid) DO NOTHING`,
		uid, req.Email)
	if err != nil {
		http.Error(w, `{"error":"failed to register user"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true})
}

// ── Tipos y handlers existentes ───────────────────────────────────

type usuario struct {
	UID       string    `json:"uid"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Plan      string    `json:"plan"`
	NegocioID *int      `json:"negocio_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *usersHandler) me(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())

	var u usuario
	err := h.db.QueryRow(r.Context(),
		`SELECT uid, email, role, plan, negocio_id, created_at FROM usuarios WHERE uid = $1`, uid,
	).Scan(&u.UID, &u.Email, &u.Role, &u.Plan, &u.NegocioID, &u.CreatedAt)
	if err != nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(u)
}

type inviteRequest struct {
	Email     string `json:"email"`
	NegocioID *int   `json:"negocio_id"`
}

func (h *usersHandler) invite(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())

	var role string
	if err := h.db.QueryRow(r.Context(),
		`SELECT role FROM usuarios WHERE uid = $1`, uid).Scan(&role); err != nil || role != "admin" {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}

	var req inviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	userRecord, err := h.authClient.CreateUser(r.Context(), (&firebaseauth.UserToCreate{}).Email(req.Email))
	if err != nil {
		http.Error(w, `{"error":"failed to create firebase user"}`, http.StatusInternalServerError)
		return
	}

	link, err := h.authClient.PasswordResetLink(r.Context(), req.Email)
	if err != nil {
		http.Error(w, `{"error":"failed to generate activation link"}`, http.StatusInternalServerError)
		return
	}

	if _, err := h.db.Exec(r.Context(),
		`INSERT INTO usuarios (uid, email, role, negocio_id) VALUES ($1, $2, 'client', $3)
		 ON CONFLICT (uid) DO NOTHING`,
		userRecord.UID, req.Email, req.NegocioID); err != nil {
		http.Error(w, `{"error":"failed to save user"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true, "link": link})
}

// ── POST /api/users/send-delete-code ─────────────────────────────

func (h *usersHandler) sendDeleteCode(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())

	var userEmail string
	if err := h.db.QueryRow(r.Context(),
		`SELECT email FROM usuarios WHERE uid = $1`, uid).Scan(&userEmail); err != nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}

	code, err := generateOTP()
	if err != nil {
		http.Error(w, `{"error":"failed to generate code"}`, http.StatusInternalServerError)
		return
	}

	expiresAt := time.Now().Add(15 * time.Minute)
	if _, err := h.db.Exec(r.Context(),
		`INSERT INTO delete_codes (uid, code, expires_at) VALUES ($1, $2, $3)
		 ON CONFLICT (uid) DO UPDATE SET code = EXCLUDED.code, expires_at = EXCLUDED.expires_at`,
		uid, code, expiresAt); err != nil {
		http.Error(w, `{"error":"failed to save code"}`, http.StatusInternalServerError)
		return
	}

	apiKey := os.Getenv("RESEND_API_KEY")
	client := resend.NewClient(apiKey)
	params := &resend.SendEmailRequest{
		From:    "DEZAI <noreply@dezai.mx>",
		To:      []string{userEmail},
		Subject: "Código para eliminar tu cuenta",
		Html:    deleteCodeEmailHTML(code),
	}
	if _, err := client.Emails.Send(params); err != nil {
		http.Error(w, `{"error":"failed to send email"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true})
}

// ── POST /api/users/verify-delete-code ───────────────────────────

type verifyDeleteCodeRequest struct {
	Code string `json:"code"`
}

func (h *usersHandler) verifyDeleteCode(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())

	var req verifyDeleteCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	var count int
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM delete_codes WHERE uid=$1 AND code=$2 AND expires_at > NOW() AT TIME ZONE 'UTC'`,
		uid, req.Code).Scan(&count)

	w.Header().Set("Content-Type", "application/json")
	if count > 0 {
		json.NewEncoder(w).Encode(map[string]any{"valid": true})
	} else {
		json.NewEncoder(w).Encode(map[string]any{
			"valid": false,
			"error": "Código incorrecto o expirado",
		})
	}
}

// ── DELETE /api/users/me ─────────────────────────────────────────

func (h *usersHandler) deleteMe(w http.ResponseWriter, r *http.Request) {
	uid := mw.UIDFromContext(r.Context())

	// 1. Eliminar código OTP pendiente
	h.db.Exec(r.Context(), `DELETE FROM delete_codes WHERE uid=$1`, uid)

	// 2. Eliminar negocio (CASCADE elimina servicios, citas, ventas, etc.)
	if _, err := h.db.Exec(r.Context(),
		`DELETE FROM negocios WHERE uid=$1`, uid); err != nil {
		http.Error(w, `{"error":"failed to delete negocio"}`, http.StatusInternalServerError)
		return
	}

	// 3. Eliminar de tabla usuarios
	if _, err := h.db.Exec(r.Context(),
		`DELETE FROM usuarios WHERE uid=$1`, uid); err != nil {
		http.Error(w, `{"error":"failed to delete user record"}`, http.StatusInternalServerError)
		return
	}

	// 4. Eliminar de Firebase Auth
	if err := h.authClient.DeleteUser(r.Context(), uid); err != nil {
		http.Error(w, `{"error":"failed to delete firebase user"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true})
}

// ── PUT /api/admin/usuarios/:uid/plan ────────────────────────────

type updatePlanRequest struct {
	Plan string `json:"plan"`
}

func (h *usersHandler) updatePlan(w http.ResponseWriter, r *http.Request) {
	requesterUID := mw.UIDFromContext(r.Context())

	var role string
	if err := h.db.QueryRow(r.Context(),
		`SELECT role FROM usuarios WHERE uid = $1`, requesterUID).Scan(&role); err != nil || role != "admin" {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}

	targetUID := chi.URLParam(r, "uid")

	var req updatePlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	switch req.Plan {
	case "prueba", "basico", "pro":
	default:
		http.Error(w, `{"error":"plan must be 'prueba', 'basico' or 'pro'"}`, http.StatusBadRequest)
		return
	}

	tag, err := h.db.Exec(r.Context(),
		`UPDATE usuarios SET plan=$2 WHERE uid=$1`, targetUID, req.Plan)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"uid": targetUID, "plan": req.Plan})
}
