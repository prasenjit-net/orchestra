import { useState, useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, Save, Trash2 } from 'lucide-react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import Editor from '@monaco-editor/react'
import { useMonacoTheme } from '../hooks/useMonacoTheme'
import { scriptsApi } from '../services/api'
import type { CreateScriptInput } from '../types'

const BUILTINS_HINT = `json        — json.encode / json.decode
strings     — lower, upper, trim, contains, replace
collections — compact, flatten
workflow    — id, step_name, step_output(name), signal(name), fail(msg)
asserts     — non_empty(v), equals(l, r)
ctx         — full workflow context object
input       — data passed to this step`

export default function ScriptEditorPage() {
  const { scriptId } = useParams<{ scriptId: string }>()
  const isNew = !scriptId || scriptId === 'new'
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [language, setLanguage] = useState('starlark')
  const [source, setSource] = useState('')
  const [timeoutMs, setTimeoutMs] = useState('')
  const [exportsRaw, setExportsRaw] = useState('result')
  const [pageError, setPageError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)

  const scriptQuery = useQuery({
    queryKey: ['script', scriptId],
    queryFn: () => scriptsApi.get(scriptId!),
    enabled: !isNew,
  })

  useEffect(() => {
    if (scriptQuery.data) {
      const s = scriptQuery.data
      setName(s.name)
      setDescription(s.description)
      setLanguage(s.language)
      setSource(s.source)
      setTimeoutMs(s.timeoutMs ? String(s.timeoutMs) : '')
      setExportsRaw((s.exports ?? ['result']).join(', '))
    }
  }, [scriptQuery.data])

  const buildInput = (): CreateScriptInput => ({
    name: name.trim(),
    description: description.trim(),
    language: language.trim() || 'starlark',
    source,
    timeoutMs: timeoutMs ? parseInt(timeoutMs, 10) : 0,
    exports: exportsRaw
      .split(',')
      .map((e) => e.trim())
      .filter(Boolean),
  })

  const createMutation = useMutation({
    mutationFn: scriptsApi.create,
    onSuccess: (script) => {
      setPageError(null)
      setSaved(true)
      void queryClient.invalidateQueries({ queryKey: ['scripts'] })
      navigate(`/scripts/${script.id}/editor`, { replace: true })
    },
    onError: (error: Error) => {
      setPageError(error.message)
    },
  })

  const updateMutation = useMutation({
    mutationFn: (input: CreateScriptInput) => scriptsApi.update(scriptId!, input),
    onSuccess: (script) => {
      setPageError(null)
      setSaved(true)
      void queryClient.invalidateQueries({ queryKey: ['scripts'] })
      void queryClient.setQueryData(['script', scriptId], script)
      setTimeout(() => setSaved(false), 2000)
    },
    onError: (error: Error) => {
      setPageError(error.message)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: () => scriptsApi.delete(scriptId!),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['scripts'] })
      navigate('/scripts', { replace: true })
    },
    onError: (error: Error) => {
      setPageError(error.message)
    },
  })

  const handleSave = () => {
    const input = buildInput()
    if (!input.name) {
      setPageError('Name is required.')
      return
    }
    if (isNew) {
      createMutation.mutate(input)
    } else {
      updateMutation.mutate(input)
    }
  }

  const monacoTheme = useMonacoTheme()

  const handleDelete = () => {
    if (!window.confirm('Delete this script? Workflow steps that reference it will fail at runtime.')) {
      return
    }
    deleteMutation.mutate()
  }

  if (!isNew && scriptQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading script…</div>
  }

  if (!isNew && scriptQuery.error) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Could not load script.</div>
  }

  const isSaving = createMutation.isPending || updateMutation.isPending

  return (
    <div className="flex h-full flex-col overflow-hidden">
      {/* Header bar */}
      <div className="shrink-0 border-b border-gray-200 bg-white px-5 py-3 dark:border-slate-800 dark:bg-slate-900">
        <div className="flex items-center justify-between gap-4">
          <div className="flex items-center gap-3">
            <Link
              to="/scripts"
              className="inline-flex items-center gap-1.5 text-sm font-medium text-gray-500 transition-colors hover:text-gray-900 dark:text-slate-400 dark:hover:text-slate-100"
            >
              <ArrowLeft className="h-4 w-4" />
              Scripts
            </Link>
            <span className="text-gray-300 dark:text-slate-700">/</span>
            <span className="text-sm font-semibold text-gray-900 dark:text-slate-100">{name || (isNew ? 'New Script' : '…')}</span>
          </div>
          <div className="flex items-center gap-2">
            {saved && <span className="text-xs font-medium text-emerald-600 dark:text-emerald-400">Saved</span>}
            {pageError && <span className="text-xs font-medium text-red-600 dark:text-red-400">{pageError}</span>}
            {!isNew && (
              <button
                type="button"
                onClick={handleDelete}
                disabled={deleteMutation.isPending}
                className="inline-flex items-center gap-1.5 rounded-lg border border-red-200 px-3 py-2 text-sm font-semibold text-red-700 transition-colors hover:bg-red-50 disabled:opacity-50 dark:border-red-900/40 dark:text-red-300 dark:hover:bg-red-950/20"
              >
                <Trash2 className="h-4 w-4" />
                Delete
              </button>
            )}
            <button
              type="button"
              onClick={handleSave}
              disabled={isSaving}
              className="inline-flex items-center gap-1.5 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700 disabled:opacity-60"
            >
              <Save className="h-4 w-4" />
              {isSaving ? 'Saving…' : 'Save'}
            </button>
          </div>
        </div>
      </div>

      {/* Metadata bar */}
      <div className="shrink-0 border-b border-gray-200 bg-gray-50 px-5 py-2.5 dark:border-slate-800 dark:bg-slate-950">
        <div className="flex flex-wrap items-center gap-4">
          <div className="flex items-center gap-2">
            <label className="shrink-0 text-[11px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Name</label>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-transform"
              className="rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
            />
          </div>
          <div className="flex flex-1 items-center gap-2">
            <label className="shrink-0 text-[11px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Description</label>
            <input
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="What does this script do?"
              className="w-full rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
            />
          </div>
          <span className="rounded-full bg-violet-100 px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-violet-700 dark:bg-violet-900/30 dark:text-violet-300">
            {language}
          </span>
        </div>
      </div>

      {/* Canvas */}
      <div className="flex flex-1 overflow-hidden">
        {/* Left panel */}
        <div className="flex w-72 shrink-0 flex-col gap-4 overflow-y-auto border-r border-gray-200 bg-white p-5 dark:border-slate-800 dark:bg-slate-900">
          <div>
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Language</label>
            <input
              value={language}
              onChange={(e) => setLanguage(e.target.value || 'starlark')}
              placeholder="starlark"
              className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
            />
          </div>
          <div>
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Timeout ms</label>
            <input
              type="number"
              min={1}
              value={timeoutMs}
              onChange={(e) => setTimeoutMs(e.target.value)}
              placeholder="100"
              className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
            />
          </div>
          <div>
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Exports</label>
            <input
              value={exportsRaw}
              onChange={(e) => setExportsRaw(e.target.value)}
              placeholder="result, extra_value"
              className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
            />
            <p className="mt-1 text-[11px] text-gray-400 dark:text-slate-500">Comma-separated variable names to export.</p>
          </div>
          <div>
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Available builtins</label>
            <pre className="rounded-lg bg-slate-950 p-3 text-[11px] leading-relaxed text-slate-300">{BUILTINS_HINT}</pre>
          </div>
          <div className="rounded-lg border border-dashed border-gray-300 p-3 text-[11px] text-gray-500 dark:border-slate-700 dark:text-slate-400">
            Reference this script in a workflow step by setting <span className="font-mono">scriptId</span> to{' '}
            <span className="font-mono font-semibold">{isNew ? '<id after save>' : scriptId}</span>.
          </div>
        </div>

        {/* Right panel — Monaco source editor */}
        <div className="flex flex-1 flex-col overflow-hidden bg-white dark:bg-slate-950">
          <div className="shrink-0 px-4 pt-4 pb-2">
            <label className="text-[11px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-400">Source</label>
          </div>
          <div className="flex-1 overflow-hidden">
            <Editor
              height="100%"
              language="python"
              value={source}
              onChange={(val) => setSource(val ?? '')}
              theme={monacoTheme}
              options={{
                minimap: { enabled: false },
                fontSize: 13,
                lineHeight: 20,
                padding: { top: 8, bottom: 16 },
                scrollBeyondLastLine: false,
                wordWrap: 'on',
                renderLineHighlight: 'line',
                smoothScrolling: true,
                cursorBlinking: 'smooth',
              }}
            />
          </div>
        </div>
      </div>
    </div>
  )
}
