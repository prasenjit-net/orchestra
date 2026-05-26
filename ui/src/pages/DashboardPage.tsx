import { Link } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Activity, AlertTriangle, BookOpen, CheckCircle2, Clock, Layers, PlayCircle, RotateCcw, Wifi } from 'lucide-react'
import { healthApi, metaApi, workflowApi } from '../services/api'
import { statusClasses, formatDate, availableTaskActions } from './workflowUi'
import type { WorkflowTask } from '../types'

function StatTile({
  label,
  value,
  icon: Icon,
  tone,
  to,
}: {
  label: string
  value: number | string
  icon: typeof Activity
  tone: string
  to?: string
}) {
  const inner = (
    <div className={`rounded-xl border bg-white p-5 shadow-sm transition-colors dark:bg-slate-900 ${tone}`}>
      <div className="flex items-center justify-between">
        <p className="text-sm font-medium text-gray-500 dark:text-slate-400">{label}</p>
        <Icon className="h-4 w-4 text-gray-400 dark:text-slate-500" />
      </div>
      <p className="mt-3 text-3xl font-bold tabular-nums text-gray-900 dark:text-slate-100">{value}</p>
    </div>
  )
  return to ? <Link to={to}>{inner}</Link> : inner
}

function RelativeTime({ value }: { value?: string }) {
  if (!value) return <span className="text-gray-400">—</span>
  const diff = Date.now() - new Date(value).getTime()
  const mins = Math.floor(diff / 60_000)
  const hours = Math.floor(diff / 3_600_000)
  const days = Math.floor(diff / 86_400_000)
  let label: string
  if (mins < 1) label = 'just now'
  else if (mins < 60) label = `${mins}m ago`
  else if (hours < 24) label = `${hours}h ago`
  else label = `${days}d ago`
  return <span title={formatDate(value)}>{label}</span>
}

