import { useState, type FormEvent } from 'react'
import { Sparkles, X } from 'lucide-react'

type EnhanceAgentPromptModalProps = Readonly<{
  isPending: boolean
  error?: string | null
  onClose: () => void
  onEnhance: (message: string) => void
}>

export default function EnhanceAgentPromptModal({
  isPending,
  error,
  onClose,
  onEnhance,
}: EnhanceAgentPromptModalProps) {
  const [message, setMessage] = useState('')
  const trimmedMessage = message.trim()

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (trimmedMessage) onEnhance(trimmedMessage)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/55 p-4 backdrop-blur-sm">
      <dialog
        open
        aria-modal="true"
        aria-labelledby="enhance-agent-prompt-title"
        className="m-auto w-full max-w-lg rounded-lg border border-gray-200 bg-white p-0 shadow-xl dark:border-slate-700 dark:bg-slate-900"
      >
        <form onSubmit={handleSubmit}>
          <div className="flex items-start justify-between gap-4 border-b border-gray-200 px-5 py-4 dark:border-slate-800">
            <div>
              <h2 id="enhance-agent-prompt-title" className="text-base font-semibold text-gray-900 dark:text-slate-100">
                Enhance agent prompt
              </h2>
              <p className="mt-1 text-sm text-gray-500 dark:text-slate-400">
                Describe what the enhanced prompt should add, change, or emphasize.
              </p>
            </div>
            <button
              type="button"
              onClick={onClose}
              disabled={isPending}
              title="Close"
              className="rounded-md p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 disabled:opacity-50 dark:hover:bg-slate-800 dark:hover:text-slate-200"
            >
              <X className="h-4 w-4" />
            </button>
          </div>

          <div className="space-y-3 px-5 py-4">
            <div>
              <label htmlFor="agent-enhancement-message" className="mb-1.5 block text-sm font-medium text-gray-700 dark:text-slate-300">
                Enhancement instructions
              </label>
              <textarea
                id="agent-enhancement-message"
                value={message}
                onChange={(event) => setMessage(event.target.value)}
                rows={6}
                required
                autoFocus
                disabled={isPending}
                placeholder="For example: Add clear escalation rules and require concise, structured responses."
                className="w-full resize-y rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm text-gray-900 outline-none focus:border-primary-500 focus:ring-1 focus:ring-primary-500 disabled:opacity-60 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
              />
              <p className="mt-1 text-xs text-gray-500 dark:text-slate-400">
                Required. These instructions are combined with the current system prompt for enhancement.
              </p>
            </div>
            {error ? <p className="text-sm text-red-600 dark:text-red-300">{error}</p> : null}
          </div>

          <div className="flex justify-end gap-2 border-t border-gray-200 px-5 py-4 dark:border-slate-800">
            <button
              type="button"
              onClick={onClose}
              disabled={isPending}
              className="rounded-lg border border-gray-200 px-3 py-2 text-sm font-semibold text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={isPending || !trimmedMessage}
              className="inline-flex items-center gap-2 rounded-lg bg-primary-600 px-3 py-2 text-sm font-semibold text-white hover:bg-primary-700 disabled:cursor-not-allowed disabled:opacity-50"
            >
              <Sparkles className="h-4 w-4" />
              {isPending ? 'Enhancing...' : 'Enhance prompt'}
            </button>
          </div>
        </form>
      </dialog>
    </div>
  )
}
