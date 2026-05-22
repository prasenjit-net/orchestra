import type {
  ExampleResponse,
  HealthResponse,
  MetaResponse,
  WorkflowActivitiesResponse,
  WorkflowDefinitionDetails,
  WorkflowDefinitionDocument,
  WorkflowDefinitionsResponse,
  WorkflowHistoryResponse,
  WorkflowOperationsResponse,
  WorkflowReplay,
  WorkflowInstance,
  WorkflowTask,
  WorkflowTaskAction,
  WorkflowTasksResponse,
  WorkflowsResponse,
} from '../types'

export const API_BASE = import.meta.env.VITE_API_BASE || '/api'

function buildApiUrl(path: string) {
  return `${API_BASE}${path.startsWith('/') ? path : `/${path}`}`
}

export function buildWebSocketUrl() {
  const base = API_BASE.startsWith('http') ? new URL(API_BASE) : new URL(API_BASE, window.location.origin)
  base.protocol = base.protocol === 'https:' ? 'wss:' : 'ws:'
  base.pathname = `${base.pathname.replace(/\/$/, '')}/ws`
  base.search = ''
  base.hash = ''
  return base.toString()
}

async function handleResponse<T>(response: Response): Promise<T> {
  if (!response.ok) {
    let message = `HTTP ${response.status}`
    try {
      const payload = await response.json()
      if (payload?.error) {
        message = payload.error
      }
    } catch {
      // ignore invalid JSON
    }
    throw new Error(message)
  }

  return response.json() as Promise<T>
}

export const healthApi = {
  get: async () => handleResponse<HealthResponse>(await fetch(buildApiUrl('/health'))),
}

export const exampleApi = {
  get: async () => handleResponse<ExampleResponse>(await fetch(buildApiUrl('/example'))),
}

export const metaApi = {
  get: async () => handleResponse<MetaResponse>(await fetch(buildApiUrl('/meta'))),
}

export const workflowApi = {
  listActivities: async () => handleResponse<WorkflowActivitiesResponse>(await fetch(buildApiUrl('/workflows/activities'))),
  listDefinitions: async () => handleResponse<WorkflowDefinitionsResponse>(await fetch(buildApiUrl('/workflow-definitions'))),
  createDefinition: async (payload: WorkflowDefinitionDocument) =>
    handleResponse<WorkflowDefinitionDetails>(
      await fetch(buildApiUrl('/workflow-definitions'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      }),
    ),
  getDefinition: async (definitionId: string) =>
    handleResponse<WorkflowDefinitionDetails>(await fetch(buildApiUrl(`/workflow-definitions/${definitionId}`))),
  createDefinitionVersion: async (definitionId: string, payload: WorkflowDefinitionDocument) =>
    handleResponse<WorkflowDefinitionDetails>(
      await fetch(buildApiUrl(`/workflow-definitions/${definitionId}/versions`), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      }),
    ),
  publishDefinitionVersion: async (definitionId: string, version: number) =>
    handleResponse<WorkflowDefinitionDetails>(
      await fetch(buildApiUrl(`/workflow-definitions/${definitionId}/versions/${version}/publish`), {
        method: 'POST',
      }),
    ),
  startWorkflow: async (definitionId: string) =>
    handleResponse<WorkflowInstance>(
      await fetch(buildApiUrl(`/workflow-definitions/${definitionId}/start`), {
        method: 'POST',
      }),
    ),
  listWorkflows: async () => handleResponse<WorkflowsResponse>(await fetch(buildApiUrl('/workflows'))),
  listOperations: async (limit = 50) => {
    const response = await fetch(buildApiUrl(`/workflows/events?limit=${limit}`))
    if (response.status === 404) {
      return { events: [] } satisfies WorkflowOperationsResponse
    }
    return handleResponse<WorkflowOperationsResponse>(response)
  },
  getWorkflow: async (workflowId: string) => handleResponse<WorkflowInstance>(await fetch(buildApiUrl(`/workflows/${workflowId}`))),
  getWorkflowHistory: async (workflowId: string) =>
    handleResponse<WorkflowHistoryResponse>(await fetch(buildApiUrl(`/workflows/${workflowId}/history`))),
  cancelWorkflow: async (workflowId: string) =>
    handleResponse<WorkflowInstance>(
      await fetch(buildApiUrl(`/workflows/${workflowId}/cancel`), {
        method: 'POST',
      }),
    ),
  signalWorkflow: async (workflowId: string, payload: { name: string; payload?: unknown }) =>
    handleResponse<WorkflowInstance>(
      await fetch(buildApiUrl(`/workflows/${workflowId}/signals`), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      }),
    ),
  replayWorkflow: async (workflowId: string) =>
    handleResponse<WorkflowReplay>(await fetch(buildApiUrl(`/workflows/${workflowId}/replay`))),
  listTasks: async () => handleResponse<WorkflowTasksResponse>(await fetch(buildApiUrl('/workflows/tasks'))),
  applyTaskAction: async (taskId: number, action: WorkflowTaskAction) =>
    handleResponse<WorkflowTask>(
      await fetch(buildApiUrl(`/workflows/tasks/${taskId}/${action}`), {
        method: 'POST',
      }),
    ),
}
