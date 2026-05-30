package workflow

import "fmt"

// EntityRef identifies an entity that is blocking a delete operation.
type EntityRef struct {
	Kind  string `json:"kind"`            // e.g. "workflow_definition_version", "agent", "workflow_instance"
	ID    string `json:"id"`
	Label string `json:"label,omitempty"` // human-friendly name/version string
}

// EntityInUseError is returned when a delete is blocked because other entities reference the target.
type EntityInUseError struct {
	Kind       string      `json:"kind"`
	ID         string      `json:"id"`
	References []EntityRef `json:"references"`
}

func (e *EntityInUseError) Error() string {
	return fmt.Sprintf("%s %q is in use by %d reference(s)", e.Kind, e.ID, len(e.References))
}
