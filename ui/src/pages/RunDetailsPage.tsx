import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Activity,
  AlertCircle,
  ArrowLeft,
  Bell,
  ChevronDown,
  ChevronRight,
  Clock3,
  LayoutList,
} from 'lucide-react'
import { Link, useParams } from 'react-router-dom'
import { workflowApi } from '../services/api'
import type { WorkflowEvent, WorkflowTask, WorkflowTaskAction } from '../types'
import {
  actionLabel,
  availableTaskActions,
  eventFilterMatches,
  formatDate,
  formatEventType,
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

// ─── sub-components ───────────────────────────────────────────────────────────

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0">
      <div className="text-[10px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">{label}</div>
      <div className="mt-0.5 truncate text-sm font-medium text-gray-900 dark:text-slate-100">{children}</div>
    </div>
  )
}

function CollapsibleJSON({ label, value }: { label: string; value: unknown }) {
  const [open, setOpen] = useState(false)
  if (!value || (typeof value === 'object' && !Array.isArray(value) && Object.keys(value as object).length === 0)) return null
  return (
    <div className="rounded-xl border border-gray-200 dark:border-slate-800">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left"
      >
        <span className="text-sm font-semibold text-gray-900 dark:text-slate-100">{label}</span>
        {open ? <ChevronDown className="h-4 w-4 text-gray-400 dark:text-slate-500" /> : <ChevronRight className="h-4 w-4 text-gray-400 dark:text-slate-500" />}
      </button>
      {open && (
        <pre className="overflow-x-auto rounded-b-xl bg-slate-950 px-4 py-3 text-xs text-slate-100">
          {JSON.stringify(value, null, 2)}
        </pre>
      )}
    </div>
  )
}

function TaskActionButton({ action, onClick, disabled }: { action: WorkflowTaskAction; onClick: () => void; disabled: boolean }) {
  const destructive = action === 'cancel'
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={`rounded-lg border px-3 py-1.5 text-xs font-semibold transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
        destructive
          ? 'border-red-200 text-red-700 hover:bg-red-50 dark:border-red-900/40 dark:text-red-400 dark:hover:bg-red-950/20'
          : 'border-gray-200 text-gray-700 hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800'
      }`}
    >
      {actionLabel(action)}
    </button>
  )
}

function ActiveTaskCard({ task, onAction, isPending }: { task: WorkflowTask; onAction: (id: number, action: WorkflowTaskAction) => void; isPending: boolean }) {
  return (
    <div className="rounded-xl border border-primary-200 bg-primary-50/60 p-4 dark:border-primary-900/40 dark:bg-primary-950/20">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-semibold text-gray-900 dark:text-slate-100">{task.stepName}</span>
            <span className={`inline-flex rounded-full px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${statusClasses(task.status)}`}>
              {task.status}
            </span>
            <span className="rounded-full bg-white px-2.5 py-0.5 text-[10px] font-semibold text-primary-700 shadow-sm dark:bg-slate-900 dark:text-primary-300">
              {task.activityName}
            </span>
          </div>
          <div className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1 text-xs text-gray-500 dark:text-slate-400">
            <span>Attempt {task.attempts} of {task.maxAttempts}</span>
            <span>Scheduled {formatDate(task.runAt)}</span>
            {task.leaseOwner && <span>Leased by {task.leaseOwner}</span>}
          </div>
          {task.lastError && (
            <div className="mt-2 flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-900/40 dark:bg-red-950/20 dark:text-red-400">
              <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              {task.lastError}
            </div>
          )}
        </div>
        <div className="flex shrink-0 flex-wrap gap-2">
          {availableTaskActions(task).map((action) => (
            <TaskActionButton key={action} action={action} onClick={() => onAction(task.id, action)} disabled={isPending} />
          ))}
        </div>
      </div>
    </div>
  )
}

