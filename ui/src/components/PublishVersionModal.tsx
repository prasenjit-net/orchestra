import { useEffect, useState } from 'react'
import { Send, X } from 'lucide-react'

type PublishVersionModalProps = {
  version: number
  activeVersion: number
  isPending: boolean
  error?: string | null
  onClose: () => void
  onPublish: (activate: boolean) => void
}

export default function PublishVersionModal({ version, activeVersion, isPending, error, onClose, onPublish }: PublishVersionModalProps) {
  const [activate, setActivate] = useState(false)

  useEffect(() => {
    setActivate(false)
  }, [version])

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/55 p-4" role="dialog" aria-modal="true" aria-labelledby="publish-version-title">
      <div className="w-full max-w-md rounded-lg border border-gray-200 bg-white shadow-xl dark:border-slate-700 dark:bg-slate-900">
        <div className="flex items-start justify-between gap-4 border-b border-gray-200 px-5 py-4 dark:border-slate-800">
          <div>
            <h2 id="publish-version-title" className="text-base font-semibold text-gray-900 dark:text-slate-100">Publish version {version}</h2>
            <p className="mt-1 text-sm text-gray-500 dark:text-slate-400">Version {activeVersion} is currently active for new runs.</p>
          </div>
          <button type="button" onClick={onClose} disabled={isPending} title="Close" className="rounded-md p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 disabled:opacity-50 dark:hover:bg-slate-800 dark:hover:text-slate-200">
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="space-y-4 px-5 py-4">
          <label className="flex cursor-pointer items-start gap-3 rounded-lg border border-gray-200 p-3 dark:border-slate-700">
            <input type="checkbox" checked={activate} onChange={(event) => setActivate(event.target.checked)} className="mt-0.5 h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" />
            <span>
              <span className="block text-sm font-semibold text-gray-900 dark:text-slate-100">Activate after publishing</span>
              <span className="mt-0.5 block text-xs text-gray-500 dark:text-slate-400">New workflow runs will start on version {version}. Existing runs are unchanged.</span>
            </span>
          </label>
          {error ? <p className="text-sm text-red-600 dark:text-red-300">{error}</p> : null}
        </div>

        <div className="flex justify-end gap-2 border-t border-gray-200 px-5 py-4 dark:border-slate-800">
          <button type="button" onClick={onClose} disabled={isPending} className="rounded-lg border border-gray-200 px-3 py-2 text-sm font-semibold text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800">Cancel</button>
          <button type="button" onClick={() => onPublish(activate)} disabled={isPending} className="inline-flex items-center gap-2 rounded-lg bg-primary-600 px-3 py-2 text-sm font-semibold text-white hover:bg-primary-700 disabled:opacity-50">
            <Send className="h-4 w-4" />
            {isPending ? 'Publishing…' : activate ? 'Publish and activate' : 'Publish'}
          </button>
        </div>
      </div>
    </div>
  )
}
