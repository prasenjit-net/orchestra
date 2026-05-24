import { useQuery } from '@tanstack/react-query'
import { Plus, Server, Wrench } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { mcpServersApi } from '../services/api'
import { formatDate } from './workflowUi'

export default function ConnectorsPage() {
  const navigate = useNavigate()

  const serversQuery = useQuery({
    queryKey: ['connectors'],
    queryFn: mcpServersApi.list,
  })

  if (serversQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading connectors…</div>
  }

  if (serversQuery.error) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Unable to load connectors.</div>
  }

  const servers = serversQuery.data?.servers ?? []

  return (
    <div className="space-y-8 p-8">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-gray-900 dark:text-slate-100">Connectors</h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-slate-400">
            Reusable Model Context Protocol connectors you can attach to agents.
          </p>
        </div>
        <button
          type="button"
          onClick={() => navigate('/connectors/new')}
          className="inline-flex items-center gap-2 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700"
        >
          <Plus className="h-4 w-4" />
          New Connector
        </button>
      </div>

      {servers.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-gray-300 py-20 dark:border-slate-700">
          <Server className="mb-4 h-10 w-10 text-gray-300 dark:text-slate-600" />
          <p className="text-sm font-medium text-gray-500 dark:text-slate-400">No connectors yet</p>
          <p className="mt-1 text-xs text-gray-400 dark:text-slate-500">
            Add a connector to give agents access to external tools.
          </p>
          <button
            type="button"
            onClick={() => navigate('/connectors/new')}
            className="mt-6 inline-flex items-center gap-2 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700"
          >
            <Plus className="h-4 w-4" />
            New Connector
          </button>
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {servers.map((srv) => (
            <button
              key={srv.id}
              type="button"
              onClick={() => navigate(`/connectors/${srv.id}/editor`)}
              className="group flex flex-col rounded-2xl border border-gray-200 bg-white p-5 text-left shadow-sm transition-shadow hover:shadow-md dark:border-slate-800 dark:bg-slate-900"
            >
              <div className="flex items-start justify-between gap-3">
                <p className="font-semibold text-gray-900 group-hover:text-primary-600 dark:text-slate-100 dark:group-hover:text-primary-400">
                  {srv.name}
                </p>
                <div className="flex shrink-0 items-center gap-1.5">
                  {srv.group ? (
                    <span className="rounded-full bg-blue-100 px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-blue-700 dark:bg-blue-900/30 dark:text-blue-300">
                      {srv.group}
                    </span>
                  ) : null}
                  <span
                    className={`h-2 w-2 rounded-full ${srv.enabled ? 'bg-emerald-400' : 'bg-gray-300 dark:bg-slate-600'}`}
                    title={srv.enabled ? 'Enabled' : 'Disabled'}
                  />
                </div>
              </div>
              {srv.description ? (
                <p className="mt-2 line-clamp-2 text-sm text-gray-500 dark:text-slate-400">{srv.description}</p>
              ) : null}
              <p className="mt-2 truncate text-xs text-gray-400 dark:text-slate-500">{srv.url}</p>
              <div className="mt-auto flex items-center justify-between gap-2 pt-3">
                <p className="text-xs text-gray-400 dark:text-slate-500">Updated {formatDate(srv.updatedAt)}</p>
                {srv.tools && srv.tools.length > 0 && (
                  <span className="inline-flex items-center gap-1 text-[10px] text-gray-400 dark:text-slate-500">
                    <Wrench className="h-3 w-3" />
                    {srv.tools.length} {srv.tools.length === 1 ? 'tool' : 'tools'}
                  </span>
                )}
              </div>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
