import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy, Download, GitBranch, PencilRuler, Play, Plus, Trash2, Upload, Wand2 } from 'lucide-react'
import { Link, useNavigate } from 'react-router-dom'
import ImportModal from '../components/ImportModal'
import SectionHeader from '../components/SectionHeader'
import StatCard from '../components/StatCard'
import StartWorkflowModal from '../components/StartWorkflowModal'
import { useImport } from '../hooks/useImport'
import { downloadBundle, importExportApi, workflowApi } from '../services/api'
import { ConfirmDeleteDialog, formatDate, statusClasses, InUseDialog } from './workflowUi'
import { EntityInUseError } from '../types'
import type { WorkflowDefinitionSummary } from '../types'

function WebhookUrl({ definitionId }: { definitionId: string }) {
  const [copied, setCopied] = useState(false)
  const url = `POST /ext/webhook/${definitionId}/start`

  function copy() {
    void navigator.clipboard.writeText(url)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div className="flex items-center gap-2 rounded-lg bg-gray-50 px-3 py-2 dark:bg-slate-800">
      <span className="min-w-0 flex-1 truncate font-mono text-xs text-gray-600 dark:text-slate-300">{url}</span>
      <button
        type="button"
        onClick={copy}
        className="shrink-0 text-gray-400 transition-colors hover:text-gray-600 dark:text-slate-500 dark:hover:text-slate-300"
        title="Copy webhook URL"
      >
        {copied ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
      </button>
    </div>
  )
}

export default function WorkflowListPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [notice, setNotice] = useState<string | null>(null)
  const [pageError, setPageError] = useState<string | null>(null)
  const [startTarget, setStartTarget] = useState<WorkflowDefinitionSummary | null>(null)
  const [startError, setStartError] = useState<string | null>(null)
  const [inUseError, setInUseError] = useState<InstanceType<typeof EntityInUseError> | null>(null)
  const [confirmDeleteTarget, setConfirmDeleteTarget] = useState<WorkflowDefinitionSummary | null>(null)

  const importHook = useImport(() => {
    setNotice('Import successful.')
    void queryClient.invalidateQueries({ queryKey: ['workflow-definitions'] })
  })

  const definitionsQuery = useQuery({
    queryKey: ['workflow-definitions'],
    queryFn: workflowApi.listDefinitions,
  })

  const deleteDefinitionMutation = useMutation({
    mutationFn: (id: string) => workflowApi.deleteDefinition(id),
    onSuccess: () => {
      setNotice('Workflow definition deleted.')
      void queryClient.invalidateQueries({ queryKey: ['workflow-definitions'] })
    },
    onError: (err: unknown) => {
      if (err instanceof EntityInUseError) {
        setInUseError(err)
      } else {
        setPageError(err instanceof Error ? err.message : String(err))
      }
    },
  })

  const startWorkflowMutation = useMutation({
    mutationFn: ({ definitionId, input, callbackUrl }: { definitionId: string; input: Record<string, unknown>; callbackUrl: string }) =>
      workflowApi.startWorkflow(definitionId, {
        input: Object.keys(input).length > 0 ? input : undefined,
        callbackUrl: callbackUrl || undefined,
      }),
    onSuccess: (instance) => {
      setStartTarget(null)
      setStartError(null)
      setPageError(null)
      setNotice('Workflow instance started.')
      void queryClient.invalidateQueries({ queryKey: ['workflows'] })
      void queryClient.invalidateQueries({ queryKey: ['workflow-tasks'] })
      setTimeout(() => navigate(`/runs/${instance.id}`), 800)
    },
    onError: (error: Error) => {
      setStartError(error.message)
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

  if (definitionsQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading workflows…</div>
  }

  if (definitionsQuery.error) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Unable to load workflow definitions.</div>
  }

  const definitions = definitionsQuery.data?.definitions ?? []
  const published = definitions.filter((d) => d.status === 'published').length
  const drafts = definitions.filter((d) => Boolean(d.draftVersion)).length

  return (
    <div className="space-y-8 p-8">
      <SectionHeader
        title="Workflows"
        description="Manage versioned workflow definitions, publish drafts, and start new runs."
        action={
          <div className="flex items-center gap-2">
            <input ref={importHook.fileInputRef} type="file" accept=".json" className="hidden" onChange={importHook.onFileChange} />
            <button
              type="button"
              onClick={importHook.openFilePicker}
              disabled={importHook.state.isAnalyzing || importHook.state.isApplying}
              className="inline-flex items-center gap-2 rounded-lg border border-gray-200 px-4 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 disabled:opacity-60 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
            >
              <Upload className="h-4 w-4" />
              {importHook.state.isAnalyzing ? 'Analyzing…' : importHook.state.isApplying ? 'Importing…' : 'Import'}
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

      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        <StatCard
          label="Definitions"
          value={String(definitions.length)}
          description="Total versioned workflow definitions."
          icon={GitBranch}
          tone="bg-sky-50 text-sky-600 dark:bg-sky-900/20 dark:text-sky-300"
        />
        <StatCard
          label="Published"
          value={String(published)}
          description="Definitions with an active published version."
          icon={Play}
          tone="bg-emerald-50 text-emerald-600 dark:bg-emerald-900/20 dark:text-emerald-300"
        />
        <StatCard
          label="Pending drafts"
          value={String(drafts)}
          description="Definitions with an unpublished draft version."
          icon={Wand2}
          tone="bg-amber-50 text-amber-600 dark:bg-amber-900/20 dark:text-amber-300"
        />
      </div>

      <section>
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">
            Definitions
            {definitions.length > 0 && (
              <span className="ml-2 rounded-full bg-gray-100 px-2 py-0.5 text-xs font-semibold text-gray-600 dark:bg-slate-800 dark:text-slate-300">
                {definitions.length}
              </span>
            )}
          </h2>
          <button
            type="button"
            onClick={() => navigate('/workflows/new')}
            className="inline-flex items-center gap-1.5 text-sm font-semibold text-primary-600 hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
          >
            <Plus className="h-4 w-4" />
            Create
          </button>
        </div>

        {definitions.length === 0 ? (
          <div className="mt-4 rounded-xl border border-dashed border-gray-300 p-10 text-center dark:border-slate-700">
            <GitBranch className="mx-auto h-8 w-8 text-gray-400 dark:text-slate-500" />
            <p className="mt-3 text-sm font-medium text-gray-900 dark:text-slate-100">No workflow definitions yet</p>
            <p className="mt-1 text-sm text-gray-500 dark:text-slate-400">Use the visual designer to create your first workflow.</p>
            <button
              type="button"
              onClick={() => navigate('/workflows/new')}
              className="mt-4 inline-flex items-center gap-2 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700"
            >
              <PencilRuler className="h-4 w-4" />
              Open designer
            </button>
          </div>
        ) : (
          <div className="mt-4 grid grid-cols-1 gap-4 xl:grid-cols-2">
            {definitions.map((definition) => (
              <div key={definition.id} className="flex flex-col rounded-xl border border-gray-200 bg-white dark:border-slate-800 dark:bg-slate-900">
                {/* Card header */}
                <div className="flex items-start justify-between gap-4 p-5">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <h3 className="truncate text-base font-semibold text-gray-900 dark:text-slate-100">{definition.name}</h3>
                      <span className={`inline-flex rounded-full px-2.5 py-0.5 text-[11px] font-semibold uppercase tracking-wide ${statusClasses(definition.status)}`}>
                        {definition.status}
                      </span>
                      {definition.draftVersion ? (
                        <span className="inline-flex rounded-full bg-amber-100 px-2.5 py-0.5 text-[11px] font-semibold uppercase tracking-wide text-amber-700 dark:bg-amber-900/30 dark:text-amber-300">
                          Draft v{definition.draftVersion}
                        </span>
                      ) : null}
                    </div>
                    {definition.description ? (
                      <p className="mt-1.5 text-sm text-gray-500 dark:text-slate-400">{definition.description}</p>
                    ) : null}
                  </div>
                </div>

                {/* Webhook URL */}
                <div className="border-t border-gray-100 px-5 py-3 dark:border-slate-800">
                  <p className="mb-1.5 text-[10px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Webhook</p>
                  <WebhookUrl definitionId={definition.id} />
                </div>

                {/* Metadata strip */}
                <div className="grid grid-cols-2 gap-x-4 gap-y-1 border-t border-gray-100 px-5 py-3 text-xs text-gray-500 dark:border-slate-800 dark:text-slate-400">
                  <div><span className="font-medium text-gray-700 dark:text-slate-300">Active</span> v{definition.activeVersion}</div>
                  <div><span className="font-medium text-gray-700 dark:text-slate-300">Latest</span> v{definition.latestVersion}</div>
                  <div className="col-span-2 truncate"><span className="font-medium text-gray-700 dark:text-slate-300">ID</span> {definition.id}</div>
                  <div className="col-span-2"><span className="font-medium text-gray-700 dark:text-slate-300">Updated</span> {formatDate(definition.updatedAt)}</div>
                </div>

                {/* Actions */}
                <div className="flex flex-wrap items-center gap-2 border-t border-gray-100 px-5 py-3 dark:border-slate-800">
                  <button
                    type="button"
                    onClick={() => navigate(`/workflows/${definition.id}/designer`)}
                    className="inline-flex items-center gap-1.5 rounded-lg bg-primary-600 px-3 py-1.5 text-sm font-semibold text-white transition-colors hover:bg-primary-700"
                  >
                    <PencilRuler className="h-3.5 w-3.5" />
                    Designer
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      setStartError(null)
                      setStartTarget(definition)
                    }}
                    disabled={definition.status !== 'published'}
                    title={definition.status !== 'published' ? 'Publish a version first' : undefined}
                    className="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 px-3 py-1.5 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
                  >
                    <Play className="h-3.5 w-3.5" />
                    Start run
                  </button>
                  {definition.draftVersion ? (
                    <button
                      type="button"
                      onClick={() => publishDraftMutation.mutate({ definitionId: definition.id, version: definition.draftVersion as number })}
                      disabled={publishDraftMutation.isPending}
                      className="inline-flex items-center gap-1.5 rounded-lg border border-amber-200 px-3 py-1.5 text-sm font-semibold text-amber-700 transition-colors hover:bg-amber-50 disabled:cursor-not-allowed disabled:opacity-60 dark:border-amber-800/50 dark:text-amber-300 dark:hover:bg-amber-950/20"
                    >
                      <Wand2 className="h-3.5 w-3.5" />
                      Publish draft
                    </button>
                  ) : null}
                  <button
                    type="button"
                    title="Export workflow"
                    onClick={async () => {
                      const bundle = await importExportApi.exportWorkflow(definition.id)
                      downloadBundle(bundle, definition.name)
                    }}
                    className="rounded-lg border border-gray-200 p-1.5 text-gray-400 transition-colors hover:bg-gray-50 hover:text-gray-600 dark:border-slate-700 dark:text-slate-500 dark:hover:bg-slate-800 dark:hover:text-slate-300"
                  >
                    <Download className="h-3.5 w-3.5" />
                  </button>
                  <button
                    type="button"
                    title="Delete workflow definition"
                    onClick={() => setConfirmDeleteTarget(definition)}
                    disabled={deleteDefinitionMutation.isPending}
                    className="rounded-lg border border-red-200 p-1.5 text-red-300 transition-colors hover:bg-red-50 hover:text-red-500 disabled:opacity-50 dark:border-red-900/40 dark:text-red-700 dark:hover:bg-red-950/20 dark:hover:text-red-400"
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                  <Link
                    to={`/runs?definitionId=${definition.id}`}
                    className="ml-auto text-xs font-semibold text-gray-500 hover:text-gray-700 dark:text-slate-400 dark:hover:text-slate-200"
                  >
                    View runs →
                  </Link>
                </div>
              </div>
            ))}
          </div>
        )}
      </section>

      {startTarget && (
        <StartWorkflowModal
          definitionName={startTarget.name}
          isPending={startWorkflowMutation.isPending}
          error={startError}
          onClose={() => {
            setStartTarget(null)
            setStartError(null)
          }}
          onStart={(input, callbackUrl) => {
            startWorkflowMutation.mutate({ definitionId: startTarget.id, input, callbackUrl })
          }}
        />
      )}

      {importHook.state.analysis && (
        <ImportModal
          analysis={importHook.state.analysis}
          isPending={importHook.state.isApplying}
          onConfirm={importHook.confirm}
          onClose={importHook.close}
        />
      )}
      {importHook.state.error && (
        <div className="fixed bottom-4 right-4 z-50 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 shadow-lg dark:border-red-900/40 dark:bg-red-950/30 dark:text-red-300">
          Import error: {importHook.state.error}
        </div>
      )}

      {confirmDeleteTarget && (
        <ConfirmDeleteDialog
          name={confirmDeleteTarget.name}
          isPending={deleteDefinitionMutation.isPending}
          onConfirm={() => { deleteDefinitionMutation.mutate(confirmDeleteTarget.id); setConfirmDeleteTarget(null) }}
          onClose={() => setConfirmDeleteTarget(null)}
        />
      )}
      {inUseError && <InUseDialog error={inUseError} onClose={() => setInUseError(null)} />}
    </div>
  )
}
