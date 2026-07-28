package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // Register the pgx database/sql driver.
	_ "modernc.org/sqlite"             // Register the pure-Go SQLite database/sql driver.

	"github.com/prasenjit-net/orchestra/internal/config"
)

type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
)

func ResolveDialect(raw string) Dialect {
	if raw == string(DialectPostgres) {
		return DialectPostgres
	}
	return DialectSQLite
}

func (d Dialect) IsPostgres() bool { return d == DialectPostgres }

func (d Dialect) DriverName() string {
	if d.IsPostgres() {
		return "pgx"
	}
	return "sqlite"
}

func (d Dialect) Rebind(query string) string {
	if !d.IsPostgres() {
		return query
	}
	out := make([]byte, 0, len(query)+10)
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			out = append(out, '$')
			out = strconv.AppendInt(out, int64(n), 10)
			continue
		}
		out = append(out, query[i])
	}
	return string(out)
}

func Open(ctx context.Context, cfg config.WorkflowConfig) (*sql.DB, Dialect, error) {
	dialect := ResolveDialect(cfg.DatabaseDriver)
	if dialect.IsPostgres() {
		if cfg.DatabaseURL == "" {
			return nil, dialect, fmt.Errorf("workflow.databaseURL is required when databaseDriver is postgres")
		}
		db, err := sql.Open(dialect.DriverName(), cfg.DatabaseURL)
		if err != nil {
			return nil, dialect, fmt.Errorf("open postgres database: %w", err)
		}
		db.SetMaxOpenConns(25)
		db.SetMaxIdleConns(5)
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := db.PingContext(pingCtx); err != nil {
			_ = db.Close()
			return nil, dialect, fmt.Errorf("ping postgres database: %w", err)
		}
		return db, dialect, nil
	}

	if cfg.DatabasePath != "" && cfg.DatabasePath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(cfg.DatabasePath), 0o750); err != nil {
			return nil, dialect, fmt.Errorf("create database directory: %w", err)
		}
	}
	db, err := sql.Open(dialect.DriverName(), cfg.DatabasePath)
	if err != nil {
		return nil, dialect, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA foreign_keys = ON`,
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			_ = db.Close()
			return nil, dialect, fmt.Errorf("configure sqlite database: %w", err)
		}
	}
	return db, dialect, nil
}
