// Package migrations embeds the SQL schema migrations into the binary and
// applies them with goose, so the server can migrate on startup without
// shipping files alongside it.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// FS contains all goose SQL migrations, named NNNNN_description.sql.
//
//go:embed *.sql
var FS embed.FS

// NewProvider returns a goose provider over the embedded migrations.
//
// A Postgres advisory lock is held while migrating, so several app instances
// starting at once cannot apply the same migration concurrently.
func NewProvider(db *sql.DB) (*goose.Provider, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("create migration locker: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, FS, goose.WithSessionLocker(locker))
	if err != nil {
		return nil, fmt.Errorf("create migration provider: %w", err)
	}
	return p, nil
}

// Up applies all pending migrations and logs each one.
func Up(ctx context.Context, db *sql.DB, log *slog.Logger) error {
	p, err := NewProvider(db)
	if err != nil {
		return err
	}
	results, err := p.Up(ctx)
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	for _, r := range results {
		log.InfoContext(ctx, "migration applied",
			slog.Int64("version", r.Source.Version),
			slog.String("file", r.Source.Path),
			slog.Duration("duration", r.Duration),
		)
	}
	return nil
}
