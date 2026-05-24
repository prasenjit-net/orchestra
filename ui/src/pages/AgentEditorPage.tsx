import { useState, useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, Save, Trash2 } from 'lucide-react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { agentsApi, mcpServersApi } from '../services/api'
import type { CreateAgentInput } from '../types'

export default function AgentEditorPage() {
  const { agentId } = useParams<{ agentId: string }>()
  const isNew = !agentId || agentId === 'new'
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [model, setModel] = useState('gpt-4o')
  const [systemPrompt, setSystemPrompt] = useState('')
  const [maxTokens, setMaxTokens] = useState('')
  const [temperature, setTemperature] = useState('')
  const [pageError, setPageError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [checkedMCPIds, setCheckedMCPIds] = useState<Set<string>>(new Set())

  const agentQuery = useQuery({
    queryKey: ['agent', agentId],
    queryFn: () => agentsApi.get(agentId!),
    enabled: !isNew,
  })

  const allMCPServersQuery = useQuery({
    queryKey: ['mcp-servers'],
    queryFn: mcpServersApi.list,
  })

  const agentMCPQuery = useQuery({
    queryKey: ['agent-mcp-servers', agentId],
    queryFn: () => agentsApi.getMCPServers(agentId!),
    enabled: !isNew,
  })

  useEffect(() => {
    if (agentQuery.data) {
      const a = agentQuery.data
      setName(a.name)
      setDescription(a.description)
      setModel(a.model)
      setSystemPrompt(a.systemPrompt)
      setMaxTokens(a.maxTokens ? String(a.maxTokens) : '')
      setTemperature(a.temperature ? String(a.temperature) : '')
    }
  }, [agentQuery.data])

  useEffect(() => {
    if (agentMCPQuery.data) {
      setCheckedMCPIds(new Set(agentMCPQuery.data.servers.map((s) => s.id)))
    }
  }, [agentMCPQuery.data])

  const buildInput = (): CreateAgentInput => ({
    name: name.trim(),
    description: description.trim(),
    model: model.trim() || 'gpt-4o',
    systemPrompt: systemPrompt.trim(),
    maxTokens: maxTokens ? parseInt(maxTokens, 10) : 0,
    temperature: temperature ? parseFloat(temperature) : 0,
  })

  const createMutation = useMutation({
    mutationFn: agentsApi.create,
    onSuccess: (agent) => {
      setPageError(null)
      setSaved(true)
      void queryClient.invalidateQueries({ queryKey: ['agents'] })
      navigate(`/agents/${agent.id}/editor`, { replace: true })
    },
    onError: (error: Error) => setPageError(error.message),
  })

  const updateMutation = useMutation({
    mutationFn: (input: CreateAgentInput) => agentsApi.update(agentId!, input),
    onSuccess: async (agent) => {
      await agentsApi.setMCPServers(agent.id, [...checkedMCPIds])
      setPageError(null)
      setSaved(true)
      void queryClient.invalidateQueries({ queryKey: ['agents'] })
      void queryClient.invalidateQueries({ queryKey: ['agent-mcp-servers', agentId] })
      void queryClient.setQueryData(['agent', agentId], agent)
      setTimeout(() => setSaved(false), 2000)
    },
    onError: (error: Error) => setPageError(error.message),
  })

  const deleteMutation = useMutation({
    mutationFn: () => agentsApi.delete(agentId!),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['agents'] })
      navigate('/agents', { replace: true })
    },
    onError: (error: Error) => setPageError(error.message),
  })

  const handleSave = () => {
    const input = buildInput()
    if (!input.name) { setPageError('Name is required.'); return }
    setPageError(null)
    if (isNew) createMutation.mutate(input)
    else updateMutation.mutate(input)
  }

  const toggleMCPServer = (id: string) => {
    setCheckedMCPIds((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const handleDelete = () => {
    if (!window.confirm('Delete this agent? Workflow steps that reference it will fail at runtime.')) return
    deleteMutation.mutate()
  }

  if (!isNew && agentQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading agent…</div>
  }
  if (!isNew && agentQuery.error) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Could not load agent.</div>
  }

  const isSaving = createMutation.isPending || updateMutation.isPending

  return (
    <div className="flex h-full flex-col overflow-hidden">
      {/* Header bar */}
      <div className="shrink-0 border-b border-gray-200 bg-white px-5 py-3 dark:border-slate-800 dark:bg-slate-900">
        <div className="flex items-center justify-between gap-4">
          <div className="flex items-center gap-3">
            <Link
              to="/agents"
              className="inline-flex items-center gap-1.5 text-sm font-medium text-gray-500 transition-colors hover:text-gray-900 dark:text-slate-400 dark:hover:text-slate-100"
            >
              <ArrowLeft className="h-4 w-4" />
              Agents
            </Link>
            <span className="text-gray-300 dark:text-slate-700">/</span>
            <span className="text-sm font-semibold text-gray-900 dark:text-slate-100">{name || (isNew ? 'New Agent' : '…')}</span>
          </div>
          <div className="flex items-center gap-2">
            {saved && <span className="text-xs font-medium text-emerald-600 dark:text-emerald-400">Saved</span>}
            {pageError && <span className="text-xs font-medium text-red-600 dark:text-red-400">{pageError}</span>}
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
              placeholder="my-agent"
              className="rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
            />
          </div>
          <div className="flex flex-1 items-center gap-2">
            <label className="shrink-0 text-[11px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Description</label>
            <input
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="What does this agent do?"
              className="w-full rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
            />
          </div>
          <span className="rounded-full bg-emerald-100 px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">
            {model}
          </span>
        </div>
      </div>

      {/* Canvas */}
      <div className="flex flex-1 overflow-hidden">
        {/* Left panel */}
        <div className="flex w-80 shrink-0 flex-col gap-4 overflow-y-auto border-r border-gray-200 bg-white p-5 dark:border-slate-800 dark:bg-slate-900">
          <div>
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Model</label>
            <input
              value={model}
              onChange={(e) => setModel(e.target.value || 'gpt-4o')}
              placeholder="gpt-4o"
              className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
            />
            <p className="mt-1 text-[11px] text-gray-400 dark:text-slate-500">e.g. gpt-4o, gpt-4o-mini, o1-preview</p>
          </div>
          <div>
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Temperature</label>
            <input
              type="number"
              min={0}
              max={2}
              step={0.1}
              value={temperature}
              onChange={(e) => setTemperature(e.target.value)}
              placeholder="0"
              className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
            />
            <p className="mt-1 text-[11px] text-gray-400 dark:text-slate-500">0 = deterministic, 2 = very creative. Blank uses model default.</p>
          </div>
          <div>
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Max Tokens</label>
            <input
              type="number"
              min={1}
              value={maxTokens}
              onChange={(e) => setMaxTokens(e.target.value)}
              placeholder="0"
              className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
            />
            <p className="mt-1 text-[11px] text-gray-400 dark:text-slate-500">0 or blank = model default.</p>
          </div>
          <div className="rounded-lg border border-dashed border-gray-300 p-3 text-[11px] text-gray-500 dark:border-slate-700 dark:text-slate-400">
            Reference this agent in a workflow step by setting{' '}
            <span className="font-mono">agentId</span> to{' '}
            <span className="font-mono font-semibold">{isNew ? '<id after save>' : agentId}</span>.
          </div>
        </div>

        {/* Right panel */}
        <div className="flex flex-1 flex-col gap-4 overflow-y-auto p-5">
          <div className="flex flex-1 flex-col">
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">System Prompt</label>
            <textarea
              value={systemPrompt}
              onChange={(e) => setSystemPrompt(e.target.value)}
              placeholder="You are a helpful assistant. Respond concisely and accurately."
              spellCheck={false}
              className="min-h-[200px] flex-1 resize-none rounded-lg border border-gray-200 bg-white p-4 text-sm leading-relaxed text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
            />
          </div>

          {/* MCP Servers */}
          <div>
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">
              Attached MCP Servers
            </label>
            {isNew ? (
              <p className="text-[11px] text-gray-400 dark:text-slate-500">Save the agent first to attach MCP servers.</p>
            ) : allMCPServersQuery.isLoading ? (
              <p className="text-[11px] text-gray-400 dark:text-slate-500">Loading…</p>
            ) : (allMCPServersQuery.data?.servers ?? []).length === 0 ? (
              <p className="text-[11px] text-gray-400 dark:text-slate-500">
                No MCP servers defined yet.{' '}
                <a href="/mcp-servers/new" className="text-primary-600 underline dark:text-primary-400">Add one</a>.
              </p>
            ) : (
              <div className="space-y-1 rounded-lg border border-gray-200 p-3 dark:border-slate-700">
                {(allMCPServersQuery.data?.servers ?? []).map((srv) => (
                  <label key={srv.id} className="flex cursor-pointer items-center gap-3 rounded-md px-2 py-1.5 transition-colors hover:bg-gray-50 dark:hover:bg-slate-800">
                    <input
                      type="checkbox"
                      checked={checkedMCPIds.has(srv.id)}
                      onChange={() => toggleMCPServer(srv.id)}
                      className="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                    />
                    <span className="flex-1 text-sm text-gray-800 dark:text-slate-200">{srv.name}</span>
                    <span className="truncate text-xs text-gray-400 dark:text-slate-500">{srv.url}</span>
                    {srv.group ? (
                      <span className="shrink-0 rounded-full bg-blue-100 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-blue-700 dark:bg-blue-900/30 dark:text-blue-300">
                        {srv.group}
                      </span>
                    ) : null}
                    {!srv.enabled ? (
                      <span className="shrink-0 rounded-full bg-gray-100 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-gray-500 dark:bg-slate-800 dark:text-slate-400">
                        disabled
                      </span>
                    ) : null}
                    {srv.tools && srv.tools.length > 0 ? (
                      <span className="shrink-0 text-[10px] text-gray-400 dark:text-slate-500">
                        {srv.tools.length} tools
                      </span>
                    ) : null}
                  </label>
                ))}
              </div>
            )}
            <p className="mt-1 text-[11px] text-gray-400 dark:text-slate-500">
              Checked servers will have their tools available to this agent at runtime.
            </p>
          </div>
        </div>
      </div>
    </div>
  )
}
