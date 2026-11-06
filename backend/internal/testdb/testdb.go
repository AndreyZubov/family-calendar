// Package testdb gives each integration test its own fresh, migrated
// PostgreSQL database.
//
// Where the server comes from:
//   - TEST_DATABASE_URL set: that server is used (fast; e.g. `make test-db`
//     against the local dev Postgres). The role must be allowed to create
//     databases.
//   - otherwise: a disposable postgres:17-alpine container is started with
//     testcontainers (needs Docker) and reused by all tests of the package.
//
// How a test gets a database: migrations run once into a template database;
// every test then gets `CREATE DATABASE ... TEMPLATE <template>` — a file-level
// copy that takes milliseconds — and the copy is dropped when the test ends.
// Tests are therefore fully isolated, need no cleanup code and can run with
// t.Parallel().
//
// With `go test -short` database tests are skipped.
package testdb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // "pgx" database/sql driver for goose
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/AndreyZubov/family-calendar/backend/migrations"
)

// Image is the Postgres image for test containers; keep it in sync with
// deploy/docker-compose*.yml.
const Image = "postgres:17-alpine"

var (
	once     sync.Once
	adminURL string // connection to the server's maintenance database
	template string // name of the migrated template database
	setupErr error
)

// New returns the connection string of a new, migrated, empty database
// that exists only for the duration of t.
func New(t testing.TB) string {
	t.Helper()
	if testing.Short() {
		t.Skip("database test skipped in -short mode")
	}
	once.Do(func() { setupErr = setup() })
	if setupErr != nil {
		t.Fatalf("testdb: %v\n(start Docker, or set TEST_DATABASE_URL, or run go test -short)", setupErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("testdb: connect: %v", err)
	}
	defer conn.Close(ctx)

	name := "fc_test_" + randomSuffix()
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", name, template)); err != nil {
		t.Fatalf("testdb: create database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, adminURL)
		if err != nil {
			t.Errorf("testdb: cleanup connect: %v", err)
			return
		}
		defer conn.Close(ctx)
		// FORCE terminates connections a test may have leaked.
		if _, err := conn.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", name)); err != nil {
			t.Errorf("testdb: drop database: %v", err)
		}
	})
	return withDatabase(adminURL, name)
}

func setup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if u := os.Getenv("TEST_DATABASE_URL"); u != "" {
		adminURL = u
	} else {
		// The container is not terminated explicitly: it is shared by all
		// tests in this process, and testcontainers' reaper (Ryuk) removes
		// it when the test binary exits.
		ctr, err := tcpostgres.Run(ctx, Image,
			tcpostgres.WithDatabase("postgres"),
			tcpostgres.WithUsername("test"),
			tcpostgres.WithPassword("test"),
			tcpostgres.BasicWaitStrategies(),
		)
		if err != nil {
			return fmt.Errorf("start postgres container: %w", err)
		}
		adminURL, err = ctr.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			return fmt.Errorf("container connection string: %w", err)
		}
	}

	// The template name is derived from the migrations' content: a schema
	// change creates a new template, an unchanged schema reuses the existing
	// one (relevant when the server outlives test runs, e.g. the dev DB).
	sum, err := migrationsHash()
	if err != nil {
		return err
	}
	template = "fc_template_" + sum[:16]

	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	// Several test packages run in parallel processes against the same
	// server; the lock makes exactly one of them build the template.
	lockID := int64(binary.BigEndian.Uint64([]byte(sum[:8])))
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	defer conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", lockID) //nolint:errcheck // best effort

	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", template).Scan(&exists); err != nil {
		return fmt.Errorf("check template: %w", err)
	}
	if exists {
		return nil
	}

	// Build under a temporary name and rename at the end, so a crash halfway
	// never leaves a half-migrated template behind.
	building := template + "_tmp"
	if _, err := conn.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", building)); err != nil {
		return fmt.Errorf("drop stale template: %w", err)
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+building); err != nil {
		return fmt.Errorf("create template: %w", err)
	}
	sqlDB, err := sql.Open("pgx", withDatabase(adminURL, building))
	if err != nil {
		return fmt.Errorf("connect to template: %w", err)
	}
	err = migrations.Up(ctx, sqlDB, slog.New(slog.DiscardHandler))
	sqlDB.Close() // a template must have no open connections
	if err != nil {
		return fmt.Errorf("migrate template: %w", err)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("ALTER DATABASE %s RENAME TO %s", building, template)); err != nil {
		return fmt.Errorf("rename template: %w", err)
	}
	return nil
}

// migrationsHash fingerprints all migration files.
func migrationsHash() (string, error) {
	h := sha256.New()
	err := fs.WalkDir(migrations.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(migrations.FS, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", path, len(b))
		h.Write(b)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash migrations: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// withDatabase returns dsn pointing at another database on the same server.
func withDatabase(dsn, db string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		panic(fmt.Sprintf("testdb: bad DSN: %v", err)) // only from our own config
	}
	u.Path = "/" + db
	return u.String()
}

func randomSuffix() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
