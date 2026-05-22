import { useQuery } from '@tanstack/react-query'
import { Activity, Clock3, Play, Workflow } from 'lucide-react'
import { Link } from 'react-router-dom'
import SectionHeader from '../components/SectionHeader'
import StatCard from '../components/StatCard'
import { workflowApi } from '../services/api'
import { formatDate, statusClasses } from './workflowUi'

export default function RunsPage() {
  const workflowsQuery = useQuery({
    queryKey: ['workflows'],
    queryFn: workflowApi.listWorkflows,
  })
  const tasksQuery = useQuery({
    queryKey: ['workflow-tasks'],
    queryFn: workflowApi.listTasks,
  })

  if (workflowsQuery.isLoading || tasksQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading runs…</div>
  }

  if (workflowsQuery.error || tasksQuery.error) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Unable to load workflow runs.</div>
  }

  const workflows = workflowsQuery.data?.workflows ?? []
  const tasks = tasksQuery.data?.tasks ?? []

  return (
    <div className="space-y-8 p-8">
      <SectionHeader
        title="Runs"
        description="Track live and historical workflow instances, then drill into run history and queue state."
        action={
          <Link
            to="/workflows"
            className="rounded-lg border border-gray-200 px-4 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
          >
            Browse definitions
          </Link>
        }
      />

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Runs" value={String(workflows.length)} description="Workflow instances in durable storage." icon={Workflow} tone="bg-sky-50 text-sky-600 dark:bg-sky-900/20 dark:text-sky-300" />
        <StatCard label="Running" value={String(workflows.filter((workflow) => workflow.status === 'running').length)} description="Instances currently executing." icon={Play} tone="bg-emerald-50 text-emerald-600 dark:bg-emerald-900/20 dark:text-emerald-300" />
        <StatCard label="Failed" value={String(workflows.filter((workflow) => workflow.status === 'failed').length)} description="Runs that need operator attention." icon={Activity} tone="bg-red-50 text-red-600 dark:bg-red-900/20 dark:text-red-300" />
        <StatCard label="Queued tasks" value={String(tasks.filter((task) => task.status === 'pending' || task.status === 'running' || task.status === 'paused').length)} description="Pending, running, or paused tasks across all runs." icon={Clock3} tone="bg-amber-50 text-amber-600 dark:bg-amber-900/20 dark:text-amber-300" />
      </div>

      <section className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">Workflow instances</h2>
        <div className="mt-4 space-y-3">
          {workflows.length === 0 ? (
            <p className="text-sm text-gray-500 dark:text-slate-400">No workflow runs yet.</p>
          ) : (
            workflows.map((workflow) => (
              <div key={workflow.id} className="flex flex-col gap-3 rounded-lg border border-gray-200 p-4 dark:border-slate-800 md:flex-row md:items-center md:justify-between">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <div className="truncate font-medium text-gray-900 dark:text-slate-100">{workflow.id}</div>
                    <span className={`inline-flex rounded-full px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide ${statusClasses(workflow.status)}`}>{workflow.status}</span>
                  </div>
                  <div className="mt-2 grid grid-cols-1 gap-2 text-sm text-gray-500 dark:text-slate-400 md:grid-cols-2">
                    <div>Definition: {workflow.definitionId}</div>
                    <div>Version: v{workflow.definitionVersion}</div>
                    <div>Current step: {workflow.currentStepName || 'completed'}</div>
                    <div>Updated: {formatDate(workflow.updatedAt)}</div>
                  </div>
                </div>
                <Link
                  to={`/runs/${workflow.id}`}
                  className="rounded-lg border border-gray-200 px-3 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
                >
                  Open run
                </Link>
              </div>
            ))
          )}
        </div>
      </section>
    </div>
  )
}
