package workflow

import (
	"encoding/json"
	"time"
)

const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusWaiting   = "waiting"
	StatusPaused    = "paused"
	StatusCanceled  = "canceled"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

type RetryPolicy struct {
	MaxAttempts    int `json:"maxAttempts"`
	BackoffSeconds int `json:"backoffSeconds"`
}

type StepLayout struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type TransitionCondition struct {
	Path     string          `json:"path"`
	Operator string          `json:"operator"`
	Value    json.RawMessage `json:"value,omitempty"`
}

type StepTransition struct {
	To        string               `json:"to"`
	Label     string               `json:"label,omitempty"`
	Condition *TransitionCondition `json:"condition,omitempty"`
}

type StepDefinition struct {
	Name        string           `json:"name"`
	Activity    string           `json:"activity"`
	Input       json.RawMessage  `json:"input,omitempty"`
	Retry       RetryPolicy      `json:"retry"`
	Layout      StepLayout       `json:"layout"`
	Transitions []StepTransition `json:"transitions"`
}

type DefinitionDocument struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Steps       []StepDefinition `json:"steps"`
}

type CreateDefinitionInput struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Steps       []StepDefinition `json:"steps"`
}

type DefinitionVersionSummary struct {
	Version     int        `json:"version"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
}

type DefinitionSummary struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Status        string    `json:"status"`
	ActiveVersion int       `json:"activeVersion"`
	LatestVersion int       `json:"latestVersion"`
	DraftVersion  int       `json:"draftVersion,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type DefinitionDetails struct {
	DefinitionSummary
	Document DefinitionDocument         `json:"document"`
	Versions []DefinitionVersionSummary `json:"versions"`
}

type WorkflowInstance struct {
	ID                string          `json:"id"`
	DefinitionID      string          `json:"definitionId"`
	DefinitionVersion int             `json:"definitionVersion"`
	Status            string          `json:"status"`
	CurrentStepIndex  int             `json:"currentStepIndex"`
	CurrentStepName   string          `json:"currentStepName"`
	CurrentActivity   string          `json:"currentActivity"`
	LastEventSequence int             `json:"lastEventSequence"`
	LastError         string          `json:"lastError,omitempty"`
	LastOutput        json.RawMessage `json:"lastOutput,omitempty"`
	Context           json.RawMessage `json:"context,omitempty"`
	PendingSignals    int             `json:"pendingSignals"`
	NextRunAt         *time.Time      `json:"nextRunAt,omitempty"`
	CreatedAt         time.Time       `json:"createdAt"`
	UpdatedAt         time.Time       `json:"updatedAt"`
}

type WorkflowEvent struct {
	WorkflowID string          `json:"workflowId,omitempty"`
	Sequence   int             `json:"sequence"`
	EventType  string          `json:"eventType"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"createdAt"`
}

type WorkflowTask struct {
	ID             int64           `json:"id"`
	WorkflowID     string          `json:"workflowId"`
	StepIndex      int             `json:"stepIndex"`
	StepName       string          `json:"stepName"`
	ActivityName   string          `json:"activityName"`
	Status         string          `json:"status"`
	Attempts       int             `json:"attempts"`
	MaxAttempts    int             `json:"maxAttempts"`
	RunAt          time.Time       `json:"runAt"`
	LastError      string          `json:"lastError,omitempty"`
	LeaseOwner     string          `json:"leaseOwner,omitempty"`
	LeaseExpiresAt *time.Time      `json:"leaseExpiresAt,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	State          json.RawMessage `json:"-"`
}

type WorkflowSignal struct {
	ID          int64           `json:"id"`
	WorkflowID  string          `json:"workflowId"`
	SignalName  string          `json:"signalName"`
	Payload     json.RawMessage `json:"payload"`
	Status      string          `json:"status"`
	CreatedAt   time.Time       `json:"createdAt"`
	ProcessedAt *time.Time      `json:"processedAt,omitempty"`
}

type SignalWorkflowInput struct {
	Name    string          `json:"name"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type WorkflowReplay struct {
	WorkflowID         string          `json:"workflowId"`
	Status             string          `json:"status"`
	CurrentStepName    string          `json:"currentStepName,omitempty"`
	CurrentActivity    string          `json:"currentActivity,omitempty"`
	LastEventSequence  int             `json:"lastEventSequence"`
	LastError          string          `json:"lastError,omitempty"`
	LastOutput         json.RawMessage `json:"lastOutput,omitempty"`
	Context            json.RawMessage `json:"context,omitempty"`
	EventCount         int             `json:"eventCount"`
	WorkflowDefinition string          `json:"definitionId,omitempty"`
}

type ActivityDescriptor struct {
	Name          string         `json:"name"`
	DisplayName   string         `json:"displayName,omitempty"`
	Description   string         `json:"description"`
	Category      string         `json:"category"`
	Status        string         `json:"status,omitempty"`
	Tags          []string       `json:"tags,omitempty"`
	ExampleInput  map[string]any `json:"exampleInput,omitempty"`
	ExampleOutput map[string]any `json:"exampleOutput,omitempty"`
}
