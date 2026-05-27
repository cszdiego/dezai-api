package config

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func InitDatabase(ctx context.Context) (*pgxpool.Pool, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL env var is required")
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.New: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("db ping: %w", err)
	}

	if err := migrate(ctx, pool); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return pool, nil
}

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	stmts := []string{
		// ── usuarios ──────────────────────────────────────────────────
		`CREATE TABLE IF NOT EXISTS usuarios (
			uid        VARCHAR(128) PRIMARY KEY,
			email      VARCHAR(255) UNIQUE NOT NULL,
			role       VARCHAR(20)  NOT NULL DEFAULT 'client',
			plan       VARCHAR(20)  NOT NULL DEFAULT 'basico',
			negocio_id INTEGER,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
		// columnas nuevas en tabla existente
		`ALTER TABLE usuarios ADD COLUMN IF NOT EXISTS plan VARCHAR(20) NOT NULL DEFAULT 'basico'`,
		`ALTER TABLE usuarios ALTER COLUMN plan SET DEFAULT 'prueba'`,

		// ── negocios ──────────────────────────────────────────────────
		`CREATE TABLE IF NOT EXISTS negocios (
			id               SERIAL PRIMARY KEY,
			uid              VARCHAR(128) UNIQUE NOT NULL REFERENCES usuarios(uid),
			nombre           VARCHAR(100) NOT NULL,
			apellido         VARCHAR(100),
			nombre_negocio   VARCHAR(100) NOT NULL,
			telefono         VARCHAR(20)  NOT NULL,
			fecha_nacimiento DATE,
			direccion        TEXT,
			horarios         JSONB,
			reglas           TEXT,
			created_at       TIMESTAMP DEFAULT NOW()
		)`,
		`ALTER TABLE negocios ADD COLUMN IF NOT EXISTS apellido       VARCHAR(100)`,
		`ALTER TABLE negocios ADD COLUMN IF NOT EXISTS direccion      TEXT`,
		`ALTER TABLE negocios ADD COLUMN IF NOT EXISTS horarios       JSONB`,
		`ALTER TABLE negocios ADD COLUMN IF NOT EXISTS reglas         TEXT`,

		// ── servicios ─────────────────────────────────────────────────
		`CREATE TABLE IF NOT EXISTS servicios (
			id               SERIAL PRIMARY KEY,
			negocio_id       INTEGER NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			nombre           VARCHAR(100) NOT NULL,
			descripcion      TEXT,
			duracion_minutos INTEGER NOT NULL DEFAULT 60,
			precio           DECIMAL(10,2) NOT NULL DEFAULT 0,
			color            VARCHAR(7)    NOT NULL DEFAULT '#000080',
			activo           BOOLEAN DEFAULT true,
			created_at       TIMESTAMP DEFAULT NOW()
		)`,

		// ── clientes ──────────────────────────────────────────────────
		`CREATE TABLE IF NOT EXISTS clientes (
			id               SERIAL PRIMARY KEY,
			negocio_id       INTEGER NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			nombre           VARCHAR(100) NOT NULL,
			apellido         VARCHAR(100),
			telefono         VARCHAR(20)  NOT NULL,
			fecha_nacimiento DATE,
			notas_internas   TEXT,
			created_at       TIMESTAMP DEFAULT NOW()
		)`,

		// ── citas ─────────────────────────────────────────────────────
		`CREATE TABLE IF NOT EXISTS citas (
			id               SERIAL PRIMARY KEY,
			negocio_id       INTEGER NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			cliente_id       INTEGER REFERENCES clientes(id) ON DELETE SET NULL,
			servicio_id      INTEGER REFERENCES servicios(id) ON DELETE SET NULL,
			fecha_hora       TIMESTAMP WITH TIME ZONE NOT NULL,
			duracion_minutos INTEGER NOT NULL DEFAULT 60,
			precio           DECIMAL(10,2),
			status           VARCHAR(20) NOT NULL DEFAULT 'agendada'
			                 CHECK (status IN ('agendada','confirmada','reagendada','cancelada','completada')),
			notas            TEXT,
			created_at       TIMESTAMP DEFAULT NOW(),
			updated_at       TIMESTAMP DEFAULT NOW()
		)`,

		// ── bloqueos ──────────────────────────────────────────────────
		`CREATE TABLE IF NOT EXISTS bloqueos (
			id           SERIAL PRIMARY KEY,
			negocio_id   INTEGER NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			fecha_inicio TIMESTAMP WITH TIME ZONE NOT NULL,
			fecha_fin    TIMESTAMP WITH TIME ZONE NOT NULL,
			motivo       TEXT,
			created_at   TIMESTAMP DEFAULT NOW()
		)`,

		// ── ventas ────────────────────────────────────────────────────
		`CREATE TABLE IF NOT EXISTS ventas (
			id             SERIAL PRIMARY KEY,
			negocio_id     INTEGER NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			cita_id        INTEGER REFERENCES citas(id) ON DELETE SET NULL,
			cliente_id     INTEGER REFERENCES clientes(id) ON DELETE SET NULL,
			servicio_id    INTEGER REFERENCES servicios(id) ON DELETE SET NULL,
			monto          DECIMAL(10,2) NOT NULL,
			propina        DECIMAL(10,2) DEFAULT 0,
			propina_tipo   VARCHAR(10) DEFAULT 'monto' CHECK (propina_tipo IN ('monto','porcentaje')),
			metodo_pago    VARCHAR(20) DEFAULT 'efectivo' CHECK (metodo_pago IN ('efectivo','tarjeta','transferencia')),
			descuento      DECIMAL(10,2) DEFAULT 0,
			descuento_tipo VARCHAR(10) DEFAULT 'monto' CHECK (descuento_tipo IN ('monto','porcentaje')),
			hora_venta     TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			created_at     TIMESTAMP DEFAULT NOW()
		)`,
		`ALTER TABLE ventas ADD COLUMN IF NOT EXISTS metodo_pago    VARCHAR(20) DEFAULT 'efectivo' CHECK (metodo_pago    IN ('efectivo','tarjeta','transferencia'))`,
		`ALTER TABLE ventas ADD COLUMN IF NOT EXISTS descuento      DECIMAL(10,2) DEFAULT 0`,
		`ALTER TABLE ventas ADD COLUMN IF NOT EXISTS descuento_tipo VARCHAR(10) DEFAULT 'monto'    CHECK (descuento_tipo IN ('monto','porcentaje'))`,
		`ALTER TABLE ventas ADD COLUMN IF NOT EXISTS propina_tipo   VARCHAR(10) DEFAULT 'monto'    CHECK (propina_tipo   IN ('monto','porcentaje'))`,

		// ── notificaciones ────────────────────────────────────────────
		`CREATE TABLE IF NOT EXISTS notificaciones (
			id              SERIAL PRIMARY KEY,
			negocio_id      INTEGER NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			titulo          VARCHAR(100) NOT NULL,
			mensaje         TEXT NOT NULL,
			tipo            VARCHAR(30) NOT NULL,
			referencia_id   INTEGER,
			referencia_tipo VARCHAR(20),
			leida           BOOLEAN DEFAULT false,
			created_at      TIMESTAMP DEFAULT NOW()
		)`,

		// ── dispositivos (push tokens) ────────────────────────────────
		`CREATE TABLE IF NOT EXISTS dispositivos (
			id         SERIAL PRIMARY KEY,
			negocio_id INTEGER NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			push_token TEXT    NOT NULL,
			updated_at TIMESTAMP DEFAULT NOW(),
			UNIQUE(negocio_id, push_token)
		)`,

		// columna para evitar duplicar recordatorios
		`ALTER TABLE citas ADD COLUMN IF NOT EXISTS recordatorio_enviado BOOLEAN DEFAULT false`,

		// ── trabajadores ──────────────────────────────────────────────
		`CREATE TABLE IF NOT EXISTS trabajadores (
			id         SERIAL PRIMARY KEY,
			negocio_id INTEGER NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			nombre     VARCHAR(100) NOT NULL,
			apellido   VARCHAR(100),
			telefono   VARCHAR(20),
			activo     BOOLEAN DEFAULT true,
			horario    JSONB,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
		`ALTER TABLE citas   ADD COLUMN IF NOT EXISTS trabajador_id                 INTEGER REFERENCES trabajadores(id) ON DELETE SET NULL`,
		`ALTER TABLE citas   ADD COLUMN IF NOT EXISTS calificacion                 INTEGER`,
		`ALTER TABLE citas   ADD COLUMN IF NOT EXISTS calificacion_fecha_respuesta TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE citas   ADD COLUMN IF NOT EXISTS comentario                   TEXT`,
		`ALTER TABLE ventas  ADD COLUMN IF NOT EXISTS trabajador_id                INTEGER REFERENCES trabajadores(id) ON DELETE SET NULL`,

		// ── configuracion_agenda ─────────────────────────────────────
		`CREATE TABLE IF NOT EXISTS configuracion_agenda (
			id                      SERIAL PRIMARY KEY,
			negocio_id              INTEGER UNIQUE NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			intervalo_minutos       INTEGER NOT NULL DEFAULT 30 CHECK (intervalo_minutos IN (15, 30, 60)),
			dia_inicio_semana       INTEGER NOT NULL DEFAULT 1   CHECK (dia_inicio_semana IN (0, 1)),
			confirmacion_automatica BOOLEAN DEFAULT false,
			max_citas_por_hora      INTEGER NOT NULL DEFAULT 1   CHECK (max_citas_por_hora >= 1 AND max_citas_por_hora <= 10)
		)`,

		`ALTER TABLE servicios ADD COLUMN IF NOT EXISTS imagen_url TEXT`,

		// Migrar expires_at a TIMESTAMPTZ para evitar ambigüedad de zona horaria
		`ALTER TABLE delete_codes ALTER COLUMN expires_at TYPE TIMESTAMPTZ USING expires_at AT TIME ZONE 'UTC'`,

		// ── api_keys ─────────────────────────────────────────────────
		`CREATE TABLE IF NOT EXISTS api_keys (
			id           SERIAL PRIMARY KEY,
			negocio_id   INTEGER      NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			nombre       VARCHAR(100) NOT NULL,
			key_hash     VARCHAR(64)  NOT NULL UNIQUE,
			key_preview  VARCHAR(12)  NOT NULL,
			activo       BOOLEAN      DEFAULT true,
			created_at   TIMESTAMP    DEFAULT NOW(),
			last_used_at TIMESTAMP
		)`,

		// ── delete_codes (OTP para eliminar cuenta) ──────────────────
		`CREATE TABLE IF NOT EXISTS delete_codes (
			uid        VARCHAR(128) PRIMARY KEY,
			code       VARCHAR(6)   NOT NULL,
			expires_at TIMESTAMP    NOT NULL
		)`,

		// ── configuracion_calificaciones ─────────────────────────────
		`CREATE TABLE IF NOT EXISTS configuracion_calificaciones (
			id                      SERIAL PRIMARY KEY,
			negocio_id              INTEGER UNIQUE NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			activo                  BOOLEAN NOT NULL DEFAULT false,
			tiempo_despues_minutos  INTEGER NOT NULL DEFAULT 1440,
			mensaje                 TEXT    NOT NULL DEFAULT '¿Cómo fue tu experiencia con {servicio} en {negocio}, {nombre}? Tu opinión nos ayuda a mejorar.'
		)`,

		// ── configuracion_recordatorios ──────────────────────────────
		`CREATE TABLE IF NOT EXISTS configuracion_recordatorios (
			id                    SERIAL PRIMARY KEY,
			negocio_id            INTEGER UNIQUE NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			activo                BOOLEAN NOT NULL DEFAULT false,
			tiempo_antes_minutos  INTEGER NOT NULL DEFAULT 120,
			mensaje               TEXT    NOT NULL DEFAULT 'Hola {nombre}, te recordamos tu cita de {servicio} el {fecha} a las {hora}. ¡Te esperamos!'
		)`,

		// ── configuracion_notificaciones ──────────────────────────────
		`CREATE TABLE IF NOT EXISTS configuracion_notificaciones (
			id                  SERIAL PRIMARY KEY,
			negocio_id          INTEGER UNIQUE NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			recordatorio_cita   BOOLEAN DEFAULT true,
			horas_antes         INTEGER DEFAULT 2,
			notif_cita_nueva    BOOLEAN DEFAULT true,
			notif_cancelacion   BOOLEAN DEFAULT true,
			notif_confirmacion  BOOLEAN DEFAULT true,
			notif_calificacion  BOOLEAN DEFAULT true
		)`,

		// ── configuracion_fidelidad ───────────────────────────────────
		`CREATE TABLE IF NOT EXISTS configuracion_fidelidad (
			id                   SERIAL PRIMARY KEY,
			negocio_id           INTEGER UNIQUE NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			activo               BOOLEAN DEFAULT false,
			citas_para_recompensa INTEGER NOT NULL DEFAULT 10,
			tipo_descuento       VARCHAR(10) NOT NULL DEFAULT 'porcentaje'
			                     CHECK (tipo_descuento IN ('porcentaje', 'monto')),
			valor_descuento      DECIMAL(10,2) NOT NULL DEFAULT 10,
			mensaje_recompensa   TEXT NOT NULL DEFAULT
			                     '{nombre} completó su cita #{numero} y tiene un descuento pendiente 🎉'
		)`,

		`ALTER TABLE promociones ADD COLUMN IF NOT EXISTS fecha_inicio DATE`,
		`ALTER TABLE promociones ADD COLUMN IF NOT EXISTS fecha_fin   DATE`,

		// ── agente_imagenes (renamed from galeria) ────────────────────
		`DO $$ BEGIN
		  IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name='galeria')
		     AND NOT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name='agente_imagenes') THEN
		    ALTER TABLE galeria RENAME TO agente_imagenes;
		  END IF;
		END $$`,
		`CREATE TABLE IF NOT EXISTS agente_imagenes (
			id                    SERIAL PRIMARY KEY,
			negocio_id            INTEGER NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			url                   TEXT NOT NULL,
			descripcion           TEXT,
			descripcion_intencion TEXT,
			activo                BOOLEAN DEFAULT true,
			created_at            TIMESTAMP DEFAULT NOW()
		)`,
		`DO $$ BEGIN
		  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='agente_imagenes' AND column_name='imagen_url') THEN
		    ALTER TABLE agente_imagenes RENAME COLUMN imagen_url TO url;
		  END IF;
		END $$`,
		`ALTER TABLE agente_imagenes ADD COLUMN IF NOT EXISTS descripcion_intencion TEXT`,
		`ALTER TABLE agente_imagenes ADD COLUMN IF NOT EXISTS activo BOOLEAN DEFAULT true`,

		// ── promociones ──────────────────────────────────────────────
		`CREATE TABLE IF NOT EXISTS promociones (
			id           SERIAL PRIMARY KEY,
			negocio_id   INTEGER NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			titulo       VARCHAR(200) NOT NULL,
			descripcion  TEXT,
			imagen_url   TEXT,
			activo       BOOLEAN NOT NULL DEFAULT true,
			created_at   TIMESTAMP DEFAULT NOW()
		)`,

		// ── faqs ─────────────────────────────────────────────────────
		`CREATE TABLE IF NOT EXISTS faqs (
			id         SERIAL PRIMARY KEY,
			negocio_id INTEGER NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			pregunta   TEXT NOT NULL,
			respuesta  TEXT NOT NULL,
			activo     BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		// ── links ────────────────────────────────────────────────────
		`CREATE TABLE IF NOT EXISTS links (
			id         SERIAL PRIMARY KEY,
			negocio_id INTEGER NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			etiqueta   VARCHAR(100) NOT NULL,
			url        TEXT NOT NULL,
			activo     BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		// ── recompensas_clientes ──────────────────────────────────────
		`CREATE TABLE IF NOT EXISTS recompensas_clientes (
			id                   SERIAL PRIMARY KEY,
			negocio_id           INTEGER NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			cliente_id           INTEGER NOT NULL REFERENCES clientes(id) ON DELETE CASCADE,
			citas_completadas    INTEGER NOT NULL DEFAULT 0,
			recompensa_pendiente BOOLEAN DEFAULT false,
			ultima_recompensa_at TIMESTAMP WITH TIME ZONE,
			created_at           TIMESTAMP DEFAULT NOW(),
			updated_at           TIMESTAMP DEFAULT NOW(),
			UNIQUE(negocio_id, cliente_id)
		)`,

		// ── agente_configuracion ──────────────────────────────────────
		`CREATE TABLE IF NOT EXISTS agente_configuracion (
			id                        SERIAL PRIMARY KEY,
			negocio_id                INTEGER UNIQUE NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			nombre_agente             VARCHAR(100) DEFAULT 'Asistente virtual',
			tono                      VARCHAR(20) DEFAULT 'amigable',
			mensaje_bienvenida        TEXT,
			mensaje_fuera_horario     TEXT,
			instrucciones_adicionales TEXT,
			created_at                TIMESTAMP DEFAULT NOW(),
			updated_at                TIMESTAMP DEFAULT NOW()
		)`,

		// ── push_subscriptions (Web Push VAPID) ───────────────────────
		`CREATE TABLE IF NOT EXISTS push_subscriptions (
			id         SERIAL PRIMARY KEY,
			negocio_id INTEGER NOT NULL REFERENCES negocios(id) ON DELETE CASCADE,
			endpoint   TEXT NOT NULL UNIQUE,
			p256dh     TEXT NOT NULL,
			auth       TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
	}

	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("migration failed: %w\nstmt: %s", err, s[:min(len(s), 80)])
		}
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
