import type {
  Agent,
  AgentsResponse,
  CreateAgentInput,
  CreateMCPServerInput,
  CreateScriptInput,
  MCPServer,
  MCPServersResponse,
  ExampleResponse,
  HealthResponse,
  MetaResponse,
  Script,
  ScriptsResponse,
  WorkflowActivitiesResponse,
  WorkflowDefinitionDetails,
  WorkflowDefinitionDocument,
  WorkflowDefinitionsResponse,
  WorkflowHistoryResponse,
  WorkflowOperationsResponse,
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

export const aiApi = {
  enhancePrompt: async (prompt: string) =>
    handleResponse<{ prompt: string }>(
      await fetch(buildApiUrl('/ai/enhance-prompt'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ prompt }),
      }),
    ),
}

export const adminApi = {
  restart: async () =>
    handleResponse<{ status: string }>(
      await fetch(buildApiUrl('/admin/restart'), { method: 'POST' }),
    ),
}

export const configApi = {
  getRaw: async () =>
    handleResponse<{ path: string; content: string }>(await fetch(buildApiUrl('/config/raw'))),
  putRaw: async (content: string) =>
    handleResponse<{ path: string; status: string }>(
      await fetch(buildApiUrl('/config/raw'), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ content }),
      }),
    ),
}

export const exampleApi = {
  get: async () => handleResponse<ExampleResponse>(await fetch(buildApiUrl('/example'))),
}

export const metaApi = {
  get: async () => handleResponse<MetaResponse>(await fetch(buildApiUrl('/meta'))),
}

