import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowRight, Clock3, GitBranch, ListChecks, PencilRuler, Play, Wand2 } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import SectionHeader from '../components/SectionHeader'
import StatCard from '../components/StatCard'
import { workflowApi } from '../services/api'
import { formatDate, statusClasses } from './workflowUi'

export default function WorkflowListPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [notice, setNotice] = useState<string | null>(null)
  const [pageError, setPageError] = useState<string | null>(null)

  const definitionsQuery = useQuery({
    queryKey: ['workflow-definitions'],
    queryFn: workflowApi.listDefinitions,
  })
  const workflowsQuery = useQuery({
    queryKey: ['workflows'],
    queryFn: () => workflowApi.listWorkflows(),
  })
  const tasksQuery = useQuery({
    queryKey: ['workflow-tasks'],
    queryFn: () => workflowApi.listTasks(),
  })

  const startWorkflowMutation = useMutation({
    mutationFn: workflowApi.startWorkflow,
    onSuccess: () => {
      setPageError(null)
      setNotice('Workflow instance started.')
      void Promise.all([
        queryClient.invalidateQueries({ queryKey: ['workflows'] }),
        queryClient.invalidateQueries({ queryKey: ['workflow-tasks'] }),
      ])
    },
    onError: (error: Error) => {
      setNotice(null)
      setPageError(error.message)
    },
  })

  const publishDraftMutation = useMutation({
    mutationFn: ({ definitionId, version }: { definitionId: string; version: number }) =>
      workflowApi.publishDefinitionVersion(definitionId, version),
    onSuccess: (definition) => {
      setPageError(null)
      setNotice(`Published ${definition.name} v${definition.activeVersion}.`)
      void queryClient.invalidateQueries({ queryKey: ['workflow-definitions'] })
    },
    onError: (error: Error) => {
      setNotice(null)
      setPageError(error.message)
    },
  })

  if (definitionsQuery.isLoading || workflowsQuery.isLoading || tasksQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading workflows…</div>
  }

  if (definitionsQuery.error || workflowsQuery.error || tasksQuery.error) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Unable to load workflow overview.</div>
  }

  const definitions = definitionsQuery.data?.definitions ?? []
  const workflows = workflowsQuery.data?.workflows ?? []
  const tasks = tasksQuery.data?.tasks ?? []

  const stats = {
    definitions: definitions.length,
    drafts: definitions.filter((definition) => Boolean(definition.draftVersion)).length,
    running: workflows.filter((workflow) => workflow.status === 'running').length,
    queued: tasks.filter((task) => task.status === 'pending' || task.status === 'running' || task.status === 'paused' || task.status === 'waiting').length,
  }

  return (
    <div className="space-y-8 p-8">
      <SectionHeader
        title="Workflows"
        description="Browse versioned workflow definitions and launch the dedicated visual designer."
        action={
          <div className="flex flex-wrap gap-3">
            <button
              type="button"
              onClick={() => navigate('/operations')}
              className="rounded-lg border border-gray-200 px-4 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
            >
              Operations console
            </button>
            <button
              type="button"
              onClick={() => navigate('/workflows/new')}
              className="inline-flex items-center gap-2 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700"
            >
              <PencilRuler className="h-4 w-4" />
              New workflow
            </button>
          </div>
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
        <StatCard label="Definitions" value={String(stats.definitions)} description="Versioned workflow definitions available to start." icon={GitBranch} tone="bg-sky-50 text-sky-600 dark:bg-sky-900/20 dark:text-sky-300" />
        <StatCard label="Drafts" value={String(stats.drafts)} description="Definitions with an unpublished draft waiting on review." icon={Wand2} tone="bg-amber-50 text-amber-600 dark:bg-amber-900/20 dark:text-amber-300" />
        <StatCard label="Running" value={String(stats.running)} description="Workflow instances currently executing." icon={Play} tone="bg-emerald-50 text-emerald-600 dark:bg-emerald-900/20 dark:text-emerald-300" />
        <StatCard label="Queued tasks" value={String(stats.queued)} description="Pending, running, or paused tasks in the durable queue." icon={Clock3} tone="bg-violet-50 text-violet-600 dark:bg-violet-900/20 dark:text-violet-300" />
      </div>

      <section className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <div className="flex items-center justify-between gap-3">
          <div>
            <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">Workflow definitions</h2>
            <p className="mt-1 text-sm text-gray-500 dark:text-slate-400">Open the designer, publish drafts, or start a workflow directly from here.</p>
          </div>
          <button
            type="button"
            onClick={() => navigate('/workflows/new')}
            className="rounded-lg border border-gray-200 px-3 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
          >
            Create definition
          </button>
        </div>
        <div className="mt-4 grid grid-cols-1 gap-4 xl:grid-cols-2">
          {definitions.length === 0 ? (
            <div className="rounded-lg border border-dashed border-gray-300 p-6 text-sm text-gray-500 dark:border-slate-700 dark:text-slate-400">
              No workflow definitions yet. Start with the visual designer to create your first workflow.
            </div>
          ) : (
            definitions.map((definition) => (
              <div key={definition.id} className="rounded-xl border border-gray-200 p-5 dark:border-slate-800">
                <div className="flex items-start justify-between gap-4">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <h3 className="truncate text-base font-semibold text-gray-900 dark:text-slate-100">{definition.name}</h3>
                      <span className={`inline-flex rounded-full px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide ${statusClasses(definition.status)}`}>
                        {definition.status}
                      </span>
                      {definition.draftVersion ? (
                        <span className="inline-flex rounded-full bg-amber-100 px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide text-amber-700 dark:bg-amber-900/30 dark:text-amber-300">
                          Draft v{definition.draftVersion}
                        </span>
                      ) : null}
                    </div>
                    <p className="mt-2 text-sm text-gray-500 dark:text-slate-400">{definition.description || 'No description yet.'}</p>
                  </div>
                </div>
                <div className="mt-4 grid grid-cols-1 gap-2 text-sm text-gray-500 dark:text-slate-400 md:grid-cols-2">
                  <div>ID: {definition.id}</div>
                  <div>Active version: v{definition.activeVersion}</div>
                  <div>Latest version: v{definition.latestVersion}</div>
                  <div>Updated: {formatDate(definition.updatedAt)}</div>
                </div>
                <div className="mt-5 flex flex-wrap gap-2">
                  <button
                    type="button"
                    onClick={() => navigate(`/workflows/${definition.id}/designer`)}
                    className="inline-flex items-center gap-2 rounded-lg bg-primary-600 px-3 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700"
                  >
                    Open designer
                  </button>
                  <button
                    type="button"
                    onClick={() => startWorkflowMutation.mutate(definition.id)}
                    disabled={startWorkflowMutation.isPending}
                    className="inline-flex items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-60 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
                  >
                    <Play className="h-4 w-4" />
                    Start workflow
                  </button>
                  {definition.draftVersion ? (
                    <button
                      type="button"
                      onClick={() => publishDraftMutation.mutate({ definitionId: definition.id, version: definition.draftVersion as number })}
                      disabled={publishDraftMutation.isPending}
                      className="inline-flex items-center gap-2 rounded-lg border border-amber-200 px-3 py-2 text-sm font-semibold text-amber-700 transition-colors hover:bg-amber-50 disabled:cursor-not-allowed disabled:opacity-60 dark:border-amber-800/50 dark:text-amber-300 dark:hover:bg-amber-950/20"
                    >
                      Publish draft
                    </button>
                  ) : null}
                </div>
              </div>
            ))
          )}
        </div>
      </section>

      <section className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <div className="flex items-center justify-between gap-3">
          <div>
            <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">Recent workflow instances</h2>
            <p className="mt-1 text-sm text-gray-500 dark:text-slate-400">Jump into the operations console for detailed queue, history, and instance inspection.</p>
          </div>
          <button
            type="button"
            onClick={() => navigate('/operations')}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
          >
            <ListChecks className="h-4 w-4" />
            View operations
          </button>
        </div>
        <div className="mt-4 space-y-3">
          {workflows.length === 0 ? (
            <p className="text-sm text-gray-500 dark:text-slate-400">No workflow instances yet.</p>
          ) : (
            workflows.slice(0, 8).map((workflow) => (
              <div key={workflow.id} className="flex flex-col gap-3 rounded-lg border border-gray-200 p-4 dark:border-slate-800 md:flex-row md:items-center md:justify-between">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <div className="truncate font-medium text-gray-900 dark:text-slate-100">{workflow.id}</div>
                    <span className={`inline-flex rounded-full px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide ${statusClasses(workflow.status)}`}>
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
                <button
                  type="button"
                  onClick={() => navigate(`/runs/${workflow.id}`)}
                  className="inline-flex items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
                >
                  Inspect
                  <ArrowRight className="h-4 w-4" />
                </button>
              </div>
            ))
          )}
        </div>
      </section>
    </div>
  )
}
