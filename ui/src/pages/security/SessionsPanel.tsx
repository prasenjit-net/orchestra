import { useQuery, useQueryClient } from '@tanstack/react-query'
import { LogOut } from 'lucide-react'
import { useAuth } from '../../auth/AuthProvider'
import { authApi } from '../../services/api'

export default function SessionsPanel() {
  const queryClient = useQueryClient()
  const { session } = useAuth()
  const { data, isLoading } = useQuery({ queryKey: ['auth-sessions'], queryFn: authApi.sessions })

  const revoke = async (id: string) => {
    if (id === session?.session.id && !window.confirm('Revoke this session and sign out?')) return
    await authApi.revokeSession(id)
    if (id === session?.session.id) {
      window.dispatchEvent(new CustomEvent('orchestra:unauthorized'))
      return
    }
    await queryClient.invalidateQueries({ queryKey: ['auth-sessions'] })
  }

  return (
    <div className="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-slate-800 dark:bg-slate-900">
      <div className="overflow-x-auto"><table className="w-full text-left text-sm"><thead className="border-b border-gray-200 bg-gray-50 text-xs uppercase text-gray-500 dark:border-slate-800 dark:bg-slate-950/50 dark:text-slate-400"><tr><th className="px-4 py-3">Created</th><th className="px-4 py-3">Last seen</th><th className="px-4 py-3">Expires</th><th className="px-4 py-3">Source</th><th className="px-4 py-3 text-right">Action</th></tr></thead><tbody className="divide-y divide-gray-100 dark:divide-slate-800">
        {data?.sessions.map((item) => <tr key={item.id}><td className="px-4 py-3">{item.createdAt ? new Date(item.createdAt).toLocaleString() : '-'}</td><td className="px-4 py-3 text-gray-500 dark:text-slate-400">{item.lastSeenAt ? new Date(item.lastSeenAt).toLocaleString() : '-'}</td><td className="px-4 py-3 text-gray-500 dark:text-slate-400">{new Date(item.idleExpiresAt).toLocaleString()}</td><td className="px-4 py-3 text-gray-500 dark:text-slate-400">{item.sourceIp || '-'}</td><td className="px-4 py-3 text-right"><button type="button" onClick={() => void revoke(item.id)} title="Revoke session" aria-label="Revoke session" className="inline-flex h-8 w-8 items-center justify-center rounded-md text-red-600 hover:bg-red-50 dark:hover:bg-red-950/40"><LogOut className="h-4 w-4" /></button></td></tr>)}
        {!isLoading && !data?.sessions.length && <tr><td colSpan={5} className="px-4 py-10 text-center text-gray-500">No active sessions</td></tr>}
      </tbody></table></div>
    </div>
  )
}
