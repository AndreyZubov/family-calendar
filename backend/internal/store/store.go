// Package store provides access to PostgreSQL.
//
// SQL lives in backend/queries and is compiled to Go by sqlc into the db
// subpackage (generated, do not edit). Conventions for the queries:
//
//   - Family-owned rows are always filtered by family_id, so an ID that
//     belongs to another family behaves exactly like a missing one (IDOR
//     protection). Callers pass the family of the authenticated user.
//   - Session and invite tokens are looked up by their SHA-256 hash, never by
//     the raw token.
package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/AndreyZubov/family-calendar/backend/internal/store/db"
	"github.com/AndreyZubov/family-calendar/backend/migrations"
)

// Config holds connection settings.
type Config struct {
	// URL is a libpq-style connection string, e.g. postgres://user:pass@host:5432/db.
	URL string
	// MaxConns caps the pool size. Postgres runs with max_connections=20,
	// so keep this small. Zero means DefaultMaxConns.
	MaxConns int32
}

// DefaultMaxConns is enough for a family-sized app and leaves room in
// Postgres for migrations, psql and backups.
const DefaultMaxConns = 5

// Store owns the connection pool and exposes the generated queries.
type Store struct {
	*db.Queries

	pool *pgxpool.Pool
}

// New creates a connection pool and verifies the database is reachable.
func New(ctx context.Context, cfg Config) (*Store, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		// pgx redacts the password in parse errors, so wrapping is safe.
		return nil, fmt.Errorf("parse database URL: %w", err)
	}

	pcfg.MaxConns = cfg.MaxConns
	if pcfg.MaxConns <= 0 {
		pcfg.MaxConns = DefaultMaxConns
	}
	pcfg.MinConns = 0                      // idle app holds no connections
	pcfg.MaxConnIdleTime = 5 * time.Minute // release connections when quiet
	pcfg.MaxConnLifetime = time.Hour       // recycle long-lived connections
	pcfg.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	return &Store{Queries: db.New(pool), pool: pool}, nil
}

// Close releases all connections. Safe to call once on shutdown.
func (s *Store) Close() { s.pool.Close() }

// Ping checks connectivity; used by the readiness probe.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Migrate applies pending schema migrations over the store's pool.
func (s *Store) Migrate(ctx context.Context, log *slog.Logger) error {
	sqlDB := stdlib.OpenDBFromPool(s.pool)
	defer sqlDB.Close()
	return migrations.Up(ctx, sqlDB, log)
}

// InTx runs fn inside a transaction. It commits if fn returns nil and rolls
// back otherwise (including on panic).
func (s *Store) InTx(ctx context.Context, fn func(q *db.Queries) error) (err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		// WithoutCancel: roll back even if the request context is already done.
		// The rollback error is ignored: the original failure is what matters.
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()

	if err = fn(s.Queries.WithTx(tx)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// IsNotFound reports whether err means a :one query matched no rows.
func IsNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// Postgres error codes, see https://www.postgresql.org/docs/current/errcodes-appendix.html
const (
	codeUniqueViolation     = "23505"
	codeForeignKeyViolation = "23503"
	codeCheckViolation      = "23514"
)

// IsUniqueViolation reports whether err is a unique constraint violation.
func IsUniqueViolation(err error) bool { return hasCode(err, codeUniqueViolation) }

// IsForeignKeyViolation reports whether err is a foreign key violation, e.g.
// adding a participant who is not a member of the event's family.
func IsForeignKeyViolation(err error) bool { return hasCode(err, codeForeignKeyViolation) }

// IsCheckViolation reports whether err is a CHECK constraint violation, e.g.
// an event that ends before it starts.
func IsCheckViolation(err error) bool { return hasCode(err, codeCheckViolation) }

func hasCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
