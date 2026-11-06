// External test package: testdb imports migrations, so an internal test
// importing testdb would be an import cycle.
package migrations_test

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/AndreyZubov/family-calendar/backend/internal/testdb"
	"github.com/AndreyZubov/family-calendar/backend/migrations"
)

// TestDownAndUpAgain rolls every migration back and applies it again. It
// catches broken "Down" sections, which nothing else exercises until the
// day someone needs a rollback.
func TestDownAndUpAgain(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("pgx", testdb.New(t)) // already migrated (template)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()

	p, err := migrations.NewProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	all, err := p.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	down, err := p.DownTo(ctx, 0)
	if err != nil {
		t.Fatalf("down to 0: %v", err)
	}
	if len(down) != len(all) {
		t.Errorf("rolled back %d of %d migrations", len(down), len(all))
	}
	var tables int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name <> 'goose_db_version'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Errorf("%d tables left after rolling everything back", tables)
	}

	if err := migrations.Up(ctx, db, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("up again: %v", err)
	}
	v, err := p.GetDBVersion(ctx)
	if err != nil || v != all[len(all)-1].Source.Version {
		t.Errorf("version after re-apply = %d, %v", v, err)
	}
}

func TestUpIsIdempotent(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("pgx", testdb.New(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for range 2 {
		if err := migrations.Up(context.Background(), db, slog.New(slog.DiscardHandler)); err != nil {
			t.Fatal(err)
		}
	}
}
