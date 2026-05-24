import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, ChevronDown, ChevronRight, Wifi, WifiOff } from 'lucide-react'
import { Link } from 'react-router-dom'
import Pagination from '../components/Pagination'
import { useLiveBus } from '../live/WorkflowLiveProvider'
import { workflowApi } from '../services/api'
import type { WorkflowEvent, WorkflowTask, WorkflowTaskAction } from '../types'
import {
  actionLabel,
  availableTaskActions,
  eventFilterMatches,
  formatEventType,
  payloadSummary,
  statusClasses,
} from './workflowUi'

// ─── helpers ─────────────────────────────────────────────────────────────────

function timeAgo(iso: string) {
  const ms = Date.now() - new Date(iso).getTime()
  if (ms < 60_000) return 'just now'
  if (ms < 3_600_000) return `${Math.floor(ms / 60_000)}m ago`
  if (ms < 86_400_000) return `${Math.floor(ms / 3_600_000)}h ago`
  return `${Math.floor(ms / 86_400_000)}d ago`
}

function eventDotClass(eventType: string) {
  if (eventType.includes('Failed') || eventType.includes('Error')) return 'bg-red-500'
  if (eventType.includes('Completed')) return 'bg-emerald-500'
  if (eventType.includes('Started')) return 'bg-sky-500'
  if (eventType.includes('Waiting') || eventType.includes('Signal')) return 'bg-amber-500'
  if (eventType.includes('Canceled')) return 'bg-slate-400'
  return 'bg-violet-400'
}

const TASK_URGENCY: Record<string, number> = { failed: 0, paused: 1, waiting: 2, running: 3, pending: 4 }
function taskUrgency(t: WorkflowTask) { return TASK_URGENCY[t.status] ?? 9 }

// ─── sub-components ───────────────────────────────────────────────────────────

function MetricChip({ label, value, color }: { label: string; value: number; color: string }) {
  return (
    <div className="flex items-center gap-2 rounded-lg border border-gray-200 bg-white px-4 py-2.5 dark:border-slate-800 dark:bg-slate-900">
      <span className={`text-lg font-bold tabular-nums ${color}`}>{value}</span>
      <span className="text-xs text-gray-500 dark:text-slate-400">{label}</span>
    </div>
  )
}

function TaskActionButton({
  action,
  onClick,
  disabled,
}: {
  action: WorkflowTaskAction
  onClick: () => void
  disabled: boolean
}) {
  const base = 'rounded px-2 py-1 text-xs font-semibold transition-colors disabled:cursor-not-allowed disabled:opacity-50'
  const style =
    action === 'cancel'
      ? `${base} border border-red-200 text-red-600 hover:bg-red-50 dark:border-red-900/50 dark:text-red-400 dark:hover:bg-red-950/30`
      : `${base} border border-gray-200 text-gray-700 hover:bg-gray-50 dark:border-slate-700 dark:text-slate-300 dark:hover:bg-slate-800`
  return (
    <button type="button" className={style} onClick={onClick} disabled={disabled}>
      {actionLabel(action)}
    </button>
  )
}

function TaskRow({ task, onAction, isPending }: { task: WorkflowTask; onAction: (id: number, a: WorkflowTaskAction) => void; isPending: boolean }) {
  const actions = availableTaskActions(task)
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-gray-200 p-3 dark:border-slate-800 sm:flex-row sm:items-center sm:justify-between">
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <Link
            to={`/runs/${task.workflowId}`}
            className="truncate font-mono text-xs font-medium text-primary-600 hover:underline dark:text-primary-400"
          >
            {task.workflowId}
          </Link>
          <span className={`inline-flex shrink-0 rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${statusClasses(task.status)}`}>
            {task.status}
          </span>
        </div>
        <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-gray-500 dark:text-slate-400">
          <span>{task.stepName || `Step ${task.stepIndex}`}</span>
          <span className="rounded bg-slate-100 px-1.5 py-0.5 font-mono dark:bg-slate-800">{task.activityName}</span>
          <span>
            Attempt {task.attempts}/{task.maxAttempts}
          </span>
          {task.lastError && (
            <span className="flex items-center gap-1 text-red-600 dark:text-red-400">
              <AlertTriangle className="h-3 w-3" />
              {task.lastError}
            </span>
          )}
        </div>
      </div>
      {actions.length > 0 && (
        <div className="flex shrink-0 flex-wrap gap-1.5">
          {actions.map((a) => (
            <TaskActionButton key={a} action={a} onClick={() => onAction(task.id, a)} disabled={isPending} />
          ))}
        </div>
      )}
    </div>
  )
}