function TaskRow({ task, onAction }: { task: WorkflowTask; onAction: (id: number, action: string) => void }) {
  const actions = availableTaskActions(task)
  return (
    <div className="flex items-center gap-3 py-3 text-sm">
      <span className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-semibold ${statusClasses(task.status)}`}>
        {task.status}
      </span>
      <div className="min-w-0 flex-1">
        <Link to={`/runs/${task.workflowId}`} className="font-medium text-gray-900 hover:underline dark:text-slate-100">
          {task.stepName}
        </Link>
        <span className="ml-2 text-xs text-gray-500 dark:text-slate-400">{task.activityName}</span>
        {task.lastError && (
          <p className="mt-0.5 truncate text-xs text-red-600 dark:text-red-400">{task.lastError}</p>
        )}
      </div>
      <span className="shrink-0 text-xs text-gray-400 dark:text-slate-500">
        <RelativeTime value={task.updatedAt} />
      </span>
      <div className="flex shrink-0 gap-1">
        {actions.slice(0, 2).map((action) => (
          <button
            key={action}
            onClick={() => onAction(task.id, action)}
            className="rounded border border-gray-200 px-2 py-0.5 text-xs font-medium text-gray-600 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-300 dark:hover:bg-slate-800"
          >
            {action}
          </button>
        ))}
      </div>
    </div>
  )
}

export default function DashboardPage() {
  const queryClient = useQueryClient()

  const healthQuery = useQuery({ queryKey: ['health'], queryFn: healthApi.get, refetchInterval: 30_000 })
  const metaQuery = useQuery({ queryKey: ['meta'], queryFn: metaApi.get, staleTime: 60_000 })
  const definitionsQuery = useQuery({ queryKey: ['workflow-definitions'], queryFn: workflowApi.listDefinitions, refetchInterval: 30_000 })
  const runningQuery = useQuery({
    queryKey: ['workflows', 'running'],
    queryFn: () => workflowApi.listWorkflows({ status: 'running', limit: 5 }),
    refetchInterval: 10_000,
  })
  const waitingQuery = useQuery({
    queryKey: ['workflows', 'waiting'],
    queryFn: () => workflowApi.listWorkflows({ status: 'waiting', limit: 5 }),
    refetchInterval: 10_000,
  })
  const recentQuery = useQuery({
    queryKey: ['workflows', 'recent'],
    queryFn: () => workflowApi.listWorkflows({ limit: 8 }),
    refetchInterval: 15_000,
  })
  const failedTasksQuery = useQuery({
    queryKey: ['workflow-tasks', 'failed'],
    queryFn: () => workflowApi.listTasks({ status: 'failed', limit: 10 }),
    refetchInterval: 15_000,
  })

  const taskActionMutation = useMutation({
    mutationFn: ({ id, action }: { id: number; action: string }) =>
      workflowApi.applyTaskAction(id, action as 'retry' | 'requeue' | 'pause' | 'resume' | 'cancel'),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['workflow-tasks'] })
      void queryClient.invalidateQueries({ queryKey: ['workflows'] })
    },
  })

  const definitions = definitionsQuery.data?.definitions ?? []
  const publishedDefs = definitions.filter((d) => d.status === 'published').length
  const runningRuns = runningQuery.data?.total ?? 0
  const waitingRuns = waitingQuery.data?.total ?? 0
  const failedTasks = failedTasksQuery.data?.total ?? 0
  const recentRuns = recentQuery.data?.workflows ?? []
  const failedTaskList = failedTasksQuery.data?.tasks ?? []
  const runningList = runningQuery.data?.workflows ?? []
  const waitingList = waitingQuery.data?.workflows ?? []
  const health = healthQuery.data

  return (
    <div className="space-y-8 p-8">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-gray-900 dark:text-slate-100">Dashboard</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-slate-400">
            {metaQuery.data ? `${metaQuery.data.name} · ${metaQuery.data.environment}` : 'Orchestra'}
          </p>
        </div>
        <div className="flex items-center gap-2 text-sm">
          {health ? (
            <span className={`flex items-center gap-1.5 font-medium ${health.status === 'ok' ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'}`}>
              <Wifi className="h-4 w-4" />
              {health.status === 'ok' ? 'Healthy' : health.status}
            </span>
          ) : (
            <span className="text-gray-400 dark:text-slate-500">Connecting…</span>
          )}
        </div>
      </div>

      {/* Stat tiles */}
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatTile
          label="Definitions"
          value={publishedDefs}
          icon={BookOpen}
          tone="border-gray-200 dark:border-slate-800"
          to="/workflows"
        />
        <StatTile
          label="Active runs"
          value={runningRuns}
          icon={PlayCircle}
          tone={runningRuns > 0 ? 'border-sky-200 dark:border-sky-800' : 'border-gray-200 dark:border-slate-800'}
          to="/runs"
        />
        <StatTile
          label="Waiting"
          value={waitingRuns}
          icon={Clock}
          tone={waitingRuns > 0 ? 'border-amber-200 dark:border-amber-800' : 'border-gray-200 dark:border-slate-800'}
          to="/runs"
        />
        <StatTile
          label="Failed tasks"
          value={failedTasks}
          icon={AlertTriangle}
          tone={failedTasks > 0 ? 'border-red-200 dark:border-red-900' : 'border-gray-200 dark:border-slate-800'}
          to="/queues"
        />
      </div>

      <div className="grid grid-cols-1 gap-6 xl:grid-cols-2">
        {/* Active runs */}
        <section className="rounded-xl border border-gray-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
          <div className="flex items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-slate-800">
            <h2 className="text-sm font-semibold text-gray-900 dark:text-slate-100">Active runs</h2>
            <Link to="/runs" className="text-xs font-medium text-primary-600 hover:underline dark:text-primary-400">
              View all
            </Link>
          </div>
          <div className="divide-y divide-gray-100 px-5 dark:divide-slate-800">
            {[...runningList, ...waitingList].length === 0 ? (
              <p className="py-8 text-center text-sm text-gray-400 dark:text-slate-500">No active runs</p>
            ) : (
              [...runningList, ...waitingList].slice(0, 6).map((run) => (
                <div key={run.id} className="flex items-center gap-3 py-3 text-sm">
                  <span className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-semibold ${statusClasses(run.status)}`}>
                    {run.status}
                  </span>
                  <div className="min-w-0 flex-1">
                    <Link to={`/runs/${run.id}`} className="font-medium text-gray-900 hover:underline dark:text-slate-100">
                      {run.currentStepName || '—'}
                    </Link>
                    <span className="ml-2 text-xs text-gray-500 dark:text-slate-400">{run.currentActivity}</span>
                  </div>
                  <span className="shrink-0 text-xs text-gray-400 dark:text-slate-500">
                    <RelativeTime value={run.updatedAt} />
                  </span>
                  <Link
                    to={`/runs/${run.id}`}
                    className="shrink-0 rounded border border-gray-200 px-2 py-0.5 text-xs font-medium text-gray-600 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-300 dark:hover:bg-slate-800"
                  >
                    Inspect
                  </Link>
                </div>
              ))
            )}
          </div>
        </section>

        {/* Failed tasks */}
        <section className="rounded-xl border border-gray-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
          <div className="flex items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-slate-800">
            <h2 className="text-sm font-semibold text-gray-900 dark:text-slate-100">
              Failed tasks
              {failedTasks > 0 && (
                <span className="ml-2 rounded-full bg-red-100 px-2 py-0.5 text-xs font-semibold text-red-700 dark:bg-red-900/30 dark:text-red-300">
                  {failedTasks}
                </span>
              )}
            </h2>
            <Link to="/queues" className="text-xs font-medium text-primary-600 hover:underline dark:text-primary-400">
              View all
            </Link>
          </div>
          <div className="divide-y divide-gray-100 px-5 dark:divide-slate-800">
            {failedTaskList.length === 0 ? (
              <div className="flex flex-col items-center gap-2 py-8">
                <CheckCircle2 className="h-6 w-6 text-emerald-400" />
                <p className="text-sm text-gray-400 dark:text-slate-500">No failed tasks</p>
              </div>
            ) : (
              failedTaskList.map((task) => (
                <TaskRow
                  key={task.id}
                  task={task}
                  onAction={(id, action) => taskActionMutation.mutate({ id, action })}
                />
              ))
            )}
          </div>
        </section>
      </div>

      {/* Recent runs */}
      <section className="rounded-xl border border-gray-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <div className="flex items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-slate-800">
          <h2 className="text-sm font-semibold text-gray-900 dark:text-slate-100">Recent runs</h2>
          <Link to="/runs" className="text-xs font-medium text-primary-600 hover:underline dark:text-primary-400">
            View all
          </Link>
        </div>
        {recentRuns.length === 0 ? (
          <div className="flex flex-col items-center gap-3 py-12">
            <Layers className="h-8 w-8 text-gray-300 dark:text-slate-600" />
            <p className="text-sm text-gray-400 dark:text-slate-500">No runs yet</p>
            <Link
              to="/workflows"
              className="rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700"
            >
              Go to workflows
            </Link>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-gray-100 text-left text-xs font-medium uppercase tracking-wide text-gray-500 dark:border-slate-800 dark:text-slate-400">
                  <th className="px-5 py-3">Run ID</th>
                  <th className="px-5 py-3">Status</th>
                  <th className="px-5 py-3">Current step</th>
                  <th className="px-5 py-3">Activity</th>
                  <th className="px-5 py-3">Source</th>
                  <th className="px-5 py-3">Updated</th>
                  <th className="px-5 py-3" />
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-slate-800">
                {recentRuns.map((run) => (
                  <tr key={run.id} className="hover:bg-gray-50 dark:hover:bg-slate-800/50">
                    <td className="px-5 py-3 font-mono text-xs text-gray-500 dark:text-slate-400">
                      {run.id.slice(0, 8)}…
                    </td>
                    <td className="px-5 py-3">
                      <span className={`rounded-full px-2 py-0.5 text-xs font-semibold ${statusClasses(run.status)}`}>
                        {run.status}
                      </span>
                    </td>
                    <td className="px-5 py-3 font-medium text-gray-900 dark:text-slate-100">
                      {run.currentStepName || '—'}
                    </td>
                    <td className="px-5 py-3 text-gray-500 dark:text-slate-400">{run.currentActivity || '—'}</td>
                    <td className="px-5 py-3">
                      {run.triggerSource === 'webhook' ? (
                        <span className="rounded-full bg-violet-100 px-2 py-0.5 text-xs font-semibold text-violet-700 dark:bg-violet-900/30 dark:text-violet-300">
                          webhook
                        </span>
                      ) : (
                        <span className="text-xs text-gray-400 dark:text-slate-500">{run.triggerSource ?? 'ui'}</span>
                      )}
                    </td>
                    <td className="px-5 py-3 text-xs text-gray-400 dark:text-slate-500">
                      <RelativeTime value={run.updatedAt} />
                    </td>
                    <td className="px-5 py-3 text-right">
                      <Link
                        to={`/runs/${run.id}`}
                        className="rounded border border-gray-200 px-2 py-1 text-xs font-medium text-gray-600 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-300 dark:hover:bg-slate-800"
                      >
                        Inspect
                      </Link>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {/* Workflow definitions quick list */}
      {definitions.length > 0 && (
        <section className="rounded-xl border border-gray-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
          <div className="flex items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-slate-800">
            <h2 className="text-sm font-semibold text-gray-900 dark:text-slate-100">Workflow definitions</h2>
            <Link to="/workflows" className="text-xs font-medium text-primary-600 hover:underline dark:text-primary-400">
              Manage
            </Link>
          </div>
          <div className="grid grid-cols-1 gap-px bg-gray-100 dark:bg-slate-800 sm:grid-cols-2 xl:grid-cols-3">
            {definitions.slice(0, 6).map((def) => (
              <div key={def.id} className="bg-white px-5 py-4 dark:bg-slate-900">
                <div className="flex items-start justify-between gap-2">
                  <Link
                    to={`/workflows/${def.id}/designer`}
                    className="font-medium text-gray-900 hover:underline dark:text-slate-100"
                  >
                    {def.name}
                  </Link>
                  <span className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-semibold ${statusClasses(def.status)}`}>
                    {def.status}
                  </span>
                </div>
                {def.description && (
                  <p className="mt-1 line-clamp-1 text-xs text-gray-500 dark:text-slate-400">{def.description}</p>
                )}
                <div className="mt-2 flex items-center gap-3 text-xs text-gray-400 dark:text-slate-500">
                  <span className="flex items-center gap-1">
                    <RotateCcw className="h-3 w-3" />
                    v{def.activeVersion}
                  </span>
                  <span>Updated <RelativeTime value={def.updatedAt} /></span>
                </div>
              </div>
            ))}
          </div>
        </section>
      )}

      {/* Health details footer */}
      {health && (
        <div className="flex flex-wrap items-center gap-x-6 gap-y-1 text-xs text-gray-400 dark:text-slate-500">
          <span className="flex items-center gap-1.5">
            <Activity className="h-3.5 w-3.5" />
            Server {health.status}
          </span>
          {metaQuery.data && (
            <>
              <span>v{metaQuery.data.version.version}</span>
              <span>{metaQuery.data.version.commit?.slice(0, 7)}</span>
              <span>{metaQuery.data.environment}</span>
            </>
          )}
        </div>
      )}
    </div>
  )
}
