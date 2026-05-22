import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Clock3, PauseCircle, Play, RotateCcw } from 'lucide-react'
import { Link } from 'react-router-dom'
import SectionHeader from '../components/SectionHeader'
import StatCard from '../components/StatCard'
import { workflowApi } from '../services/api'
import type { WorkflowTaskAction } from '../types'
import { actionLabel, availableTaskActions, formatDate, statusClasses } from './workflowUi'

export default function QueuesPage() {
  const queryClient = useQueryClient()
  const tasksQuery = useQuery({
    queryKey: ['workflow-tasks'],
    queryFn: workflowApi.listTasks,
  })

  const taskActionMutation = useMutation({
    mutationFn: ({ taskId, action }: { taskId: number; action: WorkflowTaskAction }) => workflowApi.applyTaskAction(taskId, action),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['workflow-tasks'] }),
        queryClient.invalidateQueries({ queryKey: ['workflows'] }),
        queryClient.invalidateQueries({ queryKey: ['workflow-operations'] }),
      ])
    },
  })

  if (tasksQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading queue…</div>
  }

  if (tasksQuery.error) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Unable to load workflow queue.</div>
  }

  const tasks = tasksQuery.data?.tasks ?? []

  return (
    <div className="space-y-8 p-8">
      <SectionHeader
        title="Queues"
        description="Operate the durable task queue with pause, resume, retry, requeue, and cancel controls."
        action={
          <Link
            to="/operations"
            className="rounded-lg border border-gray-200 px-4 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
          >
            Live operations
          </Link>
        }
      />

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Pending" value={String(tasks.filter((task) => task.status === 'pending').length)} description="Tasks waiting to be claimed." icon={Clock3} tone="bg-amber-50 text-amber-600 dark:bg-amber-900/20 dark:text-amber-300" />
        <StatCard label="Running" value={String(tasks.filter((task) => task.status === 'running').length)} description="Tasks currently leased by a worker." icon={Play} tone="bg-sky-50 text-sky-600 dark:bg-sky-900/20 dark:text-sky-300" />
        <StatCard label="Paused" value={String(tasks.filter((task) => task.status === 'paused').length)} description="Tasks paused by an operator." icon={PauseCircle} tone="bg-violet-50 text-violet-600 dark:bg-violet-900/20 dark:text-violet-300" />
        <StatCard label="Failed" value={String(tasks.filter((task) => task.status === 'failed').length)} description="Failed tasks awaiting action." icon={RotateCcw} tone="bg-red-50 text-red-600 dark:bg-red-900/20 dark:text-red-300" />
      </div>

      <section className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">Task queue</h2>
        <div className="mt-4 space-y-3">
          {tasks.length === 0 ? (
            <p className="text-sm text-gray-500 dark:text-slate-400">No queued tasks.</p>
          ) : (
            tasks.map((task) => (
              <div key={task.id} className="rounded-lg border border-gray-200 p-4 dark:border-slate-800">
                <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-medium text-gray-900 dark:text-slate-100">{task.stepName}</span>
                      <span className={`inline-flex rounded-full px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide ${statusClasses(task.status)}`}>{task.status}</span>
                      <Link to={`/runs/${task.workflowId}`} className="text-xs font-semibold text-primary-600 hover:text-primary-700 dark:text-primary-300 dark:hover:text-primary-200">
                        {task.workflowId}
                      </Link>
                    </div>
                    <div className="mt-2 grid grid-cols-1 gap-2 text-sm text-gray-500 dark:text-slate-400 md:grid-cols-2 xl:grid-cols-4">
                      <div>Activity: {task.activityName}</div>
                      <div>Attempts: {task.attempts}/{task.maxAttempts}</div>
                      <div>Run at: {formatDate(task.runAt)}</div>
                      <div>Updated: {formatDate(task.updatedAt)}</div>
                    </div>
                    {task.lastError ? <div className="mt-2 text-sm text-red-600 dark:text-red-300">{task.lastError}</div> : null}
                  </div>
                  <div className="flex flex-wrap gap-2">
                    {availableTaskActions(task).map((action) => (
                      <button
                        key={action}
                        type="button"
                        onClick={() => taskActionMutation.mutate({ taskId: task.id, action })}
                        disabled={taskActionMutation.isPending}
                        className="rounded-lg border border-gray-200 px-3 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-60 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
                      >
                        {actionLabel(action)}
                      </button>
                    ))}
                  </div>
                </div>
              </div>
            ))
          )}
        </div>
      </section>
    </div>
  )
}
