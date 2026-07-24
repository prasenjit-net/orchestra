import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Braces, Download, Plus, Upload } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import ImportModal from '../components/ImportModal'
import { useImport } from '../hooks/useImport'
import { downloadBundle, importExportApi, jsonSchemasApi } from '../services/api'
import { formatDate } from './workflowUi'

function schemaType(schema: Record<string, unknown>) {
  const type = schema.type
  return typeof type === 'string' ? type : 'schema'
}

function propertyCount(schema: Record<string, unknown>) {
  const properties = schema.properties
  if (!properties || typeof properties !== 'object' || Array.isArray(properties)) {
    return 0
  }
  return Object.keys(properties).length
}

export default function JsonSchemasPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [exportError, setExportError] = useState<string | null>(null)
  const [isExporting, setIsExporting] = useState(false)

  const schemasQuery = useQuery({
    queryKey: ['json-schemas'],
    queryFn: jsonSchemasApi.list,
  })

  const importHook = useImport(() => {
    void queryClient.invalidateQueries({ queryKey: ['json-schemas'] })
  })

  if (schemasQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading JSON schemas…</div>
  }

  if (schemasQuery.error) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Unable to load JSON schemas.</div>
  }

  const schemas = schemasQuery.data?.schemas ?? []

  const handleExportBundle = async () => {
    setExportError(null)
    setIsExporting(true)
    try {
      const bundle = await importExportApi.exportJSONSchemas()
      downloadBundle(bundle, 'json-schemas')
    } catch (error) {
      setExportError((error as Error).message)
    } finally {
      setIsExporting(false)
    }
  }

  return (
    <div className="space-y-8 p-8">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-gray-900 dark:text-slate-100">JSON Schemas</h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-slate-400">Structured schemas for future workflow inputs, outputs, and reusable contracts.</p>
        </div>
        <div className="flex items-center gap-2">
          <input ref={importHook.fileInputRef} type="file" accept=".json" className="hidden" onChange={importHook.onFileChange} />
          <button
            type="button"
            onClick={() => void handleExportBundle()}
            disabled={isExporting}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 px-4 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 disabled:opacity-60 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
          >
            <Download className="h-4 w-4" />
            {isExporting ? 'Exporting…' : 'Bundle'}
          </button>
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
            onClick={() => navigate('/json-schemas/new')}
            className="inline-flex items-center gap-2 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700"
          >
            <Plus className="h-4 w-4" />
            New Schema
          </button>
        </div>
      </div>

      {importHook.state.error && (
        <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/40 dark:bg-red-950/30 dark:text-red-300">
          Import error: {importHook.state.error}
        </div>
      )}

      {exportError && (
        <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/40 dark:bg-red-950/30 dark:text-red-300">
          Export error: {exportError}
        </div>
      )}

      {schemas.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-gray-300 py-20 dark:border-slate-700">
          <Braces className="mb-4 h-10 w-10 text-gray-300 dark:text-slate-600" />
          <p className="text-sm font-medium text-gray-500 dark:text-slate-400">No JSON schemas yet</p>
          <p className="mt-1 text-xs text-gray-400 dark:text-slate-500">Create a schema contract to reuse across workflow design later.</p>
          <button
            type="button"
            onClick={() => navigate('/json-schemas/new')}
            className="mt-6 inline-flex items-center gap-2 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700"
          >
            <Plus className="h-4 w-4" />
            New Schema
          </button>
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {schemas.map((schema) => (
            <div key={schema.id} className="group relative flex flex-col rounded-2xl border border-gray-200 bg-white p-5 text-left shadow-sm transition-shadow hover:shadow-md dark:border-slate-800 dark:bg-slate-900">
              <button
                type="button"
                onClick={async (e) => { e.stopPropagation(); const bundle = await importExportApi.exportJSONSchema(schema.id); downloadBundle(bundle, schema.name) }}
                title="Export JSON schema"
                className="absolute right-3 top-3 rounded-lg border border-gray-200 p-1.5 text-gray-300 opacity-0 transition-all hover:bg-gray-50 hover:text-gray-500 group-hover:opacity-100 dark:border-slate-700 dark:text-slate-600 dark:hover:bg-slate-800 dark:hover:text-slate-400"
              >
                <Download className="h-3.5 w-3.5" />
              </button>
              <button
                type="button"
                onClick={() => navigate(`/json-schemas/${schema.id}/editor`)}
                className="flex h-full flex-col text-left"
              >
                <div className="flex items-start justify-between gap-3 pr-8">
                  <p className="font-semibold text-gray-900 group-hover:text-primary-600 dark:text-slate-100 dark:group-hover:text-primary-400">
                    {schema.name}
                  </p>
                  <span className="shrink-0 rounded-full bg-cyan-100 px-2.5 py-0.5 text-[10px] font-semibold uppercase text-cyan-700 dark:bg-cyan-900/30 dark:text-cyan-300">
                    {schemaType(schema.schema)}
                  </span>
                </div>
                {schema.description ? (
                  <p className="mt-2 line-clamp-2 text-sm text-gray-500 dark:text-slate-400">{schema.description}</p>
                ) : null}
                <div className="mt-auto flex items-center justify-between gap-3 pt-4 text-xs text-gray-400 dark:text-slate-500">
                  <span>{propertyCount(schema.schema)} top-level properties</span>
                  <span>Updated {formatDate(schema.updatedAt)}</span>
                </div>
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