function EventRow({ event }: { event: WorkflowEvent }) {
  const [open, setOpen] = useState(false)
  const hasPayload = Boolean(event.payload && typeof event.payload === 'object' && Object.keys(event.payload as object).length > 0)

  return (
    <div className="border-b border-gray-100 last:border-0 dark:border-slate-800">
      <button
        type="button"
        onClick={() => hasPayload && setOpen((v) => !v)}
        className={`flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors ${hasPayload ? 'hover:bg-gray-50 dark:hover:bg-slate-800/50' : ''}`}
      >
        <span className={`h-2 w-2 shrink-0 rounded-full ${eventDotClass(event.eventType)}`} />
        <span className="w-28 shrink-0 text-[11px] text-gray-400 dark:text-slate-500">{timeAgo(event.createdAt)}</span>
        <span className="flex-1 text-sm font-medium text-gray-800 dark:text-slate-200">{formatEventType(event.eventType)}</span>
        <span className="shrink-0 text-[11px] text-gray-400 dark:text-slate-500">#{event.sequence}</span>
        {hasPayload && (
          open
            ? <ChevronDown className="h-3.5 w-3.5 shrink-0 text-gray-400 dark:text-slate-500" />
            : <ChevronRight className="h-3.5 w-3.5 shrink-0 text-gray-400 dark:text-slate-500" />
        )}
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

export default function RunDetailsPage() {
  const { workflowId = '' } = useParams()
  const queryClient = useQueryClient()
  const [eventFilter, setEventFilter] = useState<'all' | 'lifecycle' | 'failure' | 'queue'>('all')
  const [showTaskHistory, setShowTaskHistory] = useState(false)
  const [historyLimit, setHistoryLimit] = useState(50)

  const workflowQuery = useQuery({
    queryKey: ['workflow', workflowId],
    queryFn: () => workflowApi.getWorkflow(workflowId),
    enabled: Boolean(workflowId),
  })
  const historyQuery = useQuery({
    queryKey: ['workflow-history', workflowId, historyLimit],
    queryFn: () => workflowApi.getWorkflowHistory(workflowId, historyLimit),
    enabled: Boolean(workflowId),
  })
  const tasksQuery = useQuery({
    queryKey: ['workflow-tasks'],
    queryFn: () => workflowApi.listTasks(),
  })

  const taskActionMutation = useMutation({
    mutationFn: ({ taskId, action }: { taskId: number; action: WorkflowTaskAction }) =>
      workflowApi.applyTaskAction(taskId, action),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['workflow-tasks'] }),
        queryClient.invalidateQueries({ queryKey: ['workflow', workflowId] }),
        queryClient.invalidateQueries({ queryKey: ['workflow-history', workflowId] }),
        queryClient.invalidateQueries({ queryKey: ['workflows'] }),
      ])
    },
  })

  if (workflowQuery.isLoading || historyQuery.isLoading || tasksQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading run details…</div>
  }
  if (workflowQuery.error || historyQuery.error || !workflowQuery.data) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Unable to load run details.</div>
  }

  const workflow = workflowQuery.data
  const historyData = historyQuery.data
  const history = historyData?.events ?? []
  const historyTotal = historyData?.total ?? 0
  const hasMoreHistory = historyTotal > history.length
  const allTasks = (tasksQuery.data?.tasks ?? []).filter((t) => t.workflowId === workflow.id)
  const activeTasks = allTasks.filter((t) => ['pending', 'running', 'waiting', 'paused'].includes(t.status))
  const doneTasks = allTasks.filter((t) => ['completed', 'failed', 'canceled'].includes(t.status))
  const filteredEvents = history.filter((e) => eventFilterMatches(eventFilter, e.eventType))

  const isRunning = workflow.status === 'running'
  const hasOutput = Boolean(workflow.lastOutput && typeof workflow.lastOutput === 'object' && Object.keys(workflow.lastOutput as object).length > 0)
  const hasContext = Boolean(workflow.context && Object.keys(workflow.context).length > 0)

  const filterTabs: { key: 'all' | 'lifecycle' | 'failure' | 'queue'; label: string }[] = [
    { key: 'all', label: `All (${historyTotal > history.length ? `${history.length}/${historyTotal}` : history.length})` },
    { key: 'lifecycle', label: 'Lifecycle' },
    { key: 'failure', label: 'Failures' },
    { key: 'queue', label: 'Queue' },
  ]

  return (
    <div className="space-y-5 p-6 lg:p-8">

      {/* ── Header ─────────────────────────────────────────────────────── */}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <Link
            to="/runs"
            className="inline-flex items-center gap-1.5 text-xs font-medium text-gray-500 transition-colors hover:text-gray-900 dark:text-slate-400 dark:hover:text-slate-100"
          >
            <ArrowLeft className="h-3.5 w-3.5" />
            All runs
          </Link>
          <div className="mt-1.5 flex flex-wrap items-center gap-2">
            <h1 className="truncate font-mono text-base font-semibold text-gray-900 dark:text-slate-100">
              {workflow.id}
            </h1>
            <span className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide ${statusClasses(workflow.status)}`}>
              {isRunning && (
                <span className="relative flex h-2 w-2">
                  <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-sky-400 opacity-75" />
                  <span className="relative inline-flex h-2 w-2 rounded-full bg-sky-500" />
                </span>
              )}
              {workflow.status}
            </span>
          </div>
          <p className="mt-1 text-xs text-gray-500 dark:text-slate-400">
            Started {timeAgo(workflow.createdAt)} · Updated {timeAgo(workflow.updatedAt)}
          </p>
        </div>
        <div className="flex shrink-0 gap-2">
          <Link
            to="/queues"
            className="rounded-lg border border-gray-200 px-3 py-2 text-xs font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
          >
            Queue view
          </Link>
        </div>
      </div>

      {/* ── Error banner ───────────────────────────────────────────────── */}
      {workflow.lastError && (
        <div className="flex items-start gap-3 rounded-xl border border-red-200 bg-red-50 px-4 py-3 dark:border-red-900/40 dark:bg-red-950/20">
          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-red-600 dark:text-red-400" />
          <div>
            <div className="text-sm font-semibold text-red-700 dark:text-red-300">Last error</div>
            <div className="mt-0.5 text-sm text-red-600 dark:text-red-400">{workflow.lastError}</div>
          </div>
        </div>
      )}

      {/* ── Facts strip ────────────────────────────────────────────────── */}
      <div className="grid grid-cols-2 gap-3 rounded-xl border border-gray-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900 sm:grid-cols-3 xl:grid-cols-6">
        <Fact label="Definition">
          <span className="font-mono text-xs">{workflow.definitionId}</span>
        </Fact>
        <Fact label="Version">v{workflow.definitionVersion}</Fact>
        <Fact label="Current step">
          {workflow.currentStepName || <span className="text-gray-400 dark:text-slate-500">—</span>}
        </Fact>
        <Fact label="Activity">
          {workflow.currentActivity
            ? <span className="rounded-full bg-primary-50 px-2 py-0.5 text-[11px] font-semibold text-primary-700 dark:bg-primary-900/20 dark:text-primary-300">{workflow.currentActivity}</span>
            : <span className="text-gray-400 dark:text-slate-500">—</span>}
        </Fact>
        <Fact label="Events">{workflow.lastEventSequence}</Fact>
        {workflow.triggerSource && (
          <Fact label="Triggered via">
            <span className={`inline-flex rounded-full px-2 py-0.5 text-[11px] font-semibold ${
              workflow.triggerSource === 'webhook'
                ? 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
                : 'bg-gray-100 text-gray-600 dark:bg-slate-800 dark:text-slate-300'
            }`}>
              {workflow.triggerSource}
            </span>
          </Fact>
        )}
        {workflow.callbackUrl && (
          <Fact label="Callback">
            <span className={`inline-flex rounded-full px-2 py-0.5 text-[11px] font-semibold ${
              workflow.callbackStatus === 'delivered'
                ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
                : workflow.callbackStatus === 'failed'
                  ? 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
                  : 'bg-gray-100 text-gray-600 dark:bg-slate-800 dark:text-slate-300'
            }`}>
              {workflow.callbackStatus || 'pending'}
            </span>
          </Fact>
        )}
        {workflow.pendingSignals > 0 && (
          <Fact label="Pending signals">
            <span className="flex items-center gap-1 text-amber-600 dark:text-amber-400">
              <Bell className="h-3.5 w-3.5" />
              {workflow.pendingSignals}
            </span>
          </Fact>
        )}
        {workflow.nextRunAt && (
          <Fact label="Next run">
            <span className="flex items-center gap-1 text-gray-600 dark:text-slate-300">
              <Clock3 className="h-3.5 w-3.5" />
              {timeAgo(workflow.nextRunAt)}
            </span>
          </Fact>
        )}
      </div>

      {/* ── Active task ────────────────────────────────────────────────── */}
      {activeTasks.length > 0 && (
        <div className="space-y-2">
          <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">
            <Activity className="h-3.5 w-3.5" />
            Active task{activeTasks.length > 1 ? 's' : ''}
          </div>
          {activeTasks.map((task) => (
            <ActiveTaskCard
              key={task.id}
              task={task}
              onAction={(id, action) => taskActionMutation.mutate({ taskId: id, action })}
              isPending={taskActionMutation.isPending}
            />
          ))}
        </div>
      )}

      {/* ── Last output + context ──────────────────────────────────────── */}
      {(hasOutput || hasContext) && (
        <div className={`grid gap-4 ${hasOutput && hasContext ? 'xl:grid-cols-2' : ''}`}>
          {hasOutput && <CollapsibleJSON label="Last output" value={workflow.lastOutput} />}
          {hasContext && <CollapsibleJSON label="Workflow context" value={workflow.context} />}
        </div>
      )}

      {/* ── Event history ──────────────────────────────────────────────── */}
      <div className="rounded-xl border border-gray-200 bg-white dark:border-slate-800 dark:bg-slate-900">
        <div className="flex flex-col gap-3 px-4 pb-3 pt-4 sm:flex-row sm:items-center sm:justify-between">
          <h2 className="text-sm font-semibold text-gray-900 dark:text-slate-100">Event history</h2>
          <div className="flex gap-1 rounded-lg border border-gray-200 p-0.5 dark:border-slate-700">
            {filterTabs.map((tab) => (
              <button
                key={tab.key}
                type="button"
                onClick={() => setEventFilter(tab.key)}
                className={`rounded-md px-3 py-1 text-[11px] font-semibold transition-colors ${
                  eventFilter === tab.key
                    ? 'bg-primary-600 text-white'
                    : 'text-gray-600 hover:bg-gray-100 dark:text-slate-300 dark:hover:bg-slate-800'
                }`}
              >
                {tab.label}
              </button>
            ))}
          </div>
        </div>
        {filteredEvents.length === 0 ? (
          <div className="px-4 pb-4 text-sm text-gray-500 dark:text-slate-400">No events match this filter.</div>
        ) : (
          <div className="divide-y divide-gray-100 border-t border-gray-100 dark:divide-slate-800 dark:border-slate-800">
            {[...filteredEvents].reverse().map((event) => (
              <EventRow key={`${event.sequence}-${event.eventType}`} event={event} />
            ))}
          </div>
        )}
        {hasMoreHistory && (
          <div className="border-t border-gray-100 px-4 py-3 dark:border-slate-800">
            <button
              type="button"
              onClick={() => setHistoryLimit((l) => l + 50)}
              className="text-xs font-medium text-primary-600 hover:underline dark:text-primary-400"
            >
              Load more ({historyTotal - history.length} remaining)
            </button>
          </div>
        )}
      </div>

      {/* ── Task history ───────────────────────────────────────────────── */}
      {doneTasks.length > 0 && (
        <div className="rounded-xl border border-gray-200 bg-white dark:border-slate-800 dark:bg-slate-900">
          <button
            type="button"
            onClick={() => setShowTaskHistory((v) => !v)}
            className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left"
          >
            <span className="flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-slate-100">
              <LayoutList className="h-4 w-4 text-gray-400 dark:text-slate-500" />
              Task history
              <span className="rounded-full bg-gray-100 px-2 py-0.5 text-[11px] font-semibold text-gray-600 dark:bg-slate-800 dark:text-slate-300">
                {doneTasks.length}
              </span>
            </span>
            {showTaskHistory
              ? <ChevronDown className="h-4 w-4 text-gray-400 dark:text-slate-500" />
              : <ChevronRight className="h-4 w-4 text-gray-400 dark:text-slate-500" />}
          </button>
          {showTaskHistory && (
            <div className="border-t border-gray-100 dark:border-slate-800">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-gray-100 bg-gray-50 dark:border-slate-800 dark:bg-slate-800/50">
                    <th className="px-4 py-2 text-left text-[10px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Step</th>
                    <th className="px-4 py-2 text-left text-[10px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Activity</th>
                    <th className="px-4 py-2 text-left text-[10px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Status</th>
                    <th className="px-4 py-2 text-left text-[10px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Attempts</th>
                    <th className="px-4 py-2 text-left text-[10px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Completed</th>
                  </tr>
                </thead>
                <tbody>
                  {doneTasks.map((task) => (
                    <tr key={task.id} className="border-b border-gray-50 last:border-0 hover:bg-gray-50/50 dark:border-slate-800/50 dark:hover:bg-slate-800/30">
                      <td className="px-4 py-2.5 font-medium text-gray-900 dark:text-slate-100">{task.stepName}</td>
                      <td className="px-4 py-2.5 text-gray-500 dark:text-slate-400">{task.activityName}</td>
                      <td className="px-4 py-2.5">
                        <span className={`inline-flex rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${statusClasses(task.status)}`}>
                          {task.status}
                        </span>
                      </td>
                      <td className="px-4 py-2.5 text-gray-500 dark:text-slate-400">{task.attempts}/{task.maxAttempts}</td>
                      <td className="px-4 py-2.5 text-gray-500 dark:text-slate-400">{timeAgo(task.updatedAt)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
