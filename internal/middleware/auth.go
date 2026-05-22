package middleware

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"

	"firebase.google.com/go/v4/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

type contextKey string

const (
	ContextUID       contextKey = "uid"
	ContextEmail     contextKey = "email"
	ContextNegocioID contextKey = "negocio_id"
)

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}

// Auth accepts either:
//   - Header "Authorization: Bearer <firebase_token>"
//   - Header "X-API-Key: dzai_<key>"
func Auth(authClient *auth.Client, db *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			// ── API Key ───────────────────────────────────────────────
			if apiKey := r.Header.Get("X-API-Key"); apiKey != "" {
				hash := sha256Hex(apiKey)

				var uid string
				var negocioID int
				err := db.QueryRow(r.Context(),
					`SELECT n.uid, k.negocio_id
					 FROM api_keys k
					 JOIN negocios n ON n.id = k.negocio_id
					 WHERE k.key_hash = $1 AND k.activo = true`,
					hash).Scan(&uid, &negocioID)
				if err != nil {
					http.Error(w, `{"error":"invalid API key"}`, http.StatusUnauthorized)
					return
				}

				// Update last_used_at without blocking the request
				go db.Exec(context.Background(),
					`UPDATE api_keys SET last_used_at = NOW() WHERE key_hash = $1`, hash)

				ctx := context.WithValue(r.Context(), ContextUID, uid)
				ctx = context.WithValue(ctx, ContextNegocioID, negocioID)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// ── Firebase Bearer token ─────────────────────────────────
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				http.Error(w, `{"error":"missing or invalid Authorization header"}`, http.StatusUnauthorized)
				return
			}

			idToken := strings.TrimPrefix(header, "Bearer ")
			token, err := authClient.VerifyIDToken(r.Context(), idToken)
			if err != nil {
				http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
				return
			}

			email, _ := token.Claims["email"].(string)
			ctx := context.WithValue(r.Context(), ContextUID, token.UID)
			ctx = context.WithValue(ctx, ContextEmail, email)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func UIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ContextUID).(string)
	return v
}

func EmailFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ContextEmail).(string)
	return v
}

func NegocioIDFromContext(ctx context.Context) int {
	v, _ := ctx.Value(ContextNegocioID).(int)
	return v
}
