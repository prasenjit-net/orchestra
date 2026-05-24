import { useState, useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, Plus, RefreshCw, Save, Trash2, Wrench, X } from 'lucide-react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { mcpServersApi } from '../services/api'
import type { CreateMCPServerInput } from '../types'
import { formatDate } from './workflowUi'

type HeaderRow = { id: string; key: string; value: string }

function makeHeaderRow(key = '', value = ''): HeaderRow {
  return { id: `${Date.now()}-${Math.random()}`, key, value }
}

export default function McpServerEditorPage() {
  const { serverId } = useParams<{ serverId: string }>()
  const isNew = !serverId || serverId === 'new'
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [group, setGroup] = useState('')
  const [url, setUrl] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [headerRows, setHeaderRows] = useState<HeaderRow[]>([])
  const [pageError, setPageError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)

  const serverQuery = useQuery({
    queryKey: ['mcp-server', serverId],
    queryFn: () => mcpServersApi.get(serverId!),
    enabled: !isNew,
  })

  useEffect(() => {
    if (serverQuery.data) {
      const s = serverQuery.data
      setName(s.name)
      setDescription(s.description)
      setGroup(s.group ?? '')
      setUrl(s.url)
      setEnabled(s.enabled)
      const rows = Object.entries(s.headers ?? {}).map(([k, v]) => makeHeaderRow(k, v))
      setHeaderRows(rows)
    }
  }, [serverQuery.data])

  const buildHeaders = (): Record<string, string> => {
    const result: Record<string, string> = {}
    for (const row of headerRows) {
      const k = row.key.trim()
      if (k) result[k] = row.value
    }
    return result
  }

  const buildInput = (): CreateMCPServerInput => ({
    name: name.trim(),
    description: description.trim(),
    group: group.trim(),
    url: url.trim(),
    enabled,
    headers: buildHeaders(),
  })

  const createMutation = useMutation({
    mutationFn: mcpServersApi.create,
    onSuccess: (srv) => {
      setPageError(null)
      setSaved(true)
      void queryClient.invalidateQueries({ queryKey: ['mcp-servers'] })
      navigate(`/mcp-servers/${srv.id}/editor`, { replace: true })
    },
    onError: (error: Error) => setPageError(error.message),
  })

  const updateMutation = useMutation({
    mutationFn: (input: CreateMCPServerInput) => mcpServersApi.update(serverId!, input),
    onSuccess: (srv) => {
      setPageError(null)
      setSaved(true)
      void queryClient.invalidateQueries({ queryKey: ['mcp-servers'] })
      void queryClient.setQueryData(['mcp-server', serverId], srv)
      setTimeout(() => setSaved(false), 2000)
    },
    onError: (error: Error) => setPageError(error.message),
  })

  const deleteMutation = useMutation({
    mutationFn: () => mcpServersApi.delete(serverId!),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['mcp-servers'] })
      navigate('/mcp-servers', { replace: true })
    },
    onError: (error: Error) => setPageError(error.message),
  })

  const exploreMutation = useMutation({
    mutationFn: () => mcpServersApi.explore(serverId!),
    onSuccess: (srv) => {
      setPageError(null)
      void queryClient.invalidateQueries({ queryKey: ['mcp-servers'] })
      void queryClient.setQueryData(['mcp-server', serverId], srv)
    },
    onError: (error: Error) => setPageError(`Explore failed: ${error.message}`),
  })

  const handleSave = () => {
    const input = buildInput()
    if (!input.name) { setPageError('Name is required.'); return }
    if (!input.url) { setPageError('URL is required.'); return }
    setPageError(null)
    if (isNew) createMutation.mutate(input)
    else updateMutation.mutate(input)
  }

  const handleDelete = () => {
    if (!window.confirm('Delete this MCP server? Agents that use it will lose access to its tools.')) return
    deleteMutation.mutate()
  }

  const addHeaderRow = () => setHeaderRows((rows) => [...rows, makeHeaderRow()])
  const removeHeaderRow = (id: string) => setHeaderRows((rows) => rows.filter((r) => r.id !== id))
  const updateHeaderRow = (id: string, field: 'key' | 'value', val: string) =>
    setHeaderRows((rows) => rows.map((r) => (r.id === id ? { ...r, [field]: val } : r)))

  if (!isNew && serverQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading MCP server…</div>
  }
  if (!isNew && serverQuery.error) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Could not load MCP server.</div>
  }

  const isSaving = createMutation.isPending || updateMutation.isPending
  const srv = serverQuery.data
  const tools = srv?.tools ?? []

  return (
    <div className="flex h-full flex-col overflow-hidden">
      {/* Header */}
      <div className="shrink-0 border-b border-gray-200 bg-white px-5 py-3 dark:border-slate-800 dark:bg-slate-900">
        <div className="flex items-center justify-between gap-4">
          <div className="flex items-center gap-3">
            <Link
              to="/mcp-servers"
              className="inline-flex items-center gap-1.5 text-sm font-medium text-gray-500 transition-colors hover:text-gray-900 dark:text-slate-400 dark:hover:text-slate-100"
            >
              <ArrowLeft className="h-4 w-4" />
              MCP Servers
            </Link>
            <span className="text-gray-300 dark:text-slate-700">/</span>
            <span className="text-sm font-semibold text-gray-900 dark:text-slate-100">
              {name || (isNew ? 'New MCP Server' : '…')}
            </span>
          </div>
          <div className="flex items-center gap-2">
            {saved && <span className="text-xs font-medium text-emerald-600 dark:text-emerald-400">Saved</span>}
            {pageError && <span className="max-w-xs truncate text-xs font-medium text-red-600 dark:text-red-400">{pageError}</span>}
            {!isNew && (
              <button
                type="button"
                onClick={handleDelete}
                disabled={deleteMutation.isPending}
                className="inline-flex items-center gap-1.5 rounded-lg border border-red-200 px-3 py-2 text-sm font-semibold text-red-700 transition-colors hover:bg-red-50 disabled:opacity-50 dark:border-red-900/40 dark:text-red-300 dark:hover:bg-red-950/20"
              >
                <Trash2 className="h-4 w-4" />
                Delete
              </button>
            )}
            <button
              type="button"
              onClick={handleSave}
              disabled={isSaving}
              className="inline-flex items-center gap-1.5 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700 disabled:opacity-60"
            >
              <Save className="h-4 w-4" />
              {isSaving ? 'Saving…' : 'Save'}
            </button>
          </div>
        </div>
      </div>

      {/* Metadata bar */}
      <div className="shrink-0 border-b border-gray-200 bg-gray-50 px-5 py-2.5 dark:border-slate-800 dark:bg-slate-950">
        <div className="flex flex-wrap items-center gap-4">
          <div className="flex items-center gap-2">
            <label className="shrink-0 text-[11px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Name</label>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-mcp-server"
              className="rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
            />
          </div>
          <div className="flex flex-1 items-center gap-2">
            <label className="shrink-0 text-[11px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Description</label>
            <input
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="What does this server expose?"
              className="w-full rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
            />
          </div>
          {group ? (
            <span className="rounded-full bg-blue-100 px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-blue-700 dark:bg-blue-900/30 dark:text-blue-300">
              {group}
            </span>
          ) : null}
        </div>
      </div>

      {/* Canvas */}
      <div className="flex flex-1 overflow-hidden">
        {/* Left panel */}
        <div className="flex w-72 shrink-0 flex-col gap-4 overflow-y-auto border-r border-gray-200 bg-white p-5 dark:border-slate-800 dark:bg-slate-900">
          <div>
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Group</label>
            <input
              value={group}
              onChange={(e) => setGroup(e.target.value)}
              placeholder="e.g. data, search, devtools"
              className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
            />
            <p className="mt-1 text-[11px] text-gray-400 dark:text-slate-500">Free-text label for grouping servers in the list.</p>
          </div>
          <div>
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Enabled</label>
            <label className="flex cursor-pointer items-center gap-2">
              <input
                type="checkbox"
                checked={enabled}
                onChange={(e) => setEnabled(e.target.checked)}
                className="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
              />
              <span className="text-sm text-gray-700 dark:text-slate-300">{enabled ? 'Active' : 'Disabled'}</span>
            </label>
            <p className="mt-1 text-[11px] text-gray-400 dark:text-slate-500">Disabled servers are skipped at runtime even if attached to an agent.</p>
          </div>
          <div className="rounded-lg border border-dashed border-gray-300 p-3 text-[11px] text-gray-500 dark:border-slate-700 dark:text-slate-400">
            Attach this server to an agent via the Agent editor. Reference id:{' '}
            <span className="font-mono font-semibold">{isNew ? '<id after save>' : serverId}</span>
          </div>

          {/* Discovered Tools */}
          {!isNew && (
            <div>
              <div className="mb-1.5 flex items-center justify-between">
                <label className="text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">
                  Discovered Tools
                  {tools.length > 0 && (
                    <span className="ml-1.5 rounded-full bg-primary-100 px-1.5 py-0.5 text-[10px] text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">
                      {tools.length}
                    </span>
                  )}
                </label>
                <button
                  type="button"
                  onClick={() => exploreMutation.mutate()}
                  disabled={exploreMutation.isPending}
                  className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-[11px] font-semibold text-primary-600 transition-colors hover:bg-primary-50 disabled:opacity-50 dark:text-primary-400 dark:hover:bg-primary-950/20"
                >
                  <RefreshCw className={`h-3 w-3 ${exploreMutation.isPending ? 'animate-spin' : ''}`} />
                  {exploreMutation.isPending ? 'Exploring…' : 'Re-explore'}
                </button>
              </div>
              {srv?.exploredAt ? (
                <p className="mb-2 text-[10px] text-gray-400 dark:text-slate-500">Last explored {formatDate(srv.exploredAt)}</p>
              ) : (
                <p className="mb-2 text-[10px] text-gray-400 dark:text-slate-500">Not yet explored.</p>
              )}
              {tools.length === 0 ? (
                <p className="text-[11px] text-gray-400 dark:text-slate-500">
                  No tools found. Save the server and click Re-explore to discover tools.
                </p>
              ) : (
                <div className="space-y-1.5">
                  {tools.map((tool) => (
                    <div
                      key={tool.name}
                      className="rounded-lg border border-gray-100 bg-gray-50 px-3 py-2 dark:border-slate-700 dark:bg-slate-800"
                    >
                      <div className="flex items-center gap-1.5">
                        <Wrench className="h-3 w-3 shrink-0 text-gray-400 dark:text-slate-500" />
                        <span className="font-mono text-[11px] font-semibold text-gray-800 dark:text-slate-200">{tool.name}</span>
                      </div>
                      {tool.description && (
                        <p className="mt-0.5 pl-4 text-[10px] text-gray-500 dark:text-slate-400">{tool.description}</p>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>

        {/* Right panel */}
        <div className="flex flex-1 flex-col gap-5 overflow-y-auto p-5">
          <div>
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">URL (SSE endpoint)</label>
            <input
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://my-mcp-server.example.com/sse"
              className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
            />
            <p className="mt-1 text-[11px] text-gray-400 dark:text-slate-500">The MCP server SSE endpoint. The agent activity will GET this URL with <span className="font-mono">Accept: text/event-stream</span>.</p>
          </div>

          <div>
            <div className="mb-1.5 flex items-center justify-between">
              <label className="text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Request Headers</label>
              <button
                type="button"
                onClick={addHeaderRow}
                className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-[11px] font-semibold text-primary-600 transition-colors hover:bg-primary-50 dark:text-primary-400 dark:hover:bg-primary-950/20"
              >
                <Plus className="h-3 w-3" />
                Add
              </button>
            </div>
            {headerRows.length === 0 ? (
              <p className="text-[11px] text-gray-400 dark:text-slate-500">No custom headers. Click Add to set authorization or other headers.</p>
            ) : (
              <div className="space-y-2">
                {headerRows.map((row) => (
                  <div key={row.id} className="flex items-center gap-2">
                    <input
                      value={row.key}
                      onChange={(e) => updateHeaderRow(row.id, 'key', e.target.value)}
                      placeholder="Header name"
                      className="w-2/5 rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                    />
                    <input
                      value={row.value}
                      onChange={(e) => updateHeaderRow(row.id, 'value', e.target.value)}
                      placeholder="Value"
                      className="flex-1 rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                    />
                    <button
                      type="button"
                      onClick={() => removeHeaderRow(row.id)}
                      className="shrink-0 rounded-md p-1.5 text-gray-400 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-950/20 dark:hover:text-red-400"
                    >
                      <X className="h-4 w-4" />
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