export const scriptsApi = {
  list: async () => handleResponse<ScriptsResponse>(await fetch(buildApiUrl('/scripts'))),
  create: async (input: CreateScriptInput) =>
    handleResponse<Script>(
      await fetch(buildApiUrl('/scripts'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    ),
  get: async (id: string) => handleResponse<Script>(await fetch(buildApiUrl(`/scripts/${id}`))),
  update: async (id: string, input: CreateScriptInput) =>
    handleResponse<Script>(
      await fetch(buildApiUrl(`/scripts/${id}`), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    ),
  delete: async (id: string) => {
    const response = await fetch(buildApiUrl(`/scripts/${id}`), { method: 'DELETE' })
    if (!response.ok) {
      let message = `HTTP ${response.status}`
      try {
        const payload = await response.json()
        if (payload?.error) message = payload.error
      } catch { /* ignore */ }
      throw new Error(message)
    }
  },
}

export const agentsApi = {
  list: async () => handleResponse<AgentsResponse>(await fetch(buildApiUrl('/agents'))),
  create: async (input: CreateAgentInput) =>
    handleResponse<Agent>(
      await fetch(buildApiUrl('/agents'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    ),
  get: async (id: string) => handleResponse<Agent>(await fetch(buildApiUrl(`/agents/${id}`))),
  update: async (id: string, input: CreateAgentInput) =>
    handleResponse<Agent>(
      await fetch(buildApiUrl(`/agents/${id}`), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    ),
  delete: async (id: string) => {
    const response = await fetch(buildApiUrl(`/agents/${id}`), { method: 'DELETE' })
    if (!response.ok) {
      let message = `HTTP ${response.status}`
      try {
        const payload = await response.json()
        if (payload?.error) message = payload.error
      } catch { /* ignore */ }
      throw new Error(message)
    }
  },
  getMCPServers: async (id: string) =>
    handleResponse<MCPServersResponse>(await fetch(buildApiUrl(`/agents/${id}/mcp-servers`))),
  setMCPServers: async (id: string, serverIds: string[]) => {
    const response = await fetch(buildApiUrl(`/agents/${id}/mcp-servers`), {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ serverIds }),
    })
    if (!response.ok) {
      let message = `HTTP ${response.status}`
      try {
        const payload = await response.json()
        if (payload?.error) message = payload.error
      } catch { /* ignore */ }
      throw new Error(message)
    }
  },
}

export const mcpServersApi = {
  list: async () => handleResponse<MCPServersResponse>(await fetch(buildApiUrl('/mcp-servers'))),
  create: async (input: CreateMCPServerInput) =>
    handleResponse<MCPServer>(
      await fetch(buildApiUrl('/mcp-servers'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    ),
  get: async (id: string) => handleResponse<MCPServer>(await fetch(buildApiUrl(`/mcp-servers/${id}`))),
  update: async (id: string, input: CreateMCPServerInput) =>
    handleResponse<MCPServer>(
      await fetch(buildApiUrl(`/mcp-servers/${id}`), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    ),
  delete: async (id: string) => {
    const response = await fetch(buildApiUrl(`/mcp-servers/${id}`), { method: 'DELETE' })
    if (!response.ok) {
      let message = `HTTP ${response.status}`
      try {
        const payload = await response.json()
        if (payload?.error) message = payload.error
      } catch { /* ignore */ }
      throw new Error(message)
    }
  },
  explore: async (id: string) =>
    handleResponse<MCPServer>(
      await fetch(buildApiUrl(`/mcp-servers/${id}/explore`), { method: 'POST' }),
    ),
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
  startWorkflow: async (definitionId: string, body?: { input?: Record<string, unknown>; callbackUrl?: string }) =>
    handleResponse<WorkflowInstance>(
      await fetch(buildApiUrl(`/workflow-definitions/${definitionId}/start`), {
        method: 'POST',
        headers: body ? { 'Content-Type': 'application/json' } : undefined,
        body: body ? JSON.stringify(body) : undefined,
      }),
    ),
  listWorkflows: async (params?: { limit?: number; offset?: number; status?: string; currentActivities?: string[] }) => {
    const qs = new URLSearchParams()
    if (params?.limit) qs.set('limit', String(params.limit))
    if (params?.offset) qs.set('offset', String(params.offset))
    if (params?.status) qs.set('status', params.status)
    if (params?.currentActivities?.length) qs.set('currentActivities', params.currentActivities.join(','))
    const query = qs.toString()
    return handleResponse<WorkflowsResponse>(await fetch(buildApiUrl(query ? `/workflows?${query}` : '/workflows')))
  },
  listOperations: async (limit = 50, offset = 0) => {
    const response = await fetch(buildApiUrl(`/workflows/events?limit=${limit}&offset=${offset}`))
    if (response.status === 404) {
      return { events: [], total: 0, limit, offset } satisfies WorkflowOperationsResponse
    }
    return handleResponse<WorkflowOperationsResponse>(response)
  },
  getWorkflow: async (workflowId: string) => handleResponse<WorkflowInstance>(await fetch(buildApiUrl(`/workflows/${workflowId}`))),
  getWorkflowHistory: async (workflowId: string, limit?: number, offset?: number) => {
    const params = new URLSearchParams()
    if (limit) params.set('limit', String(limit))
    if (offset) params.set('offset', String(offset))
    const qs = params.toString()
    return handleResponse<WorkflowHistoryResponse>(
      await fetch(buildApiUrl(`/workflows/${workflowId}/history${qs ? '?' + qs : ''}`)),
    )
  },
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
  listTasks: async (params?: { limit?: number; offset?: number; status?: string; excludeCompleted?: boolean }) => {
    const qs = new URLSearchParams()
    if (params?.limit) qs.set('limit', String(params.limit))
    if (params?.offset) qs.set('offset', String(params.offset))
    if (params?.status) qs.set('status', params.status)
    if (params?.excludeCompleted) qs.set('excludeCompleted', 'true')
    const query = qs.toString()
    return handleResponse<WorkflowTasksResponse>(await fetch(buildApiUrl(query ? `/workflows/tasks?${query}` : '/workflows/tasks')))
  },
  applyTaskAction: async (taskId: number, action: WorkflowTaskAction) =>
    handleResponse<WorkflowTask>(
      await fetch(buildApiUrl(`/workflows/tasks/${taskId}/${action}`), {
        method: 'POST',
      }),
    ),
}
