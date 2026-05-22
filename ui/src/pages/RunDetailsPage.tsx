import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, History, ListChecks } from 'lucide-react'
import { Link, useParams } from 'react-router-dom'
import SectionHeader from '../components/SectionHeader'
import StatCard from '../components/StatCard'
import { workflowApi } from '../services/api'
import type { WorkflowTaskAction } from '../types'
import { EventCard, actionLabel, availableTaskActions, formatDate, statusClasses } from './workflowUi'

export default function RunDetailsPage() {
  const { workflowId = '' } = useParams()
  const queryClient = useQueryClient()

  const workflowQuery = useQuery({
    queryKey: ['workflow', workflowId],
    queryFn: () => workflowApi.getWorkflow(workflowId),
    enabled: Boolean(workflowId),
  })
  const historyQuery = useQuery({
    queryKey: ['workflow-history', workflowId],
    queryFn: () => workflowApi.getWorkflowHistory(workflowId),
    enabled: Boolean(workflowId),
  })
  const tasksQuery = useQuery({
    queryKey: ['workflow-tasks'],
    queryFn: workflowApi.listTasks,
  })

  const taskActionMutation = useMutation({
    mutationFn: ({ taskId, action }: { taskId: number; action: WorkflowTaskAction }) => workflowApi.applyTaskAction(taskId, action),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['workflow-tasks'] }),
        queryClient.invalidateQueries({ queryKey: ['workflow', workflowId] }),
        queryClient.invalidateQueries({ queryKey: ['workflow-history', workflowId] }),
        queryClient.invalidateQueries({ queryKey: ['workflow-operations'] }),
        queryClient.invalidateQueries({ queryKey: ['workflows'] }),
      ])
    },
  })

  if (workflowQuery.isLoading || historyQuery.isLoading || tasksQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading run details…</div>
  }

  if (workflowQuery.error || historyQuery.error || tasksQuery.error || !workflowQuery.data) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Unable to load run details.</div>
  }

  const workflow = workflowQuery.data
  const history = historyQuery.data?.events ?? []
  const tasks = (tasksQuery.data?.tasks ?? []).filter((task) => task.workflowId === workflow.id)

  return (
    <div className="space-y-8 p-8">
      <SectionHeader
        title="Run details"
        description="Inspect workflow state, task queue activity, and the durable event history for one run."
        action={
          <div className="flex gap-3">
            <Link
              to="/runs"
              className="rounded-lg border border-gray-200 px-4 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
            >
              All runs
            </Link>
            <Link
              to="/queues"
              className="rounded-lg border border-gray-200 px-4 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
            >
              Queue view
            </Link>
          </div>
        }
      />

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Status" value={workflow.status} description="Current workflow state." icon={Activity} tone="bg-sky-50 text-sky-600 dark:bg-sky-900/20 dark:text-sky-300" />
        <StatCard label="Version" value={`v${workflow.definitionVersion}`} description="Definition version pinned at start." icon={History} tone="bg-violet-50 text-violet-600 dark:bg-violet-900/20 dark:text-violet-300" />
        <StatCard label="Tasks" value={String(tasks.length)} description="Tasks associated with this workflow." icon={ListChecks} tone="bg-amber-50 text-amber-600 dark:bg-amber-900/20 dark:text-amber-300" />
        <StatCard label="Events" value={String(history.length)} description="Durable history entries recorded so far." icon={History} tone="bg-emerald-50 text-emerald-600 dark:bg-emerald-900/20 dark:text-emerald-300" />
      </div>

      <section className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <div className="flex flex-wrap items-center gap-3">
          <div className="font-semibold text-gray-900 dark:text-slate-100">{workflow.id}</div>
          <span className={`inline-flex rounded-full px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide ${statusClasses(workflow.status)}`}>{workflow.status}</span>
        </div>
        <div className="mt-4 grid grid-cols-1 gap-3 text-sm text-gray-500 dark:text-slate-400 md:grid-cols-2 xl:grid-cols-4">
          <div>Definition: {workflow.definitionId}</div>
          <div>Current step: {workflow.currentStepName || 'completed'}</div>
          <div>Current activity: {workflow.currentActivity || '—'}</div>
          <div>Updated: {formatDate(workflow.updatedAt)}</div>
        </div>
        {workflow.lastError ? <div className="mt-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/40 dark:bg-red-950/30 dark:text-red-300">{workflow.lastError}</div> : null}
      </section>

      <section className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">Queued tasks</h2>
        <div className="mt-4 space-y-3">
          {tasks.length === 0 ? (
            <p className="text-sm text-gray-500 dark:text-slate-400">No tasks for this workflow.</p>
          ) : (
            tasks.map((task) => (
              <div key={task.id} className="rounded-lg border border-gray-200 p-4 dark:border-slate-800">
                <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
                  <div>
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-medium text-gray-900 dark:text-slate-100">{task.stepName}</span>
                      <span className={`inline-flex rounded-full px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide ${statusClasses(task.status)}`}>{task.status}</span>
                    </div>
                    <div className="mt-2 text-sm text-gray-500 dark:text-slate-400">
                      {task.activityName} · attempts {task.attempts}/{task.maxAttempts} · run at {formatDate(task.runAt)}
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

      <section className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">Event history</h2>
        <div className="mt-4 space-y-3">
          {history.length === 0 ? <p className="text-sm text-gray-500 dark:text-slate-400">No history yet.</p> : history.map((event) => <EventCard key={`${event.sequence}-${event.eventType}`} event={event} />)}
        </div>
      </section>
    </div>
  )
}
