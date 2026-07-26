import type {
  Agent,
  AgentsResponse,
  APIKeyGrant,
  APIKeyRecord,
  APIKeysResponse,
  APIKeySecret,
  AuditEventsResponse,
  AuthSession,
  AuthUser,
  ClusterNode,
  NodeHealthResult,
  CreateAgentInput,
  CreateMCPServerInput,
  CreateScriptInput,
  CreateJSONSchemaInput,
  ImportAnalysis,
  ImportBundle,
  JSONSchemaDocument,
  JSONSchemasResponse,
  MCPServer,
  MCPServersResponse,
  ExampleResponse,
  HealthResponse,
  MetaResponse,
  Script,
  ScriptsResponse,
  SessionResponse,
  UserRole,
  UsersResponse,
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

let csrfToken = ''

export class ApiError extends Error {
  status: number
  code?: string

  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

export function setCSRFToken(token: string) {
  csrfToken = token
}

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
    let code: string | undefined
    try {
      const payload = await response.json()
      if (payload?.error) {
        message = payload.error
      }
      code = payload?.code
    } catch {
      // ignore invalid JSON
    }
    throw new ApiError(message, response.status, code)
  }

  return response.json() as Promise<T>
}

async function apiFetch(input: RequestInfo | URL, init: RequestInit = {}) {
  const method = (init.method || 'GET').toUpperCase()
  const headers = new Headers(init.headers)
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && csrfToken) {
    headers.set('X-CSRF-Token', csrfToken)
  }
  const response = await fetch(input, { ...init, headers, credentials: 'include' })
  if (response.status === 401) {
    window.dispatchEvent(new CustomEvent('orchestra:unauthorized'))
  }
  return response
}

export const authApi = {
  login: async (username: string, password: string) =>
    handleResponse<SessionResponse>(
      await apiFetch(buildApiUrl('/auth/login'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, password }),
      }),
    ),
  session: async () => handleResponse<SessionResponse>(await apiFetch(buildApiUrl('/auth/session'))),
  logout: async () =>
    handleResponse<{ status: string }>(await apiFetch(buildApiUrl('/auth/logout'), { method: 'POST' })),
  changePassword: async (currentPassword: string, newPassword: string) =>
    handleResponse<SessionResponse>(
      await apiFetch(buildApiUrl('/auth/change-password'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ currentPassword, newPassword }),
      }),
    ),
  sessions: async () =>
    handleResponse<{ sessions: AuthSession[] }>(await apiFetch(buildApiUrl('/auth/sessions'))),
  revokeSession: async (id: string) =>
    handleResponse<{ status: string }>(
      await apiFetch(buildApiUrl(`/auth/sessions/${id}`), { method: 'DELETE' }),
    ),
}

export const usersApi = {
  list: async (search = '') => {
    const query = search ? `?search=${encodeURIComponent(search)}` : ''
    return handleResponse<UsersResponse>(await apiFetch(buildApiUrl(`/users${query}`)))
  },
  get: async (id: string) =>
    handleResponse<{ user: AuthUser; effectivePermissions: string[] }>(
      await apiFetch(buildApiUrl(`/users/${id}/`)),
    ),
  create: async (input: { username: string; displayName: string; role: UserRole; password?: string }) =>
    handleResponse<{ user: AuthUser; temporaryPassword?: string }>(
      await apiFetch(buildApiUrl('/users'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    ),
  update: async (id: string, input: { username: string; displayName: string; role: UserRole; status: string }) =>
    handleResponse<AuthUser>(
      await apiFetch(buildApiUrl(`/users/${id}/`), {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    ),
  replaceEntitlements: async (id: string, entitlements: { permission: string; effect: 'allow' | 'deny' }[]) =>
    handleResponse<AuthUser>(
      await apiFetch(buildApiUrl(`/users/${id}/entitlements`), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ entitlements }),
      }),
    ),
  resetPassword: async (id: string) =>
    handleResponse<{ temporaryPassword: string }>(
      await apiFetch(buildApiUrl(`/users/${id}/reset-password`), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: '{}',
      }),
    ),
  permissions: async () =>
    handleResponse<{ permissions: string[] }>(await apiFetch(buildApiUrl('/permissions'))),
}

export interface APIKeyInput {
  name: string
  description: string
  expiresAt?: string
  grants: APIKeyGrant[]
}

