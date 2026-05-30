import { useState, useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, BookOpen, CheckCircle, Save, Sparkles, Trash2, X, XCircle } from 'lucide-react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import Editor from '@monaco-editor/react'
import ScriptAssistModal from '../components/ScriptAssistModal'
import { useMonacoTheme } from '../hooks/useMonacoTheme'
import { scriptAiApi, scriptsApi } from '../services/api'
import type { CreateScriptInput } from '../types'

// ─── Builtins reference ───────────────────────────────────────────────────────

const BUILTINS: { name: string; members: { sig: string; desc: string }[] }[] = [
  {
    name: 'ctx — workflow context',
    members: [
      { sig: 'ctx["input"]', desc: 'Dict of workflow start input fields' },
      { sig: 'ctx["steps"]["name"]', desc: 'Output dict of a completed step' },
      { sig: 'ctx["signals"]["name"]', desc: 'Last received signal payload dict' },
      { sig: 'ctx["workflow"]', desc: 'Workflow metadata (id, definition_id, …)' },
    ],
  },
  {
    name: 'input',
    members: [
      { sig: 'input', desc: 'The step\'s static data field configured in the workflow definition' },
    ],
  },
  {
    name: 'workflow',
    members: [
      { sig: 'workflow.id', desc: 'Current run ID' },
      { sig: 'workflow.definition_id', desc: 'Definition ID' },
      { sig: 'workflow.definition_version', desc: 'Version number (int)' },
      { sig: 'workflow.step_name', desc: 'Current step name' },
      { sig: 'workflow.step_output("name")', desc: 'Output dict of a past step' },
      { sig: 'workflow.signal("name")', desc: 'Last signal payload dict' },
      { sig: 'workflow.fail("message")', desc: 'Fail the step with an error' },
    ],
  },
  {
    name: 'strings',
    members: [
      { sig: 'strings.lower(v)', desc: 'Lowercase string' },
      { sig: 'strings.upper(v)', desc: 'Uppercase string' },
      { sig: 'strings.trim(v)', desc: 'Strip surrounding whitespace' },
      { sig: 'strings.contains(v, part)', desc: 'True if v contains part' },
      { sig: 'strings.replace(v, old, new)', desc: 'Replace all occurrences' },
    ],
  },
  {
    name: 'collections',
    members: [
      { sig: 'collections.compact(list_or_dict)', desc: 'Remove falsy/empty values' },
      { sig: 'collections.flatten(list)', desc: 'Flatten one nesting level' },
    ],
  },
  {
    name: 'json',
    members: [
      { sig: 'json.encode(value)', desc: 'Serialise to JSON string' },
      { sig: 'json.decode(str)', desc: 'Parse JSON string to value' },
    ],
  },
  {
    name: 'asserts',
    members: [
      { sig: 'asserts.non_empty(value, msg?)', desc: 'Fail the step if value is empty or falsy' },
      { sig: 'asserts.equals(left, right, msg?)', desc: 'Fail if left != right' },
    ],
  },
]

