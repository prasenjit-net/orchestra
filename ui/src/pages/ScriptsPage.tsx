import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Code2, Download, Plus, Upload } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import ImportModal from '../components/ImportModal'
import { useImport } from '../hooks/useImport'
import { downloadBundle, importExportApi, scriptsApi } from '../services/api'
import { formatDate } from './workflowUi'

export default function ScriptsPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const scriptsQuery = useQuery({
    queryKey: ['scripts'],
    queryFn: scriptsApi.list,
  })

  const importHook = useImport(() => {
    void queryClient.invalidateQueries({ queryKey: ['scripts'] })
  })

  if (scriptsQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading scripts…</div>
  }

  if (scriptsQuery.error) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Unable to load scripts.</div>
  }

  const scripts = scriptsQuery.data?.scripts ?? []

  return (
    <div className="space-y-8 p-8">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-gray-900 dark:text-slate-100">Scripts</h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-slate-400">Reusable Starlark scripts you can attach to workflow steps.</p>
        </div>
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
            onClick={() => navigate('/scripts/new')}
            className="inline-flex items-center gap-2 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700"
          >
            <Plus className="h-4 w-4" />
            New Script
          </button>
        </div>
      </div>

      {importHook.state.error && (
        <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/40 dark:bg-red-950/30 dark:text-red-300">
          Import error: {importHook.state.error}
        </div>
      )}

      {scripts.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-gray-300 py-20 dark:border-slate-700">
          <Code2 className="mb-4 h-10 w-10 text-gray-300 dark:text-slate-600" />
          <p className="text-sm font-medium text-gray-500 dark:text-slate-400">No scripts yet</p>
          <p className="mt-1 text-xs text-gray-400 dark:text-slate-500">Create a script to reuse Starlark code across workflow steps.</p>
          <button
            type="button"
            onClick={() => navigate('/scripts/new')}
            className="mt-6 inline-flex items-center gap-2 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700"
          >
            <Plus className="h-4 w-4" />
            New Script
          </button>
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {scripts.map((script) => (
            <div key={script.id} className="group relative flex flex-col rounded-2xl border border-gray-200 bg-white p-5 text-left shadow-sm transition-shadow hover:shadow-md dark:border-slate-800 dark:bg-slate-900">
              <button
                type="button"
                onClick={async (e) => { e.stopPropagation(); const b = await importExportApi.exportScript(script.id); downloadBundle(b, script.name) }}
                title="Export script"
                className="absolute right-3 top-3 rounded-lg border border-gray-200 p-1.5 text-gray-300 opacity-0 transition-all hover:bg-gray-50 hover:text-gray-500 group-hover:opacity-100 dark:border-slate-700 dark:text-slate-600 dark:hover:bg-slate-800 dark:hover:text-slate-400"
              >
                <Download className="h-3.5 w-3.5" />
              </button>
              <button
                type="button"
                onClick={() => navigate(`/scripts/${script.id}/editor`)}
                className="flex flex-col text-left"
              >
                <div className="flex items-start justify-between gap-3 pr-8">
                  <p className="font-semibold text-gray-900 group-hover:text-primary-600 dark:text-slate-100 dark:group-hover:text-primary-400">
                    {script.name}
                  </p>
                  <span className="shrink-0 rounded-full bg-violet-100 px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-violet-700 dark:bg-violet-900/30 dark:text-violet-300">
                    {script.language}
                  </span>
                </div>
                {script.description ? (
                  <p className="mt-2 line-clamp-2 text-sm text-gray-500 dark:text-slate-400">{script.description}</p>
                ) : null}
                <p className="mt-auto pt-4 text-xs text-gray-400 dark:text-slate-500">Updated {formatDate(script.updatedAt)}</p>
              </button>
            </div>
          ))}
        </div>
      )}

      {importHook.state.analysis && (
        <ImportModal
          analysis={importHook.state.analysis}
          isPending={importHook.state.isApplying}
          onConfirm={importHook.confirm}
          onClose={importHook.close}
        />
      )}
    </div>
  )
}
