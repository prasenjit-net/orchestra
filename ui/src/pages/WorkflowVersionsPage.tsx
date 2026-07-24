import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, Check, CheckCircle2, Copy, GitBranch, Play, Send, X } from 'lucide-react'
import { Link, useParams } from 'react-router-dom'
import PublishVersionModal from '../components/PublishVersionModal'
import SectionHeader from '../components/SectionHeader'
import { workflowApi } from '../services/api'
import { formatDate, statusClasses } from './workflowUi'

export default function WorkflowVersionsPage() {
  const { definitionId = '' } = useParams<{ definitionId: string }>()
  const queryClient = useQueryClient()
  const [publishTarget, setPublishTarget] = useState<number | null>(null)
  const [activateTarget, setActivateTarget] = useState<number | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [pageError, setPageError] = useState<string | null>(null)
  const [copiedWebhookVersion, setCopiedWebhookVersion] = useState<number | null>(null)

  const copyVersionWebhook = (version: number) => {
    void navigator.clipboard.writeText(`POST /ext/webhook/${definitionId}/start?version=${version}`)
    setCopiedWebhookVersion(version)
    setTimeout(() => setCopiedWebhookVersion(null), 2000)
  }

  const definitionQuery = useQuery({
    queryKey: ['workflow-definition', definitionId],
    queryFn: () => workflowApi.getDefinition(definitionId),
    enabled: Boolean(definitionId),
  })

  const refreshDefinition = () => {
    void Promise.all([
      queryClient.invalidateQueries({ queryKey: ['workflow-definitions'] }),
      queryClient.invalidateQueries({ queryKey: ['workflow-definition', definitionId] }),
    ])
  }

  const publishMutation = useMutation({
    mutationFn: ({ version, activate }: { version: number; activate: boolean }) =>
      workflowApi.publishDefinitionVersion(definitionId, version, activate),
    onSuccess: (definition, variables) => {
      setPublishTarget(null)
      setPageError(null)
      setNotice(variables.activate
        ? `Published and activated version ${variables.version}.`
        : `Published version ${variables.version}. Active version remains ${definition.activeVersion}.`)
      refreshDefinition()
    },
    onError: (error: Error) => setPageError(error.message),
  })

  const activateMutation = useMutation({
    mutationFn: (version: number) => workflowApi.activateDefinitionVersion(definitionId, version),
    onSuccess: (definition) => {
      setActivateTarget(null)
      setPageError(null)
      setNotice(`Activated version ${definition.activeVersion} for new workflow runs.`)
      refreshDefinition()
    },
    onError: (error: Error) => setPageError(error.message),
  })

  if (definitionQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading workflow versions…</div>
  }
  if (definitionQuery.error || !definitionQuery.data) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Unable to load workflow versions.</div>
  }

  const definition = definitionQuery.data

  return (
    <div className="space-y-6 p-8">
      <Link to="/workflows" className="inline-flex items-center gap-2 text-sm font-medium text-gray-500 hover:text-gray-900 dark:text-slate-400 dark:hover:text-slate-100">
        <ArrowLeft className="h-4 w-4" />
        Back to workflows
      </Link>

      <SectionHeader
        title={`${definition.name} versions`}
        description={`Version ${definition.activeVersion} is active. New runs use the active version; existing runs remain pinned.`}
        action={
          <Link to={`/workflows/${definition.id}/designer?version=${definition.activeVersion}`} className="inline-flex items-center gap-2 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white hover:bg-primary-700">
            <GitBranch className="h-4 w-4" />
            Open designer
          </Link>
        }
      />

      {notice ? (
        <div className="flex items-center gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-700 dark:border-emerald-900/40 dark:bg-emerald-950/30 dark:text-emerald-300">
          <CheckCircle2 className="h-4 w-4" />
          {notice}
        </div>
      ) : null}
      {pageError ? (
        <div className="flex items-center justify-between gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/40 dark:bg-red-950/30 dark:text-red-300">
          <span>{pageError}</span>
          <button type="button" onClick={() => setPageError(null)} title="Dismiss" className="rounded-md p-1 hover:bg-red-100 dark:hover:bg-red-950"><X className="h-4 w-4" /></button>
        </div>
      ) : null}

      <section className="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-slate-800 dark:bg-slate-900">
        <div className="overflow-x-auto">
          <table className="min-w-full divide-y divide-gray-200 dark:divide-slate-800">
            <thead className="bg-gray-50 dark:bg-slate-950/40">
              <tr className="text-left text-[11px] font-semibold uppercase text-gray-500 dark:text-slate-400">
                <th className="px-5 py-3">Version</th>
                <th className="px-5 py-3">State</th>
                <th className="px-5 py-3">Source</th>
                <th className="px-5 py-3">Created</th>
                <th className="px-5 py-3">Published</th>
                <th className="px-5 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100 dark:divide-slate-800">
              {definition.versions.map((version) => {
                const isActive = version.version === definition.activeVersion
                return (
                  <tr key={version.version} className="text-sm text-gray-700 dark:text-slate-300">
                    <td className="whitespace-nowrap px-5 py-4 font-mono font-semibold text-gray-900 dark:text-slate-100">v{version.version}</td>
                    <td className="whitespace-nowrap px-5 py-4">
                      <div className="flex items-center gap-2">
                        <span className={`inline-flex rounded-full px-2.5 py-0.5 text-[11px] font-semibold uppercase ${statusClasses(version.status)}`}>{version.status}</span>
                        {isActive ? <span className="inline-flex rounded-full bg-sky-100 px-2.5 py-0.5 text-[11px] font-semibold uppercase text-sky-700 dark:bg-sky-900/30 dark:text-sky-300">Active</span> : null}
                      </div>
                    </td>
                    <td className="whitespace-nowrap px-5 py-4">{version.basedOnVersion ? `v${version.basedOnVersion}` : 'Initial'}</td>
                    <td className="whitespace-nowrap px-5 py-4 text-xs text-gray-500 dark:text-slate-400">{formatDate(version.createdAt)}</td>
                    <td className="whitespace-nowrap px-5 py-4 text-xs text-gray-500 dark:text-slate-400">{formatDate(version.publishedAt)}</td>
                    <td className="px-5 py-4">
                      <div className="flex justify-end gap-2">
                        {version.status === 'published' ? (
                          <button type="button" onClick={() => copyVersionWebhook(version.version)} title={`Copy webhook URL for version ${version.version}`} className="rounded-lg border border-gray-200 p-1.5 text-gray-500 hover:bg-gray-50 dark:border-slate-700 dark:text-slate-400 dark:hover:bg-slate-800">
                            {copiedWebhookVersion === version.version ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
                          </button>
                        ) : null}
                        <Link to={`/workflows/${definition.id}/designer?version=${version.version}`} className="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 px-3 py-1.5 text-xs font-semibold text-gray-700 hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800">
                          <GitBranch className="h-3.5 w-3.5" />
                          Open
                        </Link>
                        {version.status === 'draft' ? (
                          <button type="button" onClick={() => { setPageError(null); setPublishTarget(version.version) }} className="inline-flex items-center gap-1.5 rounded-lg border border-amber-200 px-3 py-1.5 text-xs font-semibold text-amber-700 hover:bg-amber-50 dark:border-amber-800/50 dark:text-amber-300 dark:hover:bg-amber-950/20">
                            <Send className="h-3.5 w-3.5" />
                            Publish
                          </button>
                        ) : !isActive ? (
                          <button type="button" onClick={() => { setPageError(null); setActivateTarget(version.version) }} className="inline-flex items-center gap-1.5 rounded-lg border border-emerald-200 px-3 py-1.5 text-xs font-semibold text-emerald-700 hover:bg-emerald-50 dark:border-emerald-800/50 dark:text-emerald-300 dark:hover:bg-emerald-950/20">
                            <Play className="h-3.5 w-3.5" />
                            Activate
                          </button>
                        ) : null}
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </section>

      {publishTarget !== null ? (
        <PublishVersionModal
          version={publishTarget}
          activeVersion={definition.activeVersion}
          isPending={publishMutation.isPending}
          error={publishMutation.error instanceof Error ? publishMutation.error.message : null}
          onClose={() => setPublishTarget(null)}
          onPublish={(activate) => publishMutation.mutate({ version: publishTarget, activate })}
        />
      ) : null}

      {activateTarget !== null ? (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/55 p-4" role="dialog" aria-modal="true" aria-labelledby="activate-version-title">
          <div className="w-full max-w-md rounded-lg border border-gray-200 bg-white shadow-xl dark:border-slate-700 dark:bg-slate-900">
            <div className="px-5 py-4">
              <h2 id="activate-version-title" className="text-base font-semibold text-gray-900 dark:text-slate-100">Activate version {activateTarget}?</h2>
              <p className="mt-2 text-sm text-gray-500 dark:text-slate-400">New runs will use version {activateTarget}. Existing runs will continue on the version they started with.</p>
              {activateMutation.error instanceof Error ? <p className="mt-3 text-sm text-red-600 dark:text-red-300">{activateMutation.error.message}</p> : null}
            </div>
            <div className="flex justify-end gap-2 border-t border-gray-200 px-5 py-4 dark:border-slate-800">
              <button type="button" onClick={() => setActivateTarget(null)} disabled={activateMutation.isPending} className="rounded-lg border border-gray-200 px-3 py-2 text-sm font-semibold text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800">Cancel</button>
              <button type="button" onClick={() => activateMutation.mutate(activateTarget)} disabled={activateMutation.isPending} className="inline-flex items-center gap-2 rounded-lg bg-emerald-600 px-3 py-2 text-sm font-semibold text-white hover:bg-emerald-700 disabled:opacity-50">
                <Play className="h-4 w-4" />
                {activateMutation.isPending ? 'Activating…' : 'Activate'}
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  )
}