function EventRow({ event }: { event: WorkflowEvent }) {
  const [open, setOpen] = useState(false)
  const hasPayload = event.payload != null && Object.keys(event.payload as object).length > 0
  const summary = payloadSummary(event.payload)

  return (
    <div className="border-b border-gray-100 last:border-0 dark:border-slate-800">
      <button
        type="button"
        onClick={() => hasPayload && setOpen((v) => !v)}
        className={`flex w-full items-center gap-3 px-3 py-2 text-left transition-colors ${hasPayload ? 'hover:bg-gray-50 dark:hover:bg-slate-800/50' : ''}`}
      >
        <span className={`mt-0.5 h-2 w-2 shrink-0 rounded-full ${eventDotClass(event.eventType)}`} />
        <span className="flex-1 min-w-0">
          <span className="text-sm font-medium text-gray-900 dark:text-slate-100">{formatEventType(event.eventType)}</span>
          {summary && <span className="ml-2 text-xs text-gray-500 dark:text-slate-400">{summary}</span>}
          {event.workflowId && (
            <Link
              to={`/runs/${event.workflowId}`}
              onClick={(e) => e.stopPropagation()}
              className="ml-2 font-mono text-xs text-primary-600 hover:underline dark:text-primary-400"
            >
              {event.workflowId}
            </Link>
          )}
        </span>
        <span className="shrink-0 text-xs text-gray-400 dark:text-slate-500">{timeAgo(event.createdAt)}</span>
        {hasPayload ? (
          open ? <ChevronDown className="h-3.5 w-3.5 shrink-0 text-gray-400" /> : <ChevronRight className="h-3.5 w-3.5 shrink-0 text-gray-400" />
        ) : null}
      </button>
      {open && hasPayload && (
        <pre className="overflow-x-auto bg-slate-950 px-4 py-3 text-xs text-slate-100">
          {JSON.stringify(event.payload, null, 2)}
        </pre>
      )}
    </div>
  )
}

// ─── page ─────────────────────────────────────────────────────────────────────

type EventFilter = 'all' | 'lifecycle' | 'failure' | 'queue'

const EVENT_FILTER_LABELS: { key: EventFilter; label: string }[] = [
  { key: 'all', label: 'All' },
  { key: 'lifecycle', label: 'Lifecycle' },
  { key: 'failure', label: 'Failures' },
  { key: 'queue', label: 'Queue' },
]

const TASK_PAGE_SIZE = 20

