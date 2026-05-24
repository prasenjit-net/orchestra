export interface HealthResponse {
  status: string
  service: string
  env: string
  time: string
  version: {
    version: string
    commit: string
    buildDate: string
  }
  documents: string[]
}

export interface ExampleResponse {
  title: string
  summary: string
  features: string[]
  quickstart: string[]
  repository: string
  frontendDir: string
}

export interface MetaResponse {
  name: string
  description: string
  environment: string
  url: string
  uiProxy: string
  version: {
    version: string
    commit: string
    buildDate: string
  }
}

export interface WorkflowActivity {
  name: string
  displayName?: string
  description: string
  category: string
  status?: string
  tags?: string[]
  exampleInput?: unknown
  exampleOutput?: Record<string, unknown>
}

export interface WorkflowActivitiesResponse {
  activities: WorkflowActivity[]
}

export interface RetryPolicy {
  maxAttempts: number
  backoffSeconds: number
}

export interface WorkflowStepLayout {
  x: number
  y: number
}

export interface WorkflowTransitionCondition {
  path: string
  operator: string
  value?: unknown
}

export interface WorkflowStepTransition {
  to: string
  label?: string
  condition?: WorkflowTransitionCondition
}

export interface WorkflowStepDefinition {
  name: string
  activity: string
  input?: unknown
  retry?: RetryPolicy
  layout?: WorkflowStepLayout
  transitions?: WorkflowStepTransition[]
}

export interface WorkflowDefinitionDocument {
  name: string
  description: string
  steps: WorkflowStepDefinition[]
}

export interface WorkflowDefinitionSummary {
  id: string
  name: string
  description: string
  status: string
  activeVersion: number
  latestVersion: number
  draftVersion?: number
  createdAt: string
  updatedAt: string
}

export interface WorkflowDefinitionVersionSummary {
  version: number
  status: string
  createdAt: string
  updatedAt: string
  publishedAt?: string
}

export interface WorkflowDefinitionDetails extends WorkflowDefinitionSummary {
  document: WorkflowDefinitionDocument
  versions: WorkflowDefinitionVersionSummary[]
}

export interface WorkflowDefinitionsResponse {
  definitions: WorkflowDefinitionSummary[]
}

export interface WorkflowInstance {
  id: string
  definitionId: string
  definitionVersion: number
  status: string
  currentStepIndex: number
  currentStepName: string
  currentActivity: string
  lastEventSequence: number
  lastError?: string
  lastOutput?: unknown
  context?: Record<string, unknown>
  pendingSignals: number
  nextRunAt?: string
  createdAt: string
  updatedAt: string
}

export interface WorkflowsResponse {
  workflows: WorkflowInstance[]
}

export interface WorkflowEvent {
  workflowId?: string
  sequence: number
  eventType: string
  payload: unknown
  createdAt: string
}

export interface WorkflowHistoryResponse {
  events: WorkflowEvent[]
}

export interface WorkflowOperationsResponse {
  events: WorkflowEvent[]
}

export interface WorkflowTask {
  id: number
  workflowId: string
  stepIndex: number
  stepName: string
  activityName: string
  status: string
  attempts: number
  maxAttempts: number
  runAt: string
  lastError?: string
  leaseOwner?: string
  leaseExpiresAt?: string
  createdAt: string
  updatedAt: string
}

export interface WorkflowTasksResponse {
  tasks: WorkflowTask[]
}

export type WorkflowTaskAction = 'retry' | 'requeue' | 'pause' | 'resume' | 'cancel'

export interface WorkflowReplay {
  workflowId: string
  status: string
  currentStepName?: string
  currentActivity?: string
  lastEventSequence: number
  lastError?: string
  lastOutput?: unknown
  context?: Record<string, unknown>
  eventCount: number
  definitionId?: string
}

export interface WorkflowLiveEvent {
  type: string
  entity: string
  entityId?: string
  timestamp: string
  payload?: unknown
}

export type WorkflowLiveStatus = 'connecting' | 'connected' | 'reconnecting' | 'disconnected'

export interface Script {
  id: string
  name: string
  description: string
  language: string
  source: string
  timeoutMs?: number
  exports?: string[]
  createdAt: string
  updatedAt: string
}

export interface CreateScriptInput {
  name: string
  description: string
  language: string
  source: string
  timeoutMs?: number
  exports?: string[]
}

export interface ScriptsResponse {
  scripts: Script[]
}

export interface Agent {
  id: string
  name: string
  description: string
  model: string
  systemPrompt: string
  maxTokens?: number
  temperature?: number
  mcpServerIds?: string[]
  createdAt: string
  updatedAt: string
}

export interface CreateAgentInput {
  name: string
  description: string
  model: string
  systemPrompt: string
  maxTokens?: number
  temperature?: number
}

export interface AgentsResponse {
  agents: Agent[]
}

export interface MCPTool {
  name: string
  description?: string
  inputSchema: Record<string, unknown>
}

export interface MCPServer {
  id: string
  name: string
  description: string
  group: string
  url: string
  headers?: Record<string, string>
  enabled: boolean
  tools?: MCPTool[]
  exploredAt?: string
  createdAt: string
  updatedAt: string
}

export interface CreateMCPServerInput {
  name: string
  description: string
  group: string
  url: string
  headers?: Record<string, string>
  enabled: boolean
}

export interface MCPServersResponse {
  servers: MCPServer[]
}
