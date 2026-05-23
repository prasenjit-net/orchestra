/* eslint-disable react-refresh/only-export-components */
import { Link } from 'react-router-dom'
import type { WorkflowEvent, WorkflowTask, WorkflowTaskAction } from '../types'

export function formatDate(value?: string) {
  if (!value) {
    return '—'
  }
  return new Date(value).toLocaleString()
}

export function statusClasses(status: string) {
  switch (status) {
    case 'completed':
    case 'ok':
    case 'published':
      return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
    case 'failed':
      return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
    case 'running':
      return 'bg-sky-100 text-sky-700 dark:bg-sky-900/30 dark:text-sky-300'
    case 'draft':
    case 'paused':
    case 'waiting':
      return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
    case 'canceled':
      return 'bg-slate-200 text-slate-700 dark:bg-slate-800 dark:text-slate-200'
    default:
      return 'bg-violet-100 text-violet-700 dark:bg-violet-900/30 dark:text-violet-300'
  }
}

export function availableTaskActions(task: WorkflowTask): WorkflowTaskAction[] {
  switch (task.status) {
    case 'failed':
      return ['retry', 'requeue', 'cancel']
    case 'paused':
      return ['resume', 'cancel']
    case 'waiting':
      return ['requeue', 'cancel']
    case 'pending':
    case 'running':
      return ['pause', 'cancel', 'requeue']
    case 'canceled':
      return ['requeue', 'retry']
    default:
      return []
  }
}

export function actionLabel(action: WorkflowTaskAction) {
  switch (action) {
    case 'retry':
      return 'Retry'
    case 'requeue':
      return 'Requeue'
    case 'pause':
      return 'Pause'
    case 'resume':
      return 'Resume'
    case 'cancel':
      return 'Cancel'
  }
}

export const queueEventTypes = new Set([
  'ActivityScheduled',
  'ActivityWaiting',
  'ActivityWaitingForSignal',
  'ActivityRetryScheduled',
  'TaskRetried',
  'TaskRequeued',
  'TaskPaused',
  'TaskResumed',
  'TaskCanceled',
])

export const lifecycleEventTypes = new Set(['WorkflowStarted', 'ActivityCompleted', 'WorkflowCompleted', 'WorkflowCanceled'])
export const failureEventTypes = new Set(['ActivityFailed', 'WorkflowFailed'])

export function formatEventType(eventType: string) {
  return eventType.replace(/([a-z0-9])([A-Z])/g, '$1 $2')
}

export function eventFilterMatches(filter: 'all' | 'queue' | 'lifecycle' | 'failure', eventType: string) {
  if (filter === 'all') {
    return true
  }
  if (filter === 'queue') {
    return queueEventTypes.has(eventType)
  }
  if (filter === 'lifecycle') {
    return lifecycleEventTypes.has(eventType)
  }
  return failureEventTypes.has(eventType)
}

export function payloadSummary(payload: unknown) {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
    return ''
  }

  const value = payload as Record<string, unknown>
  const parts: string[] = []

  if (typeof value.stepName === 'string' && value.stepName) {
    parts.push(`Step ${value.stepName}`)
  }

  const activity = typeof value.activity === 'string' ? value.activity : typeof value.activityName === 'string' ? value.activityName : ''
  if (activity) {
    parts.push(`Activity ${activity}`)
  }

  if (typeof value.status === 'string' && value.status) {
    parts.push(`Status ${value.status}`)
  }
  if (typeof value.runAt === 'string' && value.runAt) {
    parts.push(`Run at ${formatDate(value.runAt)}`)
  }
  if (typeof value.error === 'string' && value.error) {
    parts.push(value.error)
  }

  return parts.join(' · ')
}

export function EventCard({ event, showWorkflowLink = false }: { event: WorkflowEvent; showWorkflowLink?: boolean }) {
  const summary = payloadSummary(event.payload)

  return (
    <div className="rounded-lg border border-gray-200 p-4 dark:border-slate-800">
      <div className="flex flex-col gap-2 md:flex-row md:items-center md:justify-between">
        <div className="flex flex-wrap items-center gap-3">
          {event.workflowId ? (
            <span className="rounded-full bg-slate-100 px-2.5 py-1 text-xs font-semibold text-slate-700 dark:bg-slate-800 dark:text-slate-200">
              {event.workflowId}
            </span>
          ) : null}
          <span className="rounded-full bg-primary-50 px-2.5 py-1 text-xs font-semibold text-primary-700 dark:bg-primary-900/30 dark:text-primary-200">
            #{event.sequence}
          </span>
          <div className="font-medium text-gray-900 dark:text-slate-100">{formatEventType(event.eventType)}</div>
        </div>
        <div className="flex items-center gap-3">
          <div className="text-xs text-gray-500 dark:text-slate-400">{formatDate(event.createdAt)}</div>
          {showWorkflowLink && event.workflowId ? (
            <Link
              to={`/runs/${event.workflowId}`}
              className="rounded-lg border border-gray-200 px-2.5 py-1 text-xs font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
            >
              Inspect
            </Link>
          ) : null}
        </div>
      </div>
      {summary ? <p className="mt-3 text-sm text-gray-600 dark:text-slate-300">{summary}</p> : null}
      <pre className="mt-3 overflow-x-auto rounded-lg bg-slate-950 p-3 text-xs text-slate-100">{JSON.stringify(event.payload ?? {}, null, 2)}</pre>
    </div>
  )
}
