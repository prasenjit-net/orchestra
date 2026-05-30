package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ─── Bundle types ─────────────────────────────────────────────────────────────

type ImportBundle struct {
	Version    int              `json:"version"`
	ExportedAt string           `json:"exportedAt"`
	BundleType string           `json:"bundleType"` // "workflow" | "agent" | "script" | "connector"
	Definition *DefinitionExport `json:"definition,omitempty"`
	Scripts    []Script          `json:"scripts,omitempty"`
	Agents     []Agent           `json:"agents,omitempty"`
	Connectors []MCPServer       `json:"connectors,omitempty"`
}

type DefinitionExport struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Document    DefinitionDocument `json:"document"`
}

// ─── Analysis types ───────────────────────────────────────────────────────────

type ImportAnalysis struct {
	Ready     []ImportItem `json:"ready"`     // do not exist yet — always imported
	Conflicts []ImportItem `json:"conflicts"` // already exist — user decides per item
}

type ImportItem struct {
	Type string `json:"type"` // "definition" | "script" | "agent" | "connector"
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ─── Export ───────────────────────────────────────────────────────────────────

func (s *Service) ExportWorkflow(ctx context.Context, definitionID string) (ImportBundle, error) {
	def, err := s.GetDefinition(ctx, definitionID)
	if err != nil {
		return ImportBundle{}, fmt.Errorf("get definition: %w", err)
	}

	scriptIDs, agentIDs := extractStepDependencies(def.Document.Steps)

	var scripts []Script
	seenScript := map[string]bool{}
	for _, sid := range scriptIDs {
		if seenScript[sid] {
			continue
		}
		seenScript[sid] = true
		sc, err := s.GetScript(ctx, sid)
		if err != nil {
			continue // missing reference — skip gracefully
		}
		scripts = append(scripts, sc)
	}

	var agents []Agent
	connectorsByID := map[string]MCPServer{}
	seenAgent := map[string]bool{}
	for _, aid := range agentIDs {
		if seenAgent[aid] {
			continue
		}
		seenAgent[aid] = true
		ag, err := s.GetAgent(ctx, aid)
		if err != nil {
			continue
		}
		agents = append(agents, ag)
		for _, mcpID := range ag.MCPServerIDs {
			if _, already := connectorsByID[mcpID]; already {
				continue
			}
			srv, err := s.GetMCPServer(ctx, mcpID)
			if err != nil {
				continue
			}
			connectorsByID[mcpID] = srv
		}
	}

	connectors := make([]MCPServer, 0, len(connectorsByID))
	for _, srv := range connectorsByID {
		connectors = append(connectors, srv)
	}

	return ImportBundle{
		Version:    1,
		ExportedAt: formatTime(time.Now().UTC()),
		BundleType: "workflow",
		Definition: &DefinitionExport{
			ID:          def.ID,
			Name:        def.Name,
			Description: def.Description,
			Document:    def.Document,
		},
		Scripts:    scripts,
		Agents:     agents,
		Connectors: connectors,
	}, nil
}

func (s *Service) ExportAgent(ctx context.Context, agentID string) (ImportBundle, error) {
	ag, err := s.GetAgent(ctx, agentID)
	if err != nil {
		return ImportBundle{}, fmt.Errorf("get agent: %w", err)
	}

	connectorsByID := map[string]MCPServer{}
	for _, mcpID := range ag.MCPServerIDs {
		if _, already := connectorsByID[mcpID]; already {
			continue
		}
		srv, err := s.GetMCPServer(ctx, mcpID)
		if err != nil {
			continue
		}
		connectorsByID[mcpID] = srv
	}

	connectors := make([]MCPServer, 0, len(connectorsByID))
	for _, srv := range connectorsByID {
		connectors = append(connectors, srv)
	}

	return ImportBundle{
		Version:    1,
		ExportedAt: formatTime(time.Now().UTC()),
		BundleType: "agent",
		Agents:     []Agent{ag},
		Connectors: connectors,
	}, nil
}

func (s *Service) ExportScript(ctx context.Context, scriptID string) (ImportBundle, error) {
	sc, err := s.GetScript(ctx, scriptID)
	if err != nil {
		return ImportBundle{}, fmt.Errorf("get script: %w", err)
	}
	return ImportBundle{
		Version:    1,
		ExportedAt: formatTime(time.Now().UTC()),
		BundleType: "script",
		Scripts:    []Script{sc},
	}, nil
}

func (s *Service) ExportConnector(ctx context.Context, serverID string) (ImportBundle, error) {
	srv, err := s.GetMCPServer(ctx, serverID)
	if err != nil {
		return ImportBundle{}, fmt.Errorf("get connector: %w", err)
	}
	return ImportBundle{
		Version:    1,
		ExportedAt: formatTime(time.Now().UTC()),
		BundleType: "connector",
		Connectors: []MCPServer{srv},
	}, nil
}

// ─── Analyze ──────────────────────────────────────────────────────────────────

func (s *Service) AnalyzeImport(ctx context.Context, bundle ImportBundle) (ImportAnalysis, error) {
	var ready, conflicts []ImportItem

	classify := func(id, name, kind string) error {
		exists, err := s.entityExists(ctx, kind, id)
		if err != nil {
			return err
		}
		item := ImportItem{Type: kind, ID: id, Name: name}
		if exists {
			conflicts = append(conflicts, item)
		} else {
			ready = append(ready, item)
		}
		return nil
	}

	for _, sc := range bundle.Scripts {
		if err := classify(sc.ID, sc.Name, "script"); err != nil {
			return ImportAnalysis{}, err
		}
	}
	for _, ag := range bundle.Agents {
		if err := classify(ag.ID, ag.Name, "agent"); err != nil {
			return ImportAnalysis{}, err
		}
	}
	for _, srv := range bundle.Connectors {
		if err := classify(srv.ID, srv.Name, "connector"); err != nil {
			return ImportAnalysis{}, err
		}
	}
	if bundle.Definition != nil {
		if err := classify(bundle.Definition.ID, bundle.Definition.Name, "definition"); err != nil {
			return ImportAnalysis{}, err
		}
	}

	if ready == nil {
		ready = []ImportItem{}
	}
	if conflicts == nil {
		conflicts = []ImportItem{}
	}
	return ImportAnalysis{Ready: ready, Conflicts: conflicts}, nil
}

func (s *Service) entityExists(ctx context.Context, kind, id string) (bool, error) {
	var table string
	switch kind {
	case "script":
		table = "scripts"
	case "agent":
		table = "agents"
	case "connector":
		table = "mcp_servers"
	case "definition":
		table = "workflow_definitions"
	default:
		return false, fmt.Errorf("unknown entity kind %q", kind)
	}
	var count int
	err := s.db.QueryRowContext(ctx, s.rebind("SELECT COUNT(*) FROM "+table+" WHERE id = ?"), id).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check %s existence: %w", kind, err)
	}
	return count > 0, nil
}