export const apiKeysApi = {
  list: async () => handleResponse<APIKeysResponse>(await apiFetch(buildApiUrl('/api-keys'))),
  get: async (id: string) => handleResponse<APIKeyRecord>(await apiFetch(buildApiUrl(`/api-keys/${id}/`))),
  create: async (input: APIKeyInput) =>
    handleResponse<APIKeySecret>(
      await apiFetch(buildApiUrl('/api-keys'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    ),
  update: async (id: string, input: APIKeyInput) =>
    handleResponse<APIKeyRecord>(
      await apiFetch(buildApiUrl(`/api-keys/${id}/`), {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    ),
  rotate: async (id: string) =>
    handleResponse<APIKeySecret>(
      await apiFetch(buildApiUrl(`/api-keys/${id}/rotate`), { method: 'POST' }),
    ),
  revoke: async (id: string) =>
    handleResponse<{ status: string }>(
      await apiFetch(buildApiUrl(`/api-keys/${id}/revoke`), { method: 'POST' }),
    ),
}

export const auditApi = {
  list: async (filters?: { action?: string; outcome?: string }) => {
    const params = new URLSearchParams()
    if (filters?.action) params.set('action', filters.action)
    if (filters?.outcome) params.set('outcome', filters.outcome)
    const query = params.toString()
    return handleResponse<AuditEventsResponse>(
      await apiFetch(buildApiUrl(`/audit-events${query ? `?${query}` : ''}`)),
    )
  },
}

export const healthApi = {
  get: async () => handleResponse<HealthResponse>(await apiFetch(buildApiUrl('/health'))),
}

export const clusterApi = {
  listNodes: async () => handleResponse<ClusterNode[]>(await apiFetch(buildApiUrl('/nodes'))),
  checkHealth: async () =>
    handleResponse<NodeHealthResult[]>(
      await apiFetch(buildApiUrl('/nodes/healthcheck'), { method: 'POST' }),
    ),
}

export const aiApi = {
  enhancePrompt: async (prompt: string, provider: string, model: string) =>
    handleResponse<{ prompt: string }>(
      await apiFetch(buildApiUrl('/ai/enhance-prompt'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ prompt, provider, model }),
      }),
    ),
}

export const adminApi = {
  restart: async () =>
    handleResponse<{ status: string }>(
      await apiFetch(buildApiUrl('/admin/restart'), { method: 'POST' }),
    ),
}

export const configApi = {
  getRaw: async () =>
    handleResponse<{ path: string; content: string }>(await apiFetch(buildApiUrl('/config/raw'))),
  putRaw: async (content: string) =>
    handleResponse<{ path: string; status: string }>(
      await apiFetch(buildApiUrl('/config/raw'), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ content }),
      }),
    ),
}

export const exampleApi = {
  get: async () => handleResponse<ExampleResponse>(await apiFetch(buildApiUrl('/example'))),
}

export const metaApi = {
  get: async () => handleResponse<MetaResponse>(await apiFetch(buildApiUrl('/meta'))),
  getPublic: async () => handleResponse<MetaResponse>(await apiFetch(buildApiUrl('/meta/public'))),
}

