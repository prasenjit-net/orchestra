import { useState } from 'react'
import { Play, X } from 'lucide-react'

interface StartWorkflowModalProps {
  definitionName: string
  onClose: () => void
  onStart: (input: Record<string, unknown>, callbackUrl: string) => void
  isPending: boolean
  error?: string | null
}

export default function StartWorkflowModal({
  definitionName,
  onClose,
  onStart,
  isPending,
  error,
}: StartWorkflowModalProps) {
  const [inputText, setInputText] = useState('{}')
  const [callbackUrl, setCallbackUrl] = useState('')
  const [parseError, setParseError] = useState<string | null>(null)

  function handleStart() {
    let parsed: Record<string, unknown>
    try {
      parsed = JSON.parse(inputText) as Record<string, unknown>
      if (typeof parsed !== 'object' || Array.isArray(parsed) || parsed === null) {
        setParseError('Input must be a JSON object')
        return
      }
    } catch {
      setParseError('Invalid JSON')
      return
    }
    setParseError(null)
    onStart(parsed, callbackUrl.trim())
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
      <div className="w-full max-w-lg rounded-2xl bg-white shadow-xl dark:bg-slate-900">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-slate-800">
          <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">
            Start <span className="text-primary-600 dark:text-primary-400">{definitionName}</span>
          </h2>
          <button
            type="button"
            onClick={onClose}
            className="rounded-md p-1 text-gray-400 hover:text-gray-600 dark:text-slate-500 dark:hover:text-slate-300"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        {/* Body */}
        <div className="space-y-4 px-6 py-5">
          <div>
            <label className="mb-1.5 block text-sm font-medium text-gray-700 dark:text-slate-300">
              Input JSON
            </label>
            <textarea
              value={inputText}
              onChange={(e) => {
                setInputText(e.target.value)
                setParseError(null)
              }}
              rows={8}
              spellCheck={false}
              className="w-full rounded-lg border border-gray-300 bg-white px-3 py-2 font-mono text-sm text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-1 focus:ring-primary-500 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100"
              placeholder="{}"
            />
            {parseError && (
              <p className="mt-1 text-xs text-red-600 dark:text-red-400">{parseError}</p>
            )}
          </div>

          <div>
            <label className="mb-1.5 block text-sm font-medium text-gray-700 dark:text-slate-300">
              Callback URL <span className="font-normal text-gray-400 dark:text-slate-500">(optional)</span>
            </label>
            <input
              type="url"
              value={callbackUrl}
              onChange={(e) => setCallbackUrl(e.target.value)}
              placeholder="https://example.com/webhook/result"
              className="w-full rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-1 focus:ring-primary-500 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100"
            />
            <p className="mt-1 text-xs text-gray-500 dark:text-slate-400">
              Called with the workflow output when it completes. Must match the server allowlist.
            </p>
          </div>

          {error && (
            <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/40 dark:bg-red-950/30 dark:text-red-300">
              {error}
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="flex justify-end gap-3 border-t border-gray-200 px-6 py-4 dark:border-slate-800">
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg border border-gray-200 px-4 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={handleStart}
            disabled={isPending}
            className="inline-flex items-center gap-2 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700 disabled:cursor-not-allowed disabled:opacity-60"
          >
            <Play className="h-3.5 w-3.5" />
            {isPending ? 'Starting…' : 'Start workflow'}
          </button>
        </div>
      </div>
    </div>
  )
}
