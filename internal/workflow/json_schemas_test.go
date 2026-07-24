package workflow

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/prasenjit-net/orchestra/internal/config"
)

func TestJSONSchemaCRUDAndImportExport(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, cfg.AI, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	created, err := service.CreateJSONSchema(context.Background(), CreateJSONSchemaInput{
		Name:        "Customer",
		Description: "Customer payload",
		Schema:      []byte(`{"type":"object","properties":{"email":{"type":"string","format":"email"}},"required":["email"]}`),
	})
	if err != nil {
		t.Fatalf("CreateJSONSchema returned error: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected generated schema ID")
	}

	list, err := service.ListJSONSchemas(context.Background())
	if err != nil {
		t.Fatalf("ListJSONSchemas returned error: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Customer" {
		t.Fatalf("expected created schema in list, got %#v", list)
	}

	updated, err := service.UpdateJSONSchema(context.Background(), created.ID, CreateJSONSchemaInput{
		Name:        "Customer v2",
		Description: "Updated customer payload",
		Schema:      []byte(`{"type":"object","properties":{"id":{"type":"string"},"email":{"type":"string","format":"email"}},"required":["id","email"]}`),
	})
	if err != nil {
		t.Fatalf("UpdateJSONSchema returned error: %v", err)
	}
	if updated.Name != "Customer v2" {
		t.Fatalf("expected updated name, got %q", updated.Name)
	}

	bundle, err := service.ExportJSONSchema(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("ExportJSONSchema returned error: %v", err)
	}
	if bundle.BundleType != "json-schema" || len(bundle.JSONSchemas) != 1 {
		t.Fatalf("expected JSON schema bundle, got %#v", bundle)
	}

	allSchemasBundle, err := service.ExportJSONSchemas(context.Background())
	if err != nil {
		t.Fatalf("ExportJSONSchemas returned error: %v", err)
	}
	if len(allSchemasBundle.JSONSchemas) != 1 {
		t.Fatalf("expected all schema bundle to include one schema, got %#v", allSchemasBundle)
	}

	analysis, err := service.AnalyzeImport(context.Background(), bundle)
	if err != nil {
		t.Fatalf("AnalyzeImport returned error: %v", err)
	}
	if len(analysis.Conflicts) != 1 || analysis.Conflicts[0].Type != "json-schema" {
		t.Fatalf("expected schema conflict, got %#v", analysis)
	}

	if err := service.DeleteJSONSchema(context.Background(), created.ID); err != nil {
		t.Fatalf("DeleteJSONSchema returned error: %v", err)
	}
	imported, err := service.ApplyImport(context.Background(), bundle, nil)
	if err != nil {
		t.Fatalf("ApplyImport returned error: %v", err)
	}
	if imported != 1 {
		t.Fatalf("expected one imported schema, got %d", imported)
	}
}
