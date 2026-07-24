package workflow

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/prasenjit-net/orchestra/internal/config"
)

func TestWorkflowStartSchemaValidationAndEndOutputMapping(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, cfg.AI, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	startSchema, err := service.CreateJSONSchema(context.Background(), CreateJSONSchemaInput{
		Name: "Start input",
		Schema: []byte(`{
			"type":"object",
			"properties":{"name":{"type":"string","minLength":2}},
			"required":["name"],
			"additionalProperties":false
		}`),
	})
	if err != nil {
		t.Fatalf("CreateJSONSchema start returned error: %v", err)
	}
	endSchema, err := service.CreateJSONSchema(context.Background(), CreateJSONSchemaInput{
		Name:   "End output",
		Schema: []byte(`{"type":"object","properties":{"message":{"type":"string"}}}`),
	})
	if err != nil {
		t.Fatalf("CreateJSONSchema end returned error: %v", err)
	}

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:          "Schema contracts",
		Description:   "Validates input and maps output",
		StartSchemaID: startSchema.ID,
		EndSchemaID:   endSchema.ID,
		EndOutput:     []byte(`{"message":"Hello {{input.name}}"}`),
		Steps: []StepDefinition{
			{Name: "noop", Activity: "noop", Input: []byte(`{"ok":true}`)},
		},
	})
	if err != nil {
		t.Fatalf("CreateDefinition returned error: %v", err)
	}

	if _, err := service.StartWorkflowWithInput(context.Background(), StartWorkflowInput{
		DefinitionID: definition.ID,
		Input:        map[string]any{"extra": true},
	}); err == nil {
		t.Fatal("expected invalid start input to be rejected")
	}

	instance, err := service.StartWorkflowWithInput(context.Background(), StartWorkflowInput{
		DefinitionID: definition.ID,
		Input:        map[string]any{"name": "Ada"},
	})
	if err != nil {
		t.Fatalf("StartWorkflowWithInput returned error: %v", err)
	}
	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	completed, err := service.GetWorkflow(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflow returned error: %v", err)
	}
	var output map[string]any
	if err := json.Unmarshal(completed.LastOutput, &output); err != nil {
		t.Fatalf("Unmarshal output returned error: %v", err)
	}
	if output["message"] != "Hello Ada" {
		t.Fatalf("expected mapped output, got %#v", output)
	}
}
