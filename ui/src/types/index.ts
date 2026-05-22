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
  description: string
  category: string
  exampleInput?: unknown
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

export interface WorkflowStepDefinition {
  name: string
  activity: string
  input?: unknown
  retry?: RetryPolicy
  layout?: WorkflowStepLayout
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

export interface WorkflowLiveEvent {
  type: string
  entity: string
  entityId?: string
  timestamp: string
  payload?: unknown
}

export type WorkflowLiveStatus = 'connecting' | 'connected' | 'reconnecting' | 'disconnected'