function BuiltinsDrawer({ onClose }: { onClose: () => void }) {
  return (
    <div className="absolute inset-y-0 right-0 z-20 flex w-80 flex-col border-l border-gray-200 bg-white shadow-xl dark:border-slate-700 dark:bg-slate-900">
      <div className="flex shrink-0 items-center justify-between border-b border-gray-200 px-4 py-3 dark:border-slate-700">
        <span className="text-sm font-semibold text-gray-900 dark:text-slate-100">Available builtins</span>
        <button
          type="button"
          onClick={onClose}
          className="rounded-lg p-1 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 dark:text-slate-500 dark:hover:bg-slate-800 dark:hover:text-slate-300"
        >
          <X className="h-4 w-4" />
        </button>
      </div>
      <div className="flex-1 space-y-5 overflow-y-auto px-4 py-4">
        <p className="text-[11px] text-gray-400 dark:text-slate-500">
          Assign output to <span className="font-mono font-semibold">result</span>. Booleans are <span className="font-mono">True</span> / <span className="font-mono">False</span>. No imports or I/O.
        </p>
        {BUILTINS.map((group) => (
          <div key={group.name}>
            <p className="mb-2 text-[10px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">{group.name}</p>
            <div className="space-y-1.5">
              {group.members.map((m) => (
                <div key={m.sig} className="rounded-lg bg-gray-50 px-3 py-2 dark:bg-slate-800">
                  <p className="font-mono text-[11px] font-medium text-primary-700 dark:text-primary-400">{m.sig}</p>
                  <p className="mt-0.5 text-[11px] text-gray-500 dark:text-slate-400">{m.desc}</p>
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

// ─── Page ─────────────────────────────────────────────────────────────────────

export default function ScriptEditorPage() {
  const { scriptId } = useParams<{ scriptId: string }>()
  const isNew = !scriptId || scriptId === 'new'
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [source, setSource] = useState('')
  const [timeoutMs, setTimeoutMs] = useState('')
  const [pageError, setPageError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [showAssist, setShowAssist] = useState(false)
  const [showBuiltins, setShowBuiltins] = useState(false)
  const [validation, setValidation] = useState<{ valid: boolean; error?: string } | null>(null)
  const [isValidating, setIsValidating] = useState(false)

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
      setSource(s.source)
      setTimeoutMs(s.timeoutMs ? String(s.timeoutMs) : '')
    }
  }, [scriptQuery.data])

  const buildInput = (): CreateScriptInput => ({
    name: name.trim(),
    description: description.trim(),
    language: 'starlark',
    source,
    timeoutMs: timeoutMs ? parseInt(timeoutMs, 10) : 0,
    exports: ['result'],
  })

  const createMutation = useMutation({
    mutationFn: scriptsApi.create,
    onSuccess: (script) => {
      setPageError(null)
      setSaved(true)
      void queryClient.invalidateQueries({ queryKey: ['scripts'] })
      navigate(`/scripts/${script.id}/editor`, { replace: true })
    },
    onError: (error: Error) => setPageError(error.message),
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
    onError: (error: Error) => setPageError(error.message),
  })

  const deleteMutation = useMutation({
    mutationFn: () => scriptsApi.delete(scriptId!),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['scripts'] })
      navigate('/scripts', { replace: true })
    },
    onError: (error: Error) => setPageError(error.message),
  })

  const handleSave = () => {
    const input = buildInput()
    if (!input.name) { setPageError('Name is required.'); return }
    if (isNew) { createMutation.mutate(input) } else { updateMutation.mutate(input) }
  }

  const handleValidate = async () => {
    if (!source.trim()) return
    setIsValidating(true)
    setValidation(null)
    try {
      setValidation(await scriptAiApi.validate(source))
    } catch (err) {
      setValidation({ valid: false, error: (err as Error).message })
    } finally {
      setIsValidating(false)
    }
  }

  const handleDelete = () => {
    if (!window.confirm('Delete this script? Workflow steps that reference it will fail at runtime.')) return
    deleteMutation.mutate()
  }

  const monacoTheme = useMonacoTheme()
  const isSaving = createMutation.isPending || updateMutation.isPending

  if (!isNew && scriptQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading script…</div>
  }
  if (!isNew && scriptQuery.error) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Could not load script.</div>
  }

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
            {pageError && <span className="max-w-xs truncate text-xs font-medium text-red-600 dark:text-red-400">{pageError}</span>}
            <button
              type="button"
              onClick={() => setShowBuiltins((v) => !v)}
              className={`inline-flex items-center gap-1.5 rounded-lg border px-3 py-2 text-sm font-semibold transition-colors ${showBuiltins ? 'border-primary-300 bg-primary-50 text-primary-700 dark:border-primary-700 dark:bg-primary-900/20 dark:text-primary-300' : 'border-gray-200 text-gray-700 hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800'}`}
            >
              <BookOpen className="h-4 w-4" />
              Builtins
            </button>
            <button
              type="button"
              onClick={() => void handleValidate()}
              disabled={isValidating || !source.trim()}
              className="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 px-3 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 disabled:opacity-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
            >
              {validation?.valid
                ? <CheckCircle className="h-4 w-4 text-emerald-500" />
                : validation?.error
                  ? <XCircle className="h-4 w-4 text-red-500" />
                  : null}
              {isValidating ? 'Validating…' : 'Validate'}
            </button>
            <button
              type="button"
              onClick={() => setShowAssist(true)}
              className="inline-flex items-center gap-1.5 rounded-lg border border-violet-200 px-3 py-2 text-sm font-semibold text-violet-700 transition-colors hover:bg-violet-50 dark:border-violet-800/50 dark:text-violet-300 dark:hover:bg-violet-950/20"
            >
              <Sparkles className="h-4 w-4" />
              AI Assist
            </button>
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

      {/* Metadata bar — name, description, timeout on one row */}
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
          <div className="flex items-center gap-2">
            <label className="shrink-0 text-[11px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Timeout ms</label>
            <input
              type="number"
              min={1}
              value={timeoutMs}
              onChange={(e) => setTimeoutMs(e.target.value)}
              placeholder="default"
              className="w-28 rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
            />
          </div>
          {!isNew && (
            <span className="font-mono text-[11px] text-gray-400 dark:text-slate-500">
              id: {scriptId}
            </span>
          )}
        </div>
      </div>

      {/* Editor + builtins drawer */}
      <div className="relative flex-1 overflow-hidden">
        {/* Validation result */}
        {validation && (
          <div className={`absolute left-3 top-2 z-10 flex items-center gap-1 rounded-lg px-2.5 py-1 text-xs font-medium shadow-sm ${validation.valid ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-400' : 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-400'}`}>
            {validation.valid
              ? <><CheckCircle className="h-3.5 w-3.5" /> Valid</>
              : <><XCircle className="h-3.5 w-3.5" /> {validation.error ? validation.error.replace('workflow.star:', 'Line ') : 'Invalid'}</>}
          </div>
        )}

        <Editor
          height="100%"
          language="python"
          value={source}
          onChange={(val) => { setSource(val ?? ''); setValidation(null) }}
          theme={monacoTheme}
          options={{
            minimap: { enabled: false },
            fontSize: 13,
            lineHeight: 20,
            padding: { top: 32, bottom: 16 },
            scrollBeyondLastLine: false,
            wordWrap: 'on',
            renderLineHighlight: 'line',
            smoothScrolling: true,
            cursorBlinking: 'smooth',
          }}
        />

        {showBuiltins && <BuiltinsDrawer onClose={() => setShowBuiltins(false)} />}
      </div>

      {showAssist && (
        <ScriptAssistModal
          currentScript={source}
          onApply={(script) => { setSource(script); setValidation(null) }}
          onClose={() => setShowAssist(false)}
        />
      )}
    </div>
  )
}
