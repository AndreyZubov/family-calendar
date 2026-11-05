package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/AndreyZubov/family-calendar/backend/migrations"
)

// NewMigrator returns a goose provider over the embedded migrations.
//
// A Postgres advisory lock is held while migrating, so several app instances
// starting at once cannot apply the same migration concurrently.
func NewMigrator(db *sql.DB) (*goose.Provider, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("create migration locker: %w", err)
	}

	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS,
		goose.WithSessionLocker(locker),
	)
	if err != nil {
		return nil, fmt.Errorf("create migration provider: %w", err)
	}
	return p, nil
}

// Migrate applies all pending migrations.
func Migrate(ctx context.Context, db *sql.DB, log *slog.Logger) error {
	p, err := NewMigrator(db)
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
