import { useEffect, useState } from 'react'
import { Send, X } from 'lucide-react'

type PublishVersionModalProps = Readonly<{
  version: number
  activeVersion: number
  isPending: boolean
  error?: string | null
  onClose: () => void
  onPublish: (activate: boolean) => void
}>

export default function PublishVersionModal({ version, activeVersion, isPending, error, onClose, onPublish }: PublishVersionModalProps) {
  const [activate, setActivate] = useState(false)
  let publishLabel = 'Publish'
  if (isPending) publishLabel = 'Publishing...'
  else if (activate) publishLabel = 'Publish and activate'

  useEffect(() => {
    setActivate(false)
  }, [version])

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/55 p-4">
      <dialog open aria-modal="true" aria-labelledby="publish-version-title" className="m-0 w-full max-w-md rounded-lg border border-gray-200 bg-white p-0 shadow-xl dark:border-slate-700 dark:bg-slate-900">
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
          <div className="flex items-start gap-3 rounded-lg border border-gray-200 p-3 dark:border-slate-700">
            <input id="activate-published-version" type="checkbox" checked={activate} onChange={(event) => setActivate(event.target.checked)} aria-describedby="activate-published-version-description" className="mt-0.5 h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" />
            <div>
              <label htmlFor="activate-published-version" className="block cursor-pointer text-sm font-semibold text-gray-900 dark:text-slate-100">Activate after publishing</label>
              <p id="activate-published-version-description" className="mt-0.5 text-xs text-gray-500 dark:text-slate-400">New workflow runs will start on version {version}. Existing runs are unchanged.</p>
            </div>
          </div>
          {error ? <p className="text-sm text-red-600 dark:text-red-300">{error}</p> : null}
        </div>

        <div className="flex justify-end gap-2 border-t border-gray-200 px-5 py-4 dark:border-slate-800">
          <button type="button" onClick={onClose} disabled={isPending} className="rounded-lg border border-gray-200 px-3 py-2 text-sm font-semibold text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800">Cancel</button>
          <button type="button" onClick={() => onPublish(activate)} disabled={isPending} className="inline-flex items-center gap-2 rounded-lg bg-primary-600 px-3 py-2 text-sm font-semibold text-white hover:bg-primary-700 disabled:opacity-50">
            <Send className="h-4 w-4" />
            {publishLabel}
          </button>
        </div>
      </dialog>
    </div>
  )
}
