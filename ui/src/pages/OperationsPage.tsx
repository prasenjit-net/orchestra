import { useQuery } from '@tanstack/react-query'
import { Activity, Clock3, Radio, Workflow } from 'lucide-react'
import SectionHeader from '../components/SectionHeader'
import StatCard from '../components/StatCard'
import { useLiveBus } from '../live/WorkflowLiveProvider'
import { workflowApi } from '../services/api'
import { EventCard, eventFilterMatches } from './workflowUi'

type OperationFilter = 'all' | 'queue' | 'lifecycle' | 'failure'

export default function OperationsPage() {
  const { status } = useLiveBus()
  const operationsQuery = useQuery({
    queryKey: ['workflow-operations'],
    queryFn: () => workflowApi.listOperations(80),
  })
  const workflowsQuery = useQuery({
    queryKey: ['workflows'],
    queryFn: workflowApi.listWorkflows,
  })
  const tasksQuery = useQuery({
    queryKey: ['workflow-tasks'],
    queryFn: workflowApi.listTasks,
  })

  const filter: OperationFilter = 'all'

  if (operationsQuery.isLoading || workflowsQuery.isLoading || tasksQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading operations…</div>
  }

  if (operationsQuery.error || workflowsQuery.error || tasksQuery.error) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Unable to load workflow operations.</div>
  }

  const events = (operationsQuery.data?.events ?? []).filter((event) => eventFilterMatches(filter, event.eventType))
  const workflows = workflowsQuery.data?.workflows ?? []
  const tasks = tasksQuery.data?.tasks ?? []

  const liveTone =
    status === 'connected'
      ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
      : status === 'reconnecting'
        ? 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
        : 'bg-slate-200 text-slate-700 dark:bg-slate-800 dark:text-slate-200'

  return (
    <div className="space-y-8 p-8">
      <SectionHeader
        title="Operations"
        description="Follow workflow lifecycle, queue activity, and failures from the durable event stream."
        action={
          <div className={`rounded-full px-3 py-2 text-xs font-semibold uppercase tracking-wide ${liveTone}`}>
            Live {status}
          </div>
        }
      />

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Events" value={String(events.length)} description="Recent durable events returned by the API." icon={Activity} tone="bg-violet-50 text-violet-600 dark:bg-violet-900/20 dark:text-violet-300" />
        <StatCard label="Running runs" value={String(workflows.filter((workflow) => workflow.status === 'running').length)} description="Currently active workflow instances." icon={Workflow} tone="bg-sky-50 text-sky-600 dark:bg-sky-900/20 dark:text-sky-300" />
        <StatCard label="Queued tasks" value={String(tasks.filter((task) => task.status === 'pending' || task.status === 'running' || task.status === 'paused').length)} description="Queue depth across all workflow instances." icon={Clock3} tone="bg-amber-50 text-amber-600 dark:bg-amber-900/20 dark:text-amber-300" />
        <StatCard label="Failures" value={String(events.filter((event) => event.eventType.includes('Failed')).length)} description="Recent workflow or activity failures." icon={Radio} tone="bg-red-50 text-red-600 dark:bg-red-900/20 dark:text-red-300" />
      </div>

      <section className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">Recent events</h2>
        <div className="mt-4 space-y-3">
          {events.length === 0 ? <p className="text-sm text-gray-500 dark:text-slate-400">No workflow events yet.</p> : events.map((event) => <EventCard key={`${event.workflowId ?? 'global'}-${event.sequence}`} event={event} showWorkflowLink />)}
        </div>
      </section>
    </div>
  )
}
