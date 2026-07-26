import { useState, type FormEvent } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Filter } from 'lucide-react'
import { auditApi } from '../../services/api'

export default function AuditPanel() {
  const [actionInput, setActionInput] = useState('')
  const [outcomeInput, setOutcomeInput] = useState('')
  const [filters, setFilters] = useState<{ action?: string; outcome?: string }>({})
  const { data, isLoading } = useQuery({ queryKey: ['audit-events', filters], queryFn: () => auditApi.list(filters) })

  const applyFilters = (event: FormEvent) => {
    event.preventDefault()
    setFilters({ action: actionInput || undefined, outcome: outcomeInput || undefined })
  }

  return (
    <div>
      <form onSubmit={applyFilters} className="mb-4 flex flex-col gap-3 sm:flex-row">
        <input value={actionInput} onChange={(event) => setActionInput(event.target.value)} placeholder="Action" className="rounded-md border border-gray-300 bg-white px-3 py-2 text-sm dark:border-slate-700 dark:bg-slate-900" />
        <select value={outcomeInput} onChange={(event) => setOutcomeInput(event.target.value)} className="rounded-md border border-gray-300 bg-white px-3 py-2 text-sm dark:border-slate-700 dark:bg-slate-900"><option value="">All outcomes</option><option value="success">Success</option><option value="denied">Denied</option><option value="failure">Failure</option></select>
        <button className="inline-flex items-center justify-center gap-2 rounded-md border border-gray-300 px-3.5 py-2 text-sm font-medium hover:bg-gray-50 dark:border-slate-700 dark:hover:bg-slate-800"><Filter className="h-4 w-4" /> Apply</button>
      </form>
      <div className="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-slate-800 dark:bg-slate-900">
        <div className="overflow-x-auto"><table className="w-full text-left text-sm"><thead className="border-b border-gray-200 bg-gray-50 text-xs uppercase text-gray-500 dark:border-slate-800 dark:bg-slate-950/50 dark:text-slate-400"><tr><th className="px-4 py-3">Time</th><th className="px-4 py-3">Actor</th><th className="px-4 py-3">Action</th><th className="px-4 py-3">Resource</th><th className="px-4 py-3">Outcome</th><th className="px-4 py-3">Source</th></tr></thead><tbody className="divide-y divide-gray-100 dark:divide-slate-800">
          {data?.events.map((event) => <tr key={event.id}><td className="whitespace-nowrap px-4 py-3 text-gray-500 dark:text-slate-400">{new Date(event.occurredAt).toLocaleString()}</td><td className="px-4 py-3"><div>{event.actorType}</div><div className="max-w-40 truncate font-mono text-xs text-gray-500" title={event.actorId}>{event.actorId || '-'}</div></td><td className="px-4 py-3 font-mono text-xs">{event.action}</td><td className="px-4 py-3"><div>{event.resourceType || '-'}</div><div className="max-w-40 truncate font-mono text-xs text-gray-500" title={event.resourceId}>{event.resourceId}</div></td><td className="px-4 py-3"><span className={event.outcome === 'success' ? 'text-emerald-700 dark:text-emerald-300' : 'text-red-700 dark:text-red-300'}>{event.outcome}</span></td><td className="px-4 py-3 text-gray-500 dark:text-slate-400">{event.sourceIp || '-'}</td></tr>)}
          {!isLoading && !data?.events.length && <tr><td colSpan={6} className="px-4 py-10 text-center text-gray-500">No audit events</td></tr>}
        </tbody></table></div>
      </div>
    </div>
  )
}
