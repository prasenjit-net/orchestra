import { useQuery } from '@tanstack/react-query'
import { Bot, Plus } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { agentsApi } from '../services/api'
import { formatDate } from './workflowUi'

export default function AgentsPage() {
  const navigate = useNavigate()

  const agentsQuery = useQuery({
    queryKey: ['agents'],
    queryFn: agentsApi.list,
  })

  if (agentsQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading agents…</div>
  }

  if (agentsQuery.error) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Unable to load agents.</div>
  }

  const agents = agentsQuery.data?.agents ?? []

  return (
    <div className="space-y-8 p-8">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-gray-900 dark:text-slate-100">Agents</h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-slate-400">
            AI agents powered by OpenAI that you can invoke as workflow steps.
          </p>
        </div>
        <button
          type="button"
          onClick={() => navigate('/agents/new')}
          className="inline-flex items-center gap-2 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700"
        >
          <Plus className="h-4 w-4" />
          New Agent
        </button>
      </div>

      {agents.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-gray-300 py-20 dark:border-slate-700">
          <Bot className="mb-4 h-10 w-10 text-gray-300 dark:text-slate-600" />
          <p className="text-sm font-medium text-gray-500 dark:text-slate-400">No agents yet</p>
          <p className="mt-1 text-xs text-gray-400 dark:text-slate-500">
            Create an agent to invoke AI models from workflow steps.
          </p>
          <button
            type="button"
            onClick={() => navigate('/agents/new')}
            className="mt-6 inline-flex items-center gap-2 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700"
          >
            <Plus className="h-4 w-4" />
            New Agent
          </button>
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {agents.map((agent) => (
            <button
              key={agent.id}
              type="button"
              onClick={() => navigate(`/agents/${agent.id}/editor`)}
              className="group flex flex-col rounded-2xl border border-gray-200 bg-white p-5 text-left shadow-sm transition-shadow hover:shadow-md dark:border-slate-800 dark:bg-slate-900"
            >
              <div className="flex items-start justify-between gap-3">
                <p className="font-semibold text-gray-900 group-hover:text-primary-600 dark:text-slate-100 dark:group-hover:text-primary-400">
                  {agent.name}
                </p>
                <span className="shrink-0 rounded-full bg-emerald-100 px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">
                  {agent.model}
                </span>
              </div>
              {agent.description ? (
                <p className="mt-2 line-clamp-2 text-sm text-gray-500 dark:text-slate-400">{agent.description}</p>
              ) : null}
              <p className="mt-auto pt-4 text-xs text-gray-400 dark:text-slate-500">Updated {formatDate(agent.updatedAt)}</p>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