// ─── Apply ────────────────────────────────────────────────────────────────────

func (s *Service) ApplyImport(ctx context.Context, bundle ImportBundle, overrideIDs []string) (int, error) {
	override := make(map[string]bool, len(overrideIDs))
	for _, id := range overrideIDs {
		override[id] = true
	}

	analysis, err := s.AnalyzeImport(ctx, bundle)
	if err != nil {
		return 0, err
	}

	// Build the set of IDs that will actually be imported (ready always + overridden conflicts).
	willImport := make(map[string]bool)
	for _, item := range analysis.Ready {
		willImport[item.ID] = true
	}
	for _, item := range analysis.Conflicts {
		if override[item.ID] {
			willImport[item.ID] = true
		}
	}

	now := time.Now().UTC()
	imported := 0

	// 1. Connectors
	for i := range bundle.Connectors {
		srv := bundle.Connectors[i]
		if !willImport[srv.ID] {
			continue
		}
		if err := s.upsertConnector(ctx, srv, now); err != nil {
			return imported, fmt.Errorf("import connector %q: %w", srv.ID, err)
		}
		imported++
	}

	// 2. Scripts
	for i := range bundle.Scripts {
		sc := bundle.Scripts[i]
		if !willImport[sc.ID] {
			continue
		}
		if err := s.upsertScript(ctx, sc, now); err != nil {
			return imported, fmt.Errorf("import script %q: %w", sc.ID, err)
		}
		imported++
	}

	// 3. Agents (after connectors so join rows can reference existing connectors)
	for i := range bundle.Agents {
		ag := bundle.Agents[i]
		if !willImport[ag.ID] {
			continue
		}
		if err := s.upsertAgent(ctx, ag, now); err != nil {
			return imported, fmt.Errorf("import agent %q: %w", ag.ID, err)
		}
		imported++
	}

	// 4. Definition
	if bundle.Definition != nil && willImport[bundle.Definition.ID] {
		if err := s.upsertDefinition(ctx, *bundle.Definition, now); err != nil {
			return imported, fmt.Errorf("import definition %q: %w", bundle.Definition.ID, err)
		}
		imported++
	}

	return imported, nil
}

// ─── Upsert helpers ───────────────────────────────────────────────────────────

func (s *Service) upsertScript(ctx context.Context, sc Script, now time.Time) error {
	exportsJSON, err := json.Marshal(sc.Exports)
	if err != nil {
		return fmt.Errorf("encode exports: %w", err)
	}
	ts := formatTime(now)
	if _, err := s.db.ExecContext(ctx, s.rebind(`
		INSERT INTO scripts (id, name, description, language, source, timeout_ms, exports_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name, description = EXCLUDED.description,
			language = EXCLUDED.language, source = EXCLUDED.source,
			timeout_ms = EXCLUDED.timeout_ms, exports_json = EXCLUDED.exports_json,
			updated_at = EXCLUDED.updated_at
	`), sc.ID, sc.Name, sc.Description, sc.Language, sc.Source, sc.TimeoutMs, string(exportsJSON), ts, ts); err != nil {
		return err
	}
	return nil
}

