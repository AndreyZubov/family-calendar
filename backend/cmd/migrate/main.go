// Command migrate manages database schema migrations during development.
//
// In production the server applies pending migrations itself on startup;
// this tool is for inspecting state, rolling back and creating new files.
//
// Usage:
//
//	migrate up | down | status | version
//	migrate create <name>
//
// The database is taken from the DATABASE_URL environment variable.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"

	"github.com/AndreyZubov/family-calendar/backend/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run() error {
	dir := flag.String("dir", "migrations", "directory for new migration files (create only)")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(),
			"usage: migrate [-dir migrations] up | down | status | version | create <name>")
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		return errors.New("missing command")
	}

	// Creating a file does not need a database.
	if args[0] == "create" {
		if len(args) != 2 {
			return errors.New("usage: migrate create <name>")
		}
		goose.SetSequential(true)
		return goose.Create(nil, *dir, args[1], "sql")
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is not set")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	p, err := migrations.NewProvider(db)
	if err != nil {
		return err
	}

	switch args[0] {
	case "up":
		return migrations.Up(ctx, db, log)

	case "down":
		r, err := p.Down(ctx)
		if errors.Is(err, goose.ErrNoNextVersion) {
			fmt.Println("nothing to roll back")
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Printf("rolled back %s (%s)\n", r.Source.Path, r.Duration)
		return nil

	case "status":
		statuses, err := p.Status(ctx)
		if err != nil {
			return err
		}
		for _, s := range statuses {
			applied := "pending"
			if s.State == goose.StateApplied {
				applied = "applied " + s.AppliedAt.Format("2006-01-02 15:04:05")
			}
			fmt.Printf("%-40s %s\n", s.Source.Path, applied)
		}
		return nil

	case "version":
		v, err := p.GetDBVersion(ctx)
		if err != nil {
			return err
		}
		fmt.Println(v)
		return nil

	default:
		flag.Usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}
