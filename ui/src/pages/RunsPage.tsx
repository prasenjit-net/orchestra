import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, Clock3, Play, Workflow } from 'lucide-react'
import { Link } from 'react-router-dom'
import Pagination from '../components/Pagination'
import SectionHeader from '../components/SectionHeader'
import StatCard from '../components/StatCard'
import { workflowApi } from '../services/api'
import { formatDate, statusClasses } from './workflowUi'

const PAGE_SIZE = 25

export default function RunsPage() {
  const queryClient = useQueryClient()
  const [page, setPage] = useState(0)

  const workflowsQuery = useQuery({
    queryKey: ['workflows', { page, limit: PAGE_SIZE }],
    queryFn: () => workflowApi.listWorkflows({ limit: PAGE_SIZE, offset: page * PAGE_SIZE }),
  })

  // Separate unpaginated counts query so the stat cards don't reset when paging
  const countsQuery = useQuery({
    queryKey: ['workflows', 'counts'],
    queryFn: () => workflowApi.listWorkflows({ limit: 1, offset: 0 }),
    select: (data) => data.total,
  })

  const tasksQuery = useQuery({
    queryKey: ['workflow-tasks'],
    queryFn: () => workflowApi.listTasks(),
  })

  const startMutation = useMutation({
    mutationFn: (definitionId: string) => workflowApi.startWorkflow(definitionId),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['workflows'] }),
        queryClient.invalidateQueries({ queryKey: ['workflow-tasks'] }),
      ])
      setPage(0)
    },
  })

  if (workflowsQuery.isLoading || tasksQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading runs…</div>
  }

  if (workflowsQuery.error || tasksQuery.error) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Unable to load workflow runs.</div>
  }

  const workflows = workflowsQuery.data?.workflows ?? []
  const total = workflowsQuery.data?.total ?? 0
  const tasks = tasksQuery.data?.tasks ?? []

  const running = workflows.filter((w) => w.status === 'running').length
  const failed = workflows.filter((w) => w.status === 'failed').length
  const queued = tasks.filter((t) => ['pending', 'running', 'paused', 'waiting'].includes(t.status)).length

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
        <StatCard
          label="Total runs"
          value={String(countsQuery.data ?? total)}
          description="Workflow instances in durable storage."
          icon={Workflow}
          tone="bg-sky-50 text-sky-600 dark:bg-sky-900/20 dark:text-sky-300"
        />
        <StatCard
          label="Running"
          value={String(running)}
          description="Instances currently executing on this page."
          icon={Play}
          tone="bg-emerald-50 text-emerald-600 dark:bg-emerald-900/20 dark:text-emerald-300"
        />
        <StatCard
          label="Failed"
          value={String(failed)}
          description="Runs that need operator attention on this page."
          icon={Activity}
          tone="bg-red-50 text-red-600 dark:bg-red-900/20 dark:text-red-300"
        />
        <StatCard
          label="Queued tasks"
          value={String(queued)}
          description="Pending, running, paused, or signal-waiting tasks across all runs."
          icon={Clock3}
          tone="bg-amber-50 text-amber-600 dark:bg-amber-900/20 dark:text-amber-300"
        />
      </div>

      <section className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">Workflow instances</h2>
        <div className="mt-4 space-y-3">
          {workflows.length === 0 ? (
            <p className="text-sm text-gray-500 dark:text-slate-400">No workflow runs yet.</p>
          ) : (
            workflows.map((workflow) => (
              <div
                key={workflow.id}
                className="flex flex-col gap-3 rounded-lg border border-gray-200 p-4 dark:border-slate-800 md:flex-row md:items-center md:justify-between"
              >
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <div className="truncate font-medium text-gray-900 dark:text-slate-100">{workflow.id}</div>
                    <span
                      className={`inline-flex rounded-full px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide ${statusClasses(workflow.status)}`}
                    >
                      {workflow.status}
                    </span>
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
                  className="shrink-0 rounded-lg border border-gray-200 px-3 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
                >
                  Open run
                </Link>
              </div>
            ))
          )}
        </div>

        {total > PAGE_SIZE && (
          <div className="mt-6 border-t border-gray-100 pt-4 dark:border-slate-800">
            <Pagination
              page={page}
              pageSize={PAGE_SIZE}
              total={total}
              onChange={(p) => {
                setPage(p)
                void queryClient.invalidateQueries({ queryKey: ['workflows', { page: p, limit: PAGE_SIZE }] })
              }}
            />
          </div>
        )}
      </section>
    </div>
  )
}
