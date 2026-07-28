package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/prasenjit-net/orchestra/internal/auth"
	"github.com/prasenjit-net/orchestra/internal/config"
	appdb "github.com/prasenjit-net/orchestra/internal/database"
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
	statements := append([]string(nil), dialect.DDL()...)
	statements = append(statements, auth.SchemaStatements(appdb.Dialect(dialect))...)

	migrations := dialect.Migrations()

	if !schemaCreateFlag {
		// Print to stdout.
		fmt.Printf("-- Orchestra schema DDL (%s)\n", dialect)
		fmt.Println("-- Run this once before starting the server for the first time.")
		fmt.Println()
		for _, stmt := range statements {
			fmt.Println(strings.TrimSpace(stmt) + ";")
			fmt.Println()
		}
		if len(migrations) > 0 {
			fmt.Println("-- Idempotent migration statements (safe to run on existing databases):")
			for _, stmt := range migrations {
				fmt.Println(strings.TrimSpace(stmt) + ";")
				fmt.Println()
			}
		}
		return nil
	}

	// --create: open the database and execute DDL then migrations.
	db, err := openSchemaDB(cfg, dialect)
	if err != nil {
		return err
	}
	defer db.Close()

	total := len(statements) + len(migrations)
	fmt.Fprintf(os.Stderr, "Applying %d DDL statements to %s database…\n", total, dialect)
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("execute DDL: %w\n\nStatement:\n%s", err, strings.TrimSpace(stmt))
		}
	}
	for _, stmt := range migrations {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("execute migration: %w\n\nStatement:\n%s", err, strings.TrimSpace(stmt))
		}
	}
	fmt.Fprintln(os.Stderr, "Schema applied successfully.")
	return nil
}

func openSchemaDB(cfg config.Config, dialect workflow.Dialect) (*sql.DB, error) {
	cfg.Workflow.DatabaseDriver = string(dialect)
	db, _, err := appdb.Open(context.Background(), cfg.Workflow)
	return db, err
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}