export const scriptsApi = {
  list: async () => handleResponse<ScriptsResponse>(await apiFetch(buildApiUrl('/scripts'))),
  create: async (input: CreateScriptInput) =>
    handleResponse<Script>(
      await apiFetch(buildApiUrl('/scripts'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    ),
  get: async (id: string) => handleResponse<Script>(await apiFetch(buildApiUrl(`/scripts/${id}`))),
  update: async (id: string, input: CreateScriptInput) =>
    handleResponse<Script>(
      await apiFetch(buildApiUrl(`/scripts/${id}`), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    ),
  delete: async (id: string) => {
    const response = await apiFetch(buildApiUrl(`/scripts/${id}`), { method: 'DELETE' })
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

export const jsonSchemasApi = {
  list: async () => handleResponse<JSONSchemasResponse>(await apiFetch(buildApiUrl('/json-schemas'))),
  create: async (input: CreateJSONSchemaInput) =>
    handleResponse<JSONSchemaDocument>(
      await apiFetch(buildApiUrl('/json-schemas'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    ),
  get: async (id: string) => handleResponse<JSONSchemaDocument>(await apiFetch(buildApiUrl(`/json-schemas/${id}`))),
  update: async (id: string, input: CreateJSONSchemaInput) =>
    handleResponse<JSONSchemaDocument>(
      await apiFetch(buildApiUrl(`/json-schemas/${id}`), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    ),
  delete: async (id: string) => {
    const response = await apiFetch(buildApiUrl(`/json-schemas/${id}`), { method: 'DELETE' })
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
  list: async () => handleResponse<AgentsResponse>(await apiFetch(buildApiUrl('/agents'))),
  create: async (input: CreateAgentInput) =>
    handleResponse<Agent>(
      await apiFetch(buildApiUrl('/agents'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    ),
  get: async (id: string) => handleResponse<Agent>(await apiFetch(buildApiUrl(`/agents/${id}`))),
  update: async (id: string, input: CreateAgentInput) =>
    handleResponse<Agent>(
      await apiFetch(buildApiUrl(`/agents/${id}`), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    ),
  delete: async (id: string) => {
    const response = await apiFetch(buildApiUrl(`/agents/${id}`), { method: 'DELETE' })
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
    handleResponse<MCPServersResponse>(await apiFetch(buildApiUrl(`/agents/${id}/mcp-servers`))),
  setMCPServers: async (id: string, serverIds: string[]) => {
    const response = await apiFetch(buildApiUrl(`/agents/${id}/mcp-servers`), {
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
  list: async () => handleResponse<MCPServersResponse>(await apiFetch(buildApiUrl('/mcp-servers'))),
  create: async (input: CreateMCPServerInput) =>
    handleResponse<MCPServer>(
      await apiFetch(buildApiUrl('/mcp-servers'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    ),
  get: async (id: string) => handleResponse<MCPServer>(await apiFetch(buildApiUrl(`/mcp-servers/${id}`))),
  update: async (id: string, input: CreateMCPServerInput) =>
    handleResponse<MCPServer>(
      await apiFetch(buildApiUrl(`/mcp-servers/${id}`), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    ),
  delete: async (id: string) => {
    const response = await apiFetch(buildApiUrl(`/mcp-servers/${id}`), { method: 'DELETE' })
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
      await apiFetch(buildApiUrl(`/mcp-servers/${id}/explore`), { method: 'POST' }),
    ),
}

export const workflowApi = {
  listActivities: async () => handleResponse<WorkflowActivitiesResponse>(await apiFetch(buildApiUrl('/workflows/activities'))),
  listDefinitions: async () => handleResponse<WorkflowDefinitionsResponse>(await apiFetch(buildApiUrl('/workflow-definitions'))),
  createDefinition: async (payload: WorkflowDefinitionDocument) =>
    handleResponse<WorkflowDefinitionDetails>(
      await apiFetch(buildApiUrl('/workflow-definitions'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      }),
    ),
  getDefinition: async (definitionId: string) =>
    handleResponse<WorkflowDefinitionDetails>(await apiFetch(buildApiUrl(`/workflow-definitions/${definitionId}`))),
  getDefinitionVersion: async (definitionId: string, version: number) =>
    handleResponse<WorkflowDefinitionDetails>(await apiFetch(buildApiUrl(`/workflow-definitions/${definitionId}/versions/${version}`))),
  createDefinitionVersion: async (definitionId: string, payload: WorkflowDefinitionDocument, basedOnVersion: number) =>
    handleResponse<WorkflowDefinitionDetails>(
      await apiFetch(buildApiUrl(`/workflow-definitions/${definitionId}/versions`), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ...payload, basedOnVersion }),
      }),
    ),
  publishDefinitionVersion: async (definitionId: string, version: number, activate = false) =>
    handleResponse<WorkflowDefinitionDetails>(
      await apiFetch(buildApiUrl(`/workflow-definitions/${definitionId}/versions/${version}/publish`), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ activate }),
      }),
    ),
  activateDefinitionVersion: async (definitionId: string, version: number) =>
    handleResponse<WorkflowDefinitionDetails>(
      await apiFetch(buildApiUrl(`/workflow-definitions/${definitionId}/versions/${version}/activate`), {
        method: 'POST',
      }),
    ),
  startWorkflow: async (definitionId: string, body?: { input?: Record<string, unknown>; callbackUrl?: string; version?: number }) =>
    handleResponse<WorkflowInstance>(
      await apiFetch(buildApiUrl(`/workflow-definitions/${definitionId}/start`), {
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
    return handleResponse<WorkflowsResponse>(await apiFetch(buildApiUrl(query ? `/workflows?${query}` : '/workflows')))
  },
  listOperations: async (limit = 50, offset = 0) => {
    const response = await apiFetch(buildApiUrl(`/workflows/events?limit=${limit}&offset=${offset}`))
    if (response.status === 404) {
      return { events: [], total: 0, limit, offset } satisfies WorkflowOperationsResponse
    }
    return handleResponse<WorkflowOperationsResponse>(response)
  },
  getWorkflow: async (workflowId: string) => handleResponse<WorkflowInstance>(await apiFetch(buildApiUrl(`/workflows/${workflowId}`))),
  getWorkflowHistory: async (workflowId: string, limit?: number, offset?: number) => {
    const params = new URLSearchParams()
    if (limit) params.set('limit', String(limit))
    if (offset) params.set('offset', String(offset))
    const qs = params.toString()
    return handleResponse<WorkflowHistoryResponse>(
      await apiFetch(buildApiUrl(`/workflows/${workflowId}/history${qs ? '?' + qs : ''}`)),
    )
  },
  cancelWorkflow: async (workflowId: string) =>
    handleResponse<WorkflowInstance>(
      await apiFetch(buildApiUrl(`/workflows/${workflowId}/cancel`), {
        method: 'POST',
      }),
    ),
  signalWorkflow: async (workflowId: string, payload: { name: string; payload?: unknown }) =>
    handleResponse<WorkflowInstance>(
      await apiFetch(buildApiUrl(`/workflows/${workflowId}/signals`), {
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
    return handleResponse<WorkflowTasksResponse>(await apiFetch(buildApiUrl(query ? `/workflows/tasks?${query}` : '/workflows/tasks')))
  },
  applyTaskAction: async (taskId: number, action: WorkflowTaskAction) =>
    handleResponse<WorkflowTask>(
      await apiFetch(buildApiUrl(`/workflows/tasks/${taskId}/${action}`), {
        method: 'POST',
      }),
    ),
}

export const importExportApi = {
  exportWorkflow: async (id: string) =>
    handleResponse<ImportBundle>(await apiFetch(buildApiUrl(`/workflow-definitions/${id}/export`))),
  exportAgent: async (id: string) =>
    handleResponse<ImportBundle>(await apiFetch(buildApiUrl(`/agents/${id}/export`))),
  exportScript: async (id: string) =>
    handleResponse<ImportBundle>(await apiFetch(buildApiUrl(`/scripts/${id}/export`))),
  exportJSONSchemas: async () =>
    handleResponse<ImportBundle>(await apiFetch(buildApiUrl('/json-schemas/export'))),
  exportJSONSchema: async (id: string) =>
    handleResponse<ImportBundle>(await apiFetch(buildApiUrl(`/json-schemas/${id}/export`))),
  exportConnector: async (id: string) =>
    handleResponse<ImportBundle>(await apiFetch(buildApiUrl(`/mcp-servers/${id}/export`))),
  analyze: async (bundle: ImportBundle) =>
    handleResponse<ImportAnalysis>(
      await apiFetch(buildApiUrl('/import/analyze'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(bundle),
      }),
    ),
  apply: async (bundle: ImportBundle, overrideIds: string[]) =>
    handleResponse<{ imported: number }>(
      await apiFetch(buildApiUrl('/import/apply'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ bundle, overrideIds }),
      }),
    ),
}

export const scriptAiApi = {
  assist: async (messages: { role: string; content: string }[], currentScript?: string) =>
    handleResponse<{ content: string }>(
      await apiFetch(buildApiUrl('/ai/script-assist'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ messages, currentScript: currentScript ?? '' }),
      }),
    ),
  validate: async (source: string) =>
    handleResponse<{ valid: boolean; error?: string }>(
      await apiFetch(buildApiUrl('/ai/validate-script'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ source }),
      }),
    ),
}

export function downloadBundle(bundle: ImportBundle, name: string) {
  const slug = name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || bundle.bundleType
  const json = JSON.stringify(bundle, null, 2)
  const blob = new Blob([json], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${slug}.orchestra.json`
  a.click()
  URL.revokeObjectURL(url)
}
