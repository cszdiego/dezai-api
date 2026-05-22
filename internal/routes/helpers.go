package routes

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	mw "github.com/dezai/dezai-api/internal/middleware"
)

// negocioIDForUID returns the negocio id for the authenticated request.
// When an API key was used, negocio_id is pre-loaded in context and no DB
// query is needed. For Firebase token auth it looks up by uid.
func negocioIDForUID(ctx context.Context, db *pgxpool.Pool, uid string, w http.ResponseWriter) (int, bool) {
	if nid := mw.NegocioIDFromContext(ctx); nid != 0 {
		return nid, true
	}
	var id int
	if err := db.QueryRow(ctx, `SELECT id FROM negocios WHERE uid = $1`, uid).Scan(&id); err != nil {
		http.Error(w, `{"error":"negocio not found"}`, http.StatusNotFound)
		return 0, false
	}
	return id, true
}
