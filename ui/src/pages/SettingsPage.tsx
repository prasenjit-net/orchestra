import { useState, useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, RefreshCw, RotateCcw, Save } from 'lucide-react'
import Editor from '@monaco-editor/react'
import SectionHeader from '../components/SectionHeader'
import { adminApi, configApi, metaApi } from '../services/api'
import { useMonacoTheme } from '../hooks/useMonacoTheme'

export default function SettingsPage() {
  const queryClient = useQueryClient()
  const monacoTheme = useMonacoTheme()

  const metaQuery = useQuery({ queryKey: ['meta'], queryFn: metaApi.get })
  const configQuery = useQuery({ queryKey: ['config-raw'], queryFn: configApi.getRaw })

  const [configContent, setConfigContent] = useState<string>('')
  const [saveNotice, setSaveNotice] = useState<string | null>(null)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [restartCountdown, setRestartCountdown] = useState<number | null>(null)

  useEffect(() => {
    if (configQuery.data) {
      setConfigContent(configQuery.data.content)
    }
  }, [configQuery.data])

  const restartMutation = useMutation({
    mutationFn: () => adminApi.restart(),
    onSuccess: () => {
      let count = 5
      setRestartCountdown(count)
      const interval = setInterval(() => {
        count--
        if (count <= 0) {
          clearInterval(interval)
          setRestartCountdown(null)
          window.location.reload()
        } else {
          setRestartCountdown(count)
        }
      }, 1000)
    },
  })

  const saveMutation = useMutation({
    mutationFn: () => configApi.putRaw(configContent),
    onSuccess: (result) => {
      setSaveError(null)
      setSaveNotice(`Saved to ${result.path}. Restart the server to apply changes.`)
      void queryClient.invalidateQueries({ queryKey: ['config-raw'] })
      setTimeout(() => setSaveNotice(null), 6000)
    },
    onError: (error: Error) => {
      setSaveNotice(null)
      setSaveError(error.message)
    },
  })

  const isDirty = configQuery.data ? configContent !== configQuery.data.content : false

  if (metaQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading…</div>
  }
  if (metaQuery.error || !metaQuery.data) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Failed to load application metadata.</div>
  }

  const meta = metaQuery.data

  return (
    <div className="space-y-8 p-8">
      <SectionHeader
        title="Application Settings"
        description="Inspect Orchestra configuration, build metadata, and edit the active config file."
      />

      {/* Restart banner */}
      {restartCountdown !== null && (
        <div className="rounded-xl border border-amber-200 bg-amber-50 px-6 py-4 text-sm text-amber-800 dark:border-amber-800/40 dark:bg-amber-950/30 dark:text-amber-300">
          Server is restarting… reloading in {restartCountdown}s
        </div>
      )}

      <div className="grid grid-cols-1 gap-6 xl:grid-cols-2">
        <section className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
          <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">Application</h2>
          <dl className="mt-4 space-y-3 text-sm">
            <div className="flex items-center justify-between gap-4 border-b border-gray-100 pb-3 dark:border-slate-800">
              <dt className="text-gray-500 dark:text-slate-400">Name</dt>
              <dd className="font-medium text-gray-900 dark:text-slate-100">{meta.name}</dd>
            </div>
            <div className="flex items-center justify-between gap-4 border-b border-gray-100 pb-3 dark:border-slate-800">
              <dt className="text-gray-500 dark:text-slate-400">Environment</dt>
              <dd className="font-medium text-gray-900 dark:text-slate-100">{meta.environment}</dd>
            </div>
            <div className="flex items-center justify-between gap-4 border-b border-gray-100 pb-3 dark:border-slate-800">
              <dt className="text-gray-500 dark:text-slate-400">Public URL</dt>
              <dd className="font-medium text-gray-900 dark:text-slate-100">{meta.url}</dd>
            </div>
            <div className="flex items-center justify-between gap-4">
              <dt className="text-gray-500 dark:text-slate-400">Vite Proxy URL</dt>
              <dd className="font-medium text-gray-900 dark:text-slate-100">{meta.uiProxy}</dd>
            </div>
          </dl>
        </section>

        <section className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
          <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">Build Metadata</h2>
          <dl className="mt-4 space-y-3 text-sm">
            <div className="flex items-center justify-between gap-4 border-b border-gray-100 pb-3 dark:border-slate-800">
              <dt className="text-gray-500 dark:text-slate-400">Version</dt>
              <dd className="font-medium text-gray-900 dark:text-slate-100">{meta.version.version}</dd>
            </div>
            <div className="flex items-center justify-between gap-4 border-b border-gray-100 pb-3 dark:border-slate-800">
              <dt className="text-gray-500 dark:text-slate-400">Commit</dt>
              <dd className="font-medium text-gray-900 dark:text-slate-100">{meta.version.commit}</dd>
            </div>
            <div className="flex items-center justify-between gap-4">
              <dt className="text-gray-500 dark:text-slate-400">Build date</dt>
              <dd className="font-medium text-gray-900 dark:text-slate-100">{meta.version.buildDate}</dd>
            </div>
          </dl>
        </section>
      </div>

      {/* Config file editor */}
      {meta.configEditable ? (
        <section className="rounded-xl border border-gray-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
          {/* Header */}
          <div className="flex items-center justify-between gap-4 border-b border-gray-200 px-6 py-4 dark:border-slate-800">
            <div className="min-w-0">
              <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">Config File</h2>
              {configQuery.data ? (
                <p className="mt-0.5 truncate font-mono text-xs text-gray-500 dark:text-slate-400">
                  {configQuery.data.path}
                </p>
              ) : configQuery.isLoading ? (
                <p className="mt-0.5 text-xs text-gray-400 dark:text-slate-500">Loading…</p>
              ) : (
                <p className="mt-0.5 text-xs text-amber-600 dark:text-amber-400">
                  No config file found — server started without a config file.
                </p>
              )}
            </div>
            <div className="flex shrink-0 items-center gap-3">
              {saveNotice && (
                <span className="flex items-center gap-1.5 text-xs font-medium text-emerald-600 dark:text-emerald-400">
                  <Check className="h-3.5 w-3.5" />
                  {saveNotice}
                </span>
              )}
              {saveError && (
                <span className="text-xs font-medium text-red-600 dark:text-red-400">{saveError}</span>
              )}
              <button
                type="button"
                onClick={() => void queryClient.invalidateQueries({ queryKey: ['config-raw'] })}
                disabled={configQuery.isFetching}
                title="Reload from disk"
                className="rounded-lg border border-gray-200 p-2 text-gray-500 transition-colors hover:bg-gray-50 disabled:opacity-50 dark:border-slate-700 dark:text-slate-400 dark:hover:bg-slate-800"
              >
                <RefreshCw className={`h-3.5 w-3.5 ${configQuery.isFetching ? 'animate-spin' : ''}`} />
              </button>
              <button
                type="button"
                onClick={() => saveMutation.mutate()}
                disabled={saveMutation.isPending || !isDirty || !configQuery.data}
                className="inline-flex items-center gap-1.5 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700 disabled:cursor-not-allowed disabled:opacity-60"
              >
                <Save className="h-3.5 w-3.5" />
                {saveMutation.isPending ? 'Saving…' : 'Save'}
              </button>
              <button
                type="button"
                onClick={() => restartMutation.mutate()}
                disabled={restartMutation.isPending || restartCountdown !== null}
                title="Gracefully restart the server process"
                className="inline-flex items-center gap-1.5 rounded-lg border border-amber-300 bg-amber-50 px-4 py-2 text-sm font-semibold text-amber-700 transition-colors hover:bg-amber-100 disabled:cursor-not-allowed disabled:opacity-60 dark:border-amber-700 dark:bg-amber-950/30 dark:text-amber-300 dark:hover:bg-amber-900/40"
              >
                <RotateCcw className="h-3.5 w-3.5" />
                {restartCountdown !== null ? `Restarting… ${restartCountdown}s` : restartMutation.isPending ? 'Restarting…' : 'Restart server'}
              </button>
            </div>
          </div>

          {/* Restart notice */}
          <div className="border-b border-amber-100 bg-amber-50 px-6 py-2.5 text-xs text-amber-700 dark:border-amber-900/30 dark:bg-amber-950/20 dark:text-amber-300">
            Changes are written to disk immediately but only take effect after a manual server restart.
          </div>

          {/* Editor */}
          <div className="h-[480px]">
            {configQuery.data ? (
              <Editor
                height="100%"
                language="ini"
                value={configContent}
                onChange={(val) => setConfigContent(val ?? '')}
                theme={monacoTheme}
                options={{
                  minimap: { enabled: false },
                  fontSize: 13,
                  lineHeight: 20,
                  padding: { top: 12, bottom: 16 },
                  scrollBeyondLastLine: false,
                  wordWrap: 'off',
                  renderLineHighlight: 'line',
                  smoothScrolling: true,
                  tabSize: 2,
                }}
              />
            ) : configQuery.isLoading ? (
              <div className="flex h-full items-center justify-center text-sm text-gray-400 dark:text-slate-500">
                Loading config…
              </div>
            ) : (
              <div className="flex h-full items-center justify-center text-sm text-gray-400 dark:text-slate-500">
                No config file available.
              </div>
            )}
          </div>
        </section>
      ) : (
        <section className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
          <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">Config File</h2>
          <p className="mt-2 text-sm text-gray-500 dark:text-slate-400">
            Config file editing is only available in single-node mode. In a cluster deployment, manage configuration through environment variables or your deployment tooling.
          </p>
        </section>
      )}
    </div>
  )
}