func (s *Service) upsertAgent(ctx context.Context, ag Agent, now time.Time) error {
	ts := formatTime(now)
	if _, err := s.db.ExecContext(ctx, s.rebind(`
		INSERT INTO agents (id, name, description, model, system_prompt, max_tokens, temperature, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name, description = EXCLUDED.description,
			model = EXCLUDED.model, system_prompt = EXCLUDED.system_prompt,
			max_tokens = EXCLUDED.max_tokens, temperature = EXCLUDED.temperature,
			updated_at = EXCLUDED.updated_at
	`), ag.ID, ag.Name, ag.Description, ag.Model, ag.SystemPrompt, ag.MaxTokens, ag.Temperature, ts, ts); err != nil {
		return err
	}

	// Recreate join rows for connectors that actually exist in the DB.
	if _, err := s.db.ExecContext(ctx, s.rebind(`DELETE FROM agent_mcp_servers WHERE agent_id = ?`), ag.ID); err != nil {
		return fmt.Errorf("clear agent connectors: %w", err)
	}
	for _, mcpID := range ag.MCPServerIDs {
		exists, err := s.entityExists(ctx, "connector", mcpID)
		if err != nil || !exists {
			continue // connector was skipped or is missing — omit link
		}
		if _, err := s.db.ExecContext(ctx, s.rebind(`
			INSERT INTO agent_mcp_servers (agent_id, server_id) VALUES (?, ?)
			ON CONFLICT (agent_id, server_id) DO NOTHING
		`), ag.ID, mcpID); err != nil {
			return fmt.Errorf("link agent connector %s: %w", mcpID, err)
		}
	}
	return nil
}

func (s *Service) upsertConnector(ctx context.Context, srv MCPServer, now time.Time) error {
	headersJSON, err := json.Marshal(srv.Headers)
	if err != nil {
		return fmt.Errorf("encode headers: %w", err)
	}
	toolsJSON, err := json.Marshal(srv.Tools)
	if err != nil {
		return fmt.Errorf("encode tools: %w", err)
	}
	enabled := 0
	if srv.Enabled {
		enabled = 1
	}
	ts := formatTime(now)
	if _, err := s.db.ExecContext(ctx, s.rebind(`
		INSERT INTO mcp_servers (id, name, description, group_name, url, headers_json, enabled, tools_json, explored_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name, description = EXCLUDED.description,
			group_name = EXCLUDED.group_name, url = EXCLUDED.url,
			headers_json = EXCLUDED.headers_json, enabled = EXCLUDED.enabled,
			tools_json = EXCLUDED.tools_json, updated_at = EXCLUDED.updated_at
	`), srv.ID, srv.Name, srv.Description, srv.Group, srv.URL, string(headersJSON), enabled, string(toolsJSON), ts, ts); err != nil {
		return err
	}
	return nil
}

func (s *Service) upsertDefinition(ctx context.Context, def DefinitionExport, now time.Time) error {
	documentJSON, err := json.Marshal(def.Document)
	if err != nil {
		return fmt.Errorf("encode document: %w", err)
	}
	ts := formatTime(now)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin definition import tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, s.rebind(`
		INSERT INTO workflow_definitions (id, name, description, status, active_version, created_at, updated_at)
		VALUES (?, ?, ?, 'published', 1, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name, description = EXCLUDED.description,
			updated_at = EXCLUDED.updated_at
	`), def.ID, def.Name, def.Description, ts, ts); err != nil {
		return fmt.Errorf("upsert definition: %w", err)
	}

	if _, err := tx.ExecContext(ctx, s.rebind(`
		INSERT INTO workflow_definition_versions (definition_id, version, status, document_json, created_at, updated_at, published_at)
		VALUES (?, 1, 'published', ?, ?, ?, ?)
		ON CONFLICT (definition_id, version) DO UPDATE SET
			document_json = EXCLUDED.document_json, status = EXCLUDED.status,
			updated_at = EXCLUDED.updated_at, published_at = EXCLUDED.published_at
	`), def.ID, string(documentJSON), ts, ts, ts); err != nil {
		return fmt.Errorf("upsert definition version: %w", err)
	}

	return tx.Commit()
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func extractStepDependencies(steps []StepDefinition) (scriptIDs, agentIDs []string) {
	for _, step := range steps {
		if len(step.Input) == 0 {
			continue
		}
		var input map[string]any
		if err := json.Unmarshal(step.Input, &input); err != nil {
			continue
		}
		switch step.Activity {
		case "script":
			if id, _ := input["scriptId"].(string); id != "" {
				scriptIDs = append(scriptIDs, id)
			}
		case "agent":
			if id, _ := input["agentId"].(string); id != "" {
				agentIDs = append(agentIDs, id)
			}
		}
	}
	return
}
