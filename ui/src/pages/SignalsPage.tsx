import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { BellRing, CheckCircle2, Clock3, Send, Workflow } from 'lucide-react'
import { Link } from 'react-router-dom'
import SectionHeader from '../components/SectionHeader'
import StatCard from '../components/StatCard'
import { workflowApi } from '../services/api'
import type { WorkflowDefinitionDetails, WorkflowInstance, WorkflowStepDefinition } from '../types'
import { formatDate, statusClasses } from './workflowUi'

const waitingActivities = new Set(['wait-signal', 'approval', 'manual-task', 'human-wait'])

type WaitingSignalWorkflow = {
  workflow: WorkflowInstance
  signalName: string
  step?: WorkflowStepDefinition
}

function resolveSignalName(activity: string, input: unknown) {
  if (input && typeof input === 'object' && !Array.isArray(input)) {
    const signal = (input as Record<string, unknown>).signal
    if (typeof signal === 'string' && signal.trim()) {
      return signal.trim()
    }
  }

  switch (activity) {
    case 'approval':
      return 'approval'
    case 'manual-task':
      return 'manual-complete'
    case 'human-wait':
      return 'resume'
    default:
      return 'signal'
  }
}

async function loadWaitingSignalWorkflows() {
  const workflowsResponse = await workflowApi.listWorkflows({ status: 'running' })
  const waitingWorkflows = (workflowsResponse.workflows ?? []).filter(
    (workflow) => workflow.status === 'running' && waitingActivities.has(workflow.currentActivity),
  )

  const definitionIDs = [...new Set(waitingWorkflows.map((workflow) => workflow.definitionId))]
  const definitionEntries = await Promise.all(
    definitionIDs.map(async (definitionId) => [definitionId, await workflowApi.getDefinition(definitionId)] as const),
  )
  const definitions = new Map<string, WorkflowDefinitionDetails>(definitionEntries)

  return waitingWorkflows.map((workflow) => {
    const definition = definitions.get(workflow.definitionId)
    const step =
      definition?.document.steps.find((candidate) => candidate.name === workflow.currentStepName) ??
      definition?.document.steps[workflow.currentStepIndex]

    return {
      workflow,
      step,
      signalName: resolveSignalName(workflow.currentActivity, step?.input),
    } satisfies WaitingSignalWorkflow
  })
}

function parseSignalPayload(payloadText: string) {
  const trimmed = payloadText.trim()
  if (!trimmed) {
    return {}
  }
  return JSON.parse(trimmed) as unknown
}

