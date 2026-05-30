package workflow

import (
	"context"
	"encoding/json"
	"fmt"
)

// findDefinitionsReferencingScript returns EntityRefs for every workflow definition
// version whose document_json references the given scriptID.
func (s *Service) findDefinitionsReferencingScript(ctx context.Context, scriptID string) ([]EntityRef, error) {
	return s.findDefinitionVersionsReferencingID(ctx, func(scriptIDs, _ []string) bool {
		for _, id := range scriptIDs {
			if id == scriptID {
				return true
			}
		}
		return false
	})
}

// findDefinitionsReferencingAgent returns EntityRefs for every workflow definition
// version whose document_json references the given agentID.
func (s *Service) findDefinitionsReferencingAgent(ctx context.Context, agentID string) ([]EntityRef, error) {
	return s.findDefinitionVersionsReferencingID(ctx, func(_, agentIDs []string) bool {
		for _, id := range agentIDs {
			if id == agentID {
				return true
			}
		}
		return false
	})
}

// findDefinitionVersionsReferencingID iterates all definition versions and returns refs
// for those where the predicate matches.
func (s *Service) findDefinitionVersionsReferencingID(ctx context.Context, match func(scriptIDs, agentIDs []string) bool) ([]EntityRef, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT wdv.definition_id, wdv.version, wdv.document_json, wd.name
		FROM workflow_definition_versions wdv
		JOIN workflow_definitions wd ON wd.id = wdv.definition_id
	`)
	if err != nil {
		return nil, fmt.Errorf("query definition versions for usage check: %w", err)
	}
	defer rows.Close()

	var refs []EntityRef
	for rows.Next() {
		var defID string
		var version int
		var documentJSON string
		var defName string
		if err := rows.Scan(&defID, &version, &documentJSON, &defName); err != nil {
			return nil, fmt.Errorf("scan definition version: %w", err)
		}
		var doc DefinitionDocument
		if err := json.Unmarshal([]byte(documentJSON), &doc); err != nil {
			continue
		}
		scriptIDs, agentIDs := extractStepDependencies(doc.Steps)
		if match(scriptIDs, agentIDs) {
			refs = append(refs, EntityRef{
				Kind:  "workflow_definition_version",
				ID:    fmt.Sprintf("%s@%d", defID, version),
				Label: fmt.Sprintf("%s v%d", defName, version),
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate definition versions: %w", err)
	}
	return refs, nil
}

// findAgentsUsingMCPServer returns EntityRefs for every agent linked to the given MCP server.
func (s *Service) findAgentsUsingMCPServer(ctx context.Context, serverID string) ([]EntityRef, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(`
		SELECT a.id, a.name
		FROM agents a
		JOIN agent_mcp_servers ams ON ams.agent_id = a.id
		WHERE ams.server_id = ?
	`), serverID)
	if err != nil {
		return nil, fmt.Errorf("query agents using mcp server: %w", err)
	}
	defer rows.Close()

	var refs []EntityRef
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("scan agent row: %w", err)
		}
		refs = append(refs, EntityRef{Kind: "agent", ID: id, Label: name})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agents: %w", err)
	}
	return refs, nil
}

// findActiveInstancesForDefinition returns EntityRefs for workflow instances that are
// still running (pending/running/waiting/paused) for the given definition.
func (s *Service) findActiveInstancesForDefinition(ctx context.Context, definitionID string) ([]EntityRef, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(`
		SELECT id, status
		FROM workflow_instances
		WHERE definition_id = ? AND status IN ('pending', 'running', 'waiting', 'paused')
	`), definitionID)
	if err != nil {
		return nil, fmt.Errorf("query active instances for definition: %w", err)
	}
	defer rows.Close()

	var refs []EntityRef
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			return nil, fmt.Errorf("scan instance row: %w", err)
		}
		refs = append(refs, EntityRef{Kind: "workflow_instance", ID: id, Label: fmt.Sprintf("instance %s (%s)", id, status)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate instances: %w", err)
	}
	return refs, nil
}
