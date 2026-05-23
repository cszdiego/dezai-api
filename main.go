package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"

	"github.com/dezai/dezai-api/internal/config"
	mw "github.com/dezai/dezai-api/internal/middleware"
	"github.com/dezai/dezai-api/internal/routes"
)

func main() {
	_ = godotenv.Load()

	ctx := context.Background()

	fb, err := config.InitFirebase()
	if err != nil {
		log.Fatalf("firebase init: %v", err)
	}

	db, err := config.InitDatabase(ctx)
	if err != nil {
		log.Fatalf("database init: %v", err)
	}
	defer db.Close()

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware())

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	authMiddleware := mw.Auth(fb.Auth, db)
	routes.RegisterUsers(r, db, fb.Auth, authMiddleware)
	routes.RegisterNegocios(r, db, authMiddleware)
	routes.RegisterServicios(r, db, authMiddleware)
	routes.RegisterClientes(r, db, authMiddleware)
	routes.RegisterCitas(r, db, authMiddleware)
	routes.RegisterBloqueos(r, db, authMiddleware)
	routes.RegisterVentas(r, db, authMiddleware)
	routes.RegisterNotificaciones(r, db, authMiddleware)
	routes.RegisterEstadisticas(r, db, authMiddleware)
	routes.RegisterConfiguracionNotificaciones(r, db, authMiddleware)
	routes.RegisterDispositivos(r, db, authMiddleware)
	routes.RegisterTrabajadores(r, db, authMiddleware)
	routes.RegisterConfiguracionAgenda(r, db, authMiddleware)
	routes.RegisterConfiguracionRecordatorios(r, db, authMiddleware)
	routes.RegisterConfiguracionCalificaciones(r, db, authMiddleware)
	routes.RegisterConfiguracionFidelidad(r, db, authMiddleware)
	routes.RegisterAPIKeys(r, db, authMiddleware)
	routes.RegisterImagenes(r, db, authMiddleware)
	routes.RegisterReportes(r, db, authMiddleware)
	routes.RegisterGaleria(r, db, authMiddleware)
	routes.RegisterPromociones(r, db, authMiddleware)
	routes.RegisterFAQs(r, db, authMiddleware)
	routes.RegisterLinks(r, db, authMiddleware)
	routes.RegisterAgent(r, db)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		for range ticker.C {
			routes.EnviarRecordatoriosPendientes(db)
		}
	}()

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		for range ticker.C {
			routes.AutocompletarCitas(db)
		}
	}()

	log.Printf("dezai-api listening on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func corsMiddleware() func(http.Handler) http.Handler {
	rawOrigins := os.Getenv("ALLOWED_ORIGINS")
	allowed := map[string]bool{}
	for _, o := range strings.Split(rawOrigins, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			allowed[o] = true
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				w.Header().Set("Vary", "Origin")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
