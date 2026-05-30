import { useState } from 'react'
import { Check, FileJson, X } from 'lucide-react'
import type { ImportAnalysis, ImportItem } from '../types'

const typeLabel: Record<ImportItem['type'], string> = {
  definition: 'Workflow',
  script: 'Script',
  agent: 'Agent',
  connector: 'Connector',
}

const typeColor: Record<ImportItem['type'], string> = {
  definition: 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300',
  script: 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300',
  agent: 'bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-300',
  connector: 'bg-teal-100 text-teal-700 dark:bg-teal-900/30 dark:text-teal-300',
}

interface Props {
  analysis: ImportAnalysis
  onConfirm: (overrideIds: string[]) => void
  onClose: () => void
  isPending: boolean
}

export default function ImportModal({ analysis, onConfirm, onClose, isPending }: Props) {
  // Conflicts start unchecked (safe default: keep existing).
  // User can check a conflict to override it.
  const [overrideIds, setOverrideIds] = useState<Set<string>>(new Set())

  const toggle = (id: string) =>
    setOverrideIds((prev) => {
      const next = new Set(prev)
      if (next.has(id)) { next.delete(id) } else { next.add(id) }
      return next
    })

  const totalImported = analysis.ready.length + overrideIds.size

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 backdrop-blur-sm">
      <div className="mx-4 w-full max-w-lg rounded-2xl border border-gray-200 bg-white shadow-2xl dark:border-slate-700 dark:bg-slate-900">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-slate-700">
          <div className="flex items-center gap-2">
            <FileJson className="h-5 w-5 text-primary-600 dark:text-primary-400" />
            <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">Import bundle</h2>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg p-1 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 dark:text-slate-500 dark:hover:bg-slate-800 dark:hover:text-slate-300"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="max-h-[60vh] space-y-5 overflow-y-auto px-6 py-5">
          {/* Ready items — always imported, no checkboxes */}
          {analysis.ready.length > 0 && (
            <div>
              <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">
                Will be imported ({analysis.ready.length})
              </p>
              <ul className="space-y-1.5">
                {analysis.ready.map((item) => (
                  <li key={item.id} className="flex items-center gap-3 rounded-lg bg-emerald-50 px-3 py-2 dark:bg-emerald-900/10">
                    <Check className="h-3.5 w-3.5 shrink-0 text-emerald-500" />
                    <span className="flex-1 truncate text-sm font-medium text-gray-900 dark:text-slate-100">{item.name}</span>
                    <span className={`shrink-0 rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${typeColor[item.type]}`}>
                      {typeLabel[item.type]}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          )}

          {/* Conflict items — already exist, user chooses to override or keep */}
          {analysis.conflicts.length > 0 && (
            <div>
              <p className="mb-1 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">
                Already exist — choose action ({analysis.conflicts.length})
              </p>
              <p className="mb-2.5 text-[11px] text-gray-400 dark:text-slate-500">
                Unchecked items are skipped. The dependent will continue using the existing version.
              </p>
              <ul className="space-y-1.5">
                {analysis.conflicts.map((item) => {
                  const checked = overrideIds.has(item.id)
                  return (
                    <li key={item.id}>
                      <label className="flex cursor-pointer items-center gap-3 rounded-lg border border-gray-200 px-3 py-2 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:hover:bg-slate-800">
                        <input
                          type="checkbox"
                          checked={checked}
                          onChange={() => toggle(item.id)}
                          className="h-4 w-4 rounded accent-primary-600"
                        />
                        <span className="flex-1 truncate text-sm font-medium text-gray-900 dark:text-slate-100">{item.name}</span>
                        <span className={`shrink-0 rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${typeColor[item.type]}`}>
                          {typeLabel[item.type]}
                        </span>
                        <span className={`shrink-0 text-[10px] font-semibold ${checked ? 'text-amber-600 dark:text-amber-400' : 'text-gray-400 dark:text-slate-500'}`}>
                          {checked ? 'Override' : 'Keep existing'}
                        </span>
                      </label>
                    </li>
                  )
                })}
              </ul>
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between border-t border-gray-200 px-6 py-4 dark:border-slate-700">
          <p className="text-xs text-gray-500 dark:text-slate-400">
            {totalImported === 0 ? 'Nothing to import' : `${totalImported} item${totalImported !== 1 ? 's' : ''} will be imported`}
          </p>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={onClose}
              disabled={isPending}
              className="rounded-lg border border-gray-200 px-4 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 disabled:opacity-60 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={() => onConfirm([...overrideIds])}
              disabled={isPending || totalImported === 0}
              className="inline-flex items-center gap-1.5 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700 disabled:cursor-not-allowed disabled:opacity-60"
            >
              {isPending ? 'Importing…' : `Import${totalImported > 0 ? ` (${totalImported})` : ''}`}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