export default function OperationsPage() {
  const { status } = useLiveBus()
  const queryClient = useQueryClient()
  const [eventFilter, setEventFilter] = useState<EventFilter>('all')
  const [showCompleted, setShowCompleted] = useState(false)
  const [taskPage, setTaskPage] = useState(0)

  const operationsQuery = useQuery({
    queryKey: ['workflow-operations'],
    queryFn: () => workflowApi.listOperations(80),
  })
  const workflowsQuery = useQuery({
    queryKey: ['workflows'],
    queryFn: () => workflowApi.listWorkflows(),
  })
  const tasksQuery = useQuery({
    queryKey: ['workflow-tasks'],
    queryFn: () => workflowApi.listTasks(),
  })

  const taskActionMutation = useMutation({
    mutationFn: ({ taskId, action }: { taskId: number; action: WorkflowTaskAction }) =>
      workflowApi.applyTaskAction(taskId, action),
    onSuccess: async () => {
      setTaskPage(0)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['workflow-tasks'] }),
        queryClient.invalidateQueries({ queryKey: ['workflows'] }),
        queryClient.invalidateQueries({ queryKey: ['workflow-operations'] }),
      ])
    },
  })

  if (operationsQuery.isLoading || workflowsQuery.isLoading || tasksQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading operations…</div>
  }

  if (operationsQuery.error || workflowsQuery.error || tasksQuery.error) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Unable to load workflow operations.</div>
  }

  const allEvents = operationsQuery.data?.events ?? []
  const workflows = workflowsQuery.data?.workflows ?? []
  const allTasks = tasksQuery.data?.tasks ?? []

  const activeTasks = allTasks
    .filter((t) => t.status !== 'completed')
    .sort((a, b) => taskUrgency(a) - taskUrgency(b))
  const completedTasks = allTasks.filter((t) => t.status === 'completed')

  const activeTaskPage = activeTasks.slice(taskPage * TASK_PAGE_SIZE, (taskPage + 1) * TASK_PAGE_SIZE)

  const filteredEvents = allEvents.filter((e) => eventFilterMatches(eventFilter, e.eventType))

  const running = workflows.filter((w) => w.status === 'running').length
  const pending = allTasks.filter((t) => t.status === 'pending').length
  const waiting = allTasks.filter((t) => t.status === 'waiting' || t.status === 'paused').length
  const failures = allTasks.filter((t) => t.status === 'failed').length

  return (
    <div className="space-y-6 p-8">
      {/* Header */}
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold text-gray-900 dark:text-slate-100">Operations</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-slate-400">Live task queue and workflow event stream.</p>
        </div>
        <div className={`flex items-center gap-1.5 rounded-full px-3 py-1.5 text-xs font-semibold ${
          status === 'connected'
            ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
            : status === 'reconnecting'
              ? 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
              : 'bg-slate-200 text-slate-600 dark:bg-slate-800 dark:text-slate-300'
        }`}>
          {status === 'connected' ? <Wifi className="h-3.5 w-3.5" /> : <WifiOff className="h-3.5 w-3.5" />}
          {status === 'connected' ? 'Live' : status}
        </div>
      </div>

      {/* Metrics strip */}
      <div className="flex flex-wrap gap-3">
        <MetricChip label="running workflows" value={running} color="text-sky-600 dark:text-sky-400" />
        <MetricChip label="pending tasks" value={pending} color="text-violet-600 dark:text-violet-400" />
        <MetricChip label="waiting / paused" value={waiting} color="text-amber-600 dark:text-amber-400" />
        <MetricChip label="failed tasks" value={failures} color={failures > 0 ? 'text-red-600 dark:text-red-400' : 'text-gray-400 dark:text-slate-500'} />
      </div>

      {/* Active tasks */}
      <section>
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-sm font-semibold text-gray-900 dark:text-slate-100">
            Active tasks
            {activeTasks.length > 0 && (
              <span className="ml-2 rounded-full bg-gray-100 px-2 py-0.5 text-xs font-semibold text-gray-600 dark:bg-slate-800 dark:text-slate-300">
                {activeTasks.length}
              </span>
            )}
          </h2>
        </div>

        <div className="mt-2 space-y-2">
          {activeTasks.length === 0 ? (
            <div className="rounded-lg border border-dashed border-gray-300 p-4 text-center text-sm text-gray-500 dark:border-slate-700 dark:text-slate-400">
              No active tasks.
            </div>
          ) : (
            activeTaskPage.map((task) => (
              <TaskRow
                key={task.id}
                task={task}
                onAction={(id, action) => taskActionMutation.mutate({ taskId: id, action })}
                isPending={taskActionMutation.isPending}
              />
            ))
          )}
        </div>

        {activeTasks.length > TASK_PAGE_SIZE && (
          <div className="mt-3">
            <Pagination
              page={taskPage}
              pageSize={TASK_PAGE_SIZE}
              total={activeTasks.length}
              onChange={setTaskPage}
            />
          </div>
        )}

        {completedTasks.length > 0 && (
          <button
            type="button"
            onClick={() => setShowCompleted((v) => !v)}
            className="mt-2 flex items-center gap-1.5 text-xs text-gray-500 hover:text-gray-700 dark:text-slate-400 dark:hover:text-slate-200"
          >
            {showCompleted ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
            {showCompleted ? 'Hide' : 'Show'} {completedTasks.length} completed task{completedTasks.length !== 1 ? 's' : ''}
          </button>
        )}

        {showCompleted && completedTasks.length > 0 && (
          <div className="mt-2 space-y-2">
            {completedTasks.map((task) => (
              <TaskRow
                key={task.id}
                task={task}
                onAction={(id, action) => taskActionMutation.mutate({ taskId: id, action })}
                isPending={taskActionMutation.isPending}
              />
            ))}
          </div>
        )}
      </section>

      {/* Event stream */}
      <section>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="text-sm font-semibold text-gray-900 dark:text-slate-100">
            Event stream
            <span className="ml-2 rounded-full bg-gray-100 px-2 py-0.5 text-xs font-semibold text-gray-600 dark:bg-slate-800 dark:text-slate-300">
              {filteredEvents.length}
            </span>
          </h2>
          <div className="flex rounded-lg border border-gray-200 bg-gray-50 p-0.5 dark:border-slate-700 dark:bg-slate-800">
            {EVENT_FILTER_LABELS.map(({ key, label }) => (
              <button
                key={key}
                type="button"
                onClick={() => setEventFilter(key)}
                className={`rounded-md px-3 py-1 text-xs font-semibold transition-colors ${
                  eventFilter === key
                    ? 'bg-white text-gray-900 shadow-sm dark:bg-slate-700 dark:text-slate-100'
                    : 'text-gray-500 hover:text-gray-700 dark:text-slate-400 dark:hover:text-slate-200'
                }`}
              >
                {label}
              </button>
            ))}
          </div>
        </div>

        <div className="mt-2 rounded-xl border border-gray-200 bg-white dark:border-slate-800 dark:bg-slate-900">
          {filteredEvents.length === 0 ? (
            <p className="p-4 text-sm text-gray-500 dark:text-slate-400">No events match this filter.</p>
          ) : (
            [...filteredEvents].reverse().map((event) => (
              <EventRow key={`${event.workflowId ?? 'global'}-${event.sequence}`} event={event} />
            ))
          )}
        </div>
        <p className="mt-1.5 text-right text-xs text-gray-400 dark:text-slate-500">
          Showing last {allEvents.length} events · newest first
        </p>
      </section>
    </div>
  )
}