export default function SignalsPage() {
  const queryClient = useQueryClient()
  const [payloadText, setPayloadText] = useState('{\n  "approved": true\n}')
  const [notice, setNotice] = useState<string | null>(null)
  const [pageError, setPageError] = useState<string | null>(null)

  const waitingQuery = useQuery({
    queryKey: ['waiting-signal-workflows'],
    queryFn: loadWaitingSignalWorkflows,
  })

  const waitingWorkflows = useMemo(() => waitingQuery.data ?? [], [waitingQuery.data])

  const signalOneMutation = useMutation({
    mutationFn: async ({ workflowId, signalName, payload }: { workflowId: string; signalName: string; payload: unknown }) =>
      workflowApi.signalWorkflow(workflowId, { name: signalName, payload }),
    onSuccess: (_, variables) => {
      setPageError(null)
      setNotice(`Sent ${variables.signalName} to ${variables.workflowId}.`)
      // Cancel any in-flight refetch before applying the optimistic removal so the
      // result of that fetch cannot overwrite our update.
      void queryClient.cancelQueries({ queryKey: ['waiting-signal-workflows'] })
      queryClient.setQueryData<WaitingSignalWorkflow[]>(['waiting-signal-workflows'], (old) =>
        (old ?? []).filter((item) => item.workflow.id !== variables.workflowId),
      )
      void queryClient.invalidateQueries({ queryKey: ['workflows'] })
      // Re-validate the waiting list after a short delay so the backend worker has
      // had time to advance the workflow before we query again.
      setTimeout(() => void queryClient.invalidateQueries({ queryKey: ['waiting-signal-workflows'] }), 2500)
    },
    onError: (error: Error) => {
      setNotice(null)
      setPageError(error.message)
    },
  })

  const signalAllMutation = useMutation({
    mutationFn: async ({ items, payload }: { items: WaitingSignalWorkflow[]; payload: unknown }) => {
      await Promise.all(items.map((item) => workflowApi.signalWorkflow(item.workflow.id, { name: item.signalName, payload })))
      return items.length
    },
    onSuccess: (count) => {
      setPageError(null)
      setNotice(`Sent signals to ${count} waiting workflow${count === 1 ? '' : 's'}.`)
      void queryClient.cancelQueries({ queryKey: ['waiting-signal-workflows'] })
      queryClient.setQueryData<WaitingSignalWorkflow[]>(['waiting-signal-workflows'], [])
      void queryClient.invalidateQueries({ queryKey: ['workflows'] })
      setTimeout(() => void queryClient.invalidateQueries({ queryKey: ['waiting-signal-workflows'] }), 2500)
    },
    onError: (error: Error) => {
      setNotice(null)
      setPageError(error.message)
    },
  })

  const submitAllSignals = () => {
    try {
      const payload = parseSignalPayload(payloadText)
      setPageError(null)
      signalAllMutation.mutate({ items: waitingWorkflows, payload })
    } catch (error) {
      setNotice(null)
      setPageError(error instanceof Error ? error.message : 'Signal payload must be valid JSON.')
    }
  }

  const submitSingleSignal = (item: WaitingSignalWorkflow) => {
    try {
      const payload = parseSignalPayload(payloadText)
      setPageError(null)
      signalOneMutation.mutate({ workflowId: item.workflow.id, signalName: item.signalName, payload })
    } catch (error) {
      setNotice(null)
      setPageError(error instanceof Error ? error.message : 'Signal payload must be valid JSON.')
    }
  }

  if (waitingQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading signal console…</div>
  }

  if (waitingQuery.error) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Unable to load waiting workflows.</div>
  }

  return (
    <div className="space-y-8 p-8">
      <SectionHeader
        title="Signals"
        description="Find workflows blocked on signal-driven steps and resume them from one operator console."
        action={
          <button
            type="button"
            onClick={submitAllSignals}
            disabled={waitingWorkflows.length === 0 || signalAllMutation.isPending}
            className="inline-flex items-center gap-2 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700 disabled:cursor-not-allowed disabled:opacity-60"
          >
            <Send className="h-4 w-4" />
            Signal all waiting
          </button>
        }
      />

      {notice ? (
        <div className="rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-700 dark:border-emerald-900/40 dark:bg-emerald-950/30 dark:text-emerald-300">
          {notice}
        </div>
      ) : null}
      {pageError ? (
        <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/40 dark:bg-red-950/30 dark:text-red-300">
          {pageError}
        </div>
      ) : null}

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Waiting runs" value={String(waitingWorkflows.length)} description="Workflows currently blocked on a signal step." icon={Workflow} tone="bg-sky-50 text-sky-600 dark:bg-sky-900/20 dark:text-sky-300" />
        <StatCard label="Approval waits" value={String(waitingWorkflows.filter((item) => item.workflow.currentActivity === 'approval').length)} description="Runs waiting for an approval decision." icon={CheckCircle2} tone="bg-emerald-50 text-emerald-600 dark:bg-emerald-900/20 dark:text-emerald-300" />
        <StatCard label="Manual waits" value={String(waitingWorkflows.filter((item) => item.workflow.currentActivity === 'manual-task' || item.workflow.currentActivity === 'human-wait').length)} description="Runs waiting for manual or resume signals." icon={BellRing} tone="bg-amber-50 text-amber-600 dark:bg-amber-900/20 dark:text-amber-300" />
        <StatCard label="Custom signal waits" value={String(waitingWorkflows.filter((item) => item.workflow.currentActivity === 'wait-signal').length)} description="Runs waiting on an explicit signal name." icon={Clock3} tone="bg-violet-50 text-violet-600 dark:bg-violet-900/20 dark:text-violet-300" />
      </div>

      <section className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">Shared signal payload</h2>
        <p className="mt-1 text-sm text-gray-500 dark:text-slate-400">
          This JSON payload is sent to each selected waiting workflow with its expected signal name.
        </p>
        <textarea
          rows={8}
          value={payloadText}
          onChange={(event) => setPayloadText(event.target.value)}
          className="mt-4 w-full rounded-lg border border-gray-200 bg-slate-950 px-3 py-2 font-mono text-sm text-slate-100 outline-none transition-colors focus:border-primary-500 dark:border-slate-700"
        />
      </section>

      <section className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <div className="flex items-center justify-between gap-3">
          <div>
            <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">Waiting workflows</h2>
            <p className="mt-1 text-sm text-gray-500 dark:text-slate-400">Each row shows the current waiting step and the signal that will unblock it.</p>
          </div>
        </div>

        <div className="mt-4 space-y-3">
          {waitingWorkflows.length === 0 ? (
            <div className="rounded-lg border border-dashed border-gray-300 p-6 text-sm text-gray-500 dark:border-slate-700 dark:text-slate-400">
              No workflows are currently waiting on signals.
            </div>
          ) : (
            waitingWorkflows.map((item) => (
              <div key={item.workflow.id} className="flex flex-col gap-4 rounded-lg border border-gray-200 p-4 dark:border-slate-800 lg:flex-row lg:items-center lg:justify-between">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <div className="truncate font-medium text-gray-900 dark:text-slate-100">{item.workflow.id}</div>
                    <span className={`inline-flex rounded-full px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide ${statusClasses(item.workflow.status)}`}>
                      {item.workflow.status}
                    </span>
                    <span className="inline-flex rounded-full bg-primary-50 px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide text-primary-700 dark:bg-primary-900/30 dark:text-primary-200">
                      {item.workflow.currentActivity}
                    </span>
                  </div>
                  <div className="mt-2 grid grid-cols-1 gap-2 text-sm text-gray-500 dark:text-slate-400 md:grid-cols-2 xl:grid-cols-4">
                    <div>Definition: {item.workflow.definitionId}</div>
                    <div>Step: {item.workflow.currentStepName || '—'}</div>
                    <div>Signal: {item.signalName}</div>
                    <div>Updated: {formatDate(item.workflow.updatedAt)}</div>
                  </div>
                </div>
                <div className="flex flex-wrap gap-2">
                  <Link
                    to={`/runs/${item.workflow.id}`}
                    className="rounded-lg border border-gray-200 px-3 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
                  >
                    Open run
                  </Link>
                  <button
                    type="button"
                    onClick={() => submitSingleSignal(item)}
                    disabled={signalOneMutation.isPending}
                    className="inline-flex items-center gap-2 rounded-lg bg-primary-600 px-3 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700 disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    <Send className="h-4 w-4" />
                    Send {item.signalName}
                  </button>
                </div>
              </div>
            ))
          )}
        </div>
      </section>
    </div>
  )
}
