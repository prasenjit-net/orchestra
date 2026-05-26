package app

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/prasenjit-net/orchestra/internal/config"
	"github.com/prasenjit-net/orchestra/internal/workflow"
)

var (
	schemaDriverFlag string
	schemaCreateFlag bool
)

var schemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Print (or apply) the DDL for the configured database driver",
	Long: `Print the CREATE TABLE / CREATE INDEX statements for the active database driver.

By default the DDL is written to stdout so you can review and run it manually.
Pass --create to execute the statements against the configured database instead.

For PostgreSQL the application never auto-creates tables; run this command once
after provisioning the database before starting the server.

Examples:
  # Print Postgres DDL to stdout
  orchestra schema --driver postgres

  # Print DDL derived from the active config
  orchestra schema --config config.toml

  # Apply DDL directly to the Postgres database in config.toml
  orchestra schema --create`,
	RunE: runSchema,
}

func init() {
	schemaCmd.Flags().StringVar(&schemaDriverFlag, "driver", "", "Database driver to target (sqlite|postgres); overrides workflow.databaseDriver in config")
	schemaCmd.Flags().BoolVar(&schemaCreateFlag, "create", false, "Execute DDL against the database instead of printing to stdout")
}

func runSchema(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load(viper.GetViper())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// --driver flag overrides config.
	driver := cfg.Workflow.DatabaseDriver
	if schemaDriverFlag != "" {
		driver = schemaDriverFlag
	}
	if driver == "" {
		driver = "sqlite"
	}

	dialect := workflow.Dialect(driver)
	statements := dialect.DDL()

	if !schemaCreateFlag {
		// Print to stdout.
		fmt.Printf("-- Orchestra schema DDL (%s)\n", dialect)
		fmt.Println("-- Run this once before starting the server for the first time.")
		fmt.Println()
		for _, stmt := range statements {
			fmt.Println(strings.TrimSpace(stmt) + ";")
			fmt.Println()
		}
		return nil
	}

	// --create: open the database and execute.
	db, err := openSchemaDB(cfg, dialect)
	if err != nil {
		return err
	}
	defer db.Close()

	fmt.Fprintf(os.Stderr, "Applying %d DDL statements to %s database…\n", len(statements), dialect)
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("execute DDL: %w\n\nStatement:\n%s", err, strings.TrimSpace(stmt))
		}
	}
	fmt.Fprintln(os.Stderr, "Schema created successfully.")
	return nil
}

func openSchemaDB(cfg config.Config, dialect workflow.Dialect) (*sql.DB, error) {
	switch dialect {
	case workflow.DialectPostgres:
		if cfg.Workflow.DatabaseURL == "" {
			return nil, fmt.Errorf("workflow.databaseURL is required for postgres; set it in config or via APP_WORKFLOW_DATABASEURL")
		}
		db, err := sql.Open("pgx", cfg.Workflow.DatabaseURL)
		if err != nil {
			return nil, fmt.Errorf("open postgres database: %w", err)
		}
		return db, nil
	default:
		if err := os.MkdirAll(dirOf(cfg.Workflow.DatabasePath), 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
		db, err := sql.Open("sqlite", cfg.Workflow.DatabasePath)
		if err != nil {
			return nil, fmt.Errorf("open sqlite database: %w", err)
		}
		return db, nil
	}
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}
