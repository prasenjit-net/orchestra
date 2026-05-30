import { useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, CheckCircle, Cpu, Network, RefreshCw, WifiOff, XCircle } from 'lucide-react'
import SectionHeader from '../components/SectionHeader'
import StatCard from '../components/StatCard'
import { buildWebSocketUrl, clusterApi } from '../services/api'
import type { ClusterNode, NodeHealthResult } from '../types'

function roleBadge(role: ClusterNode['role']) {
  const classes: Record<ClusterNode['role'], string> = {
    controller: 'bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300',
    worker: 'bg-purple-100 text-purple-700 dark:bg-purple-900/40 dark:text-purple-300',
    all: 'bg-teal-100 text-teal-700 dark:bg-teal-900/40 dark:text-teal-300',
  }
  return (
    <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${classes[role]}`}>
      {role}
    </span>
  )
}

function statusBadge(status: ClusterNode['status']) {
  if (status === 'online') {
    return (
      <span className="inline-flex items-center gap-1 text-xs font-medium text-green-600 dark:text-green-400">
        <span className="h-2 w-2 rounded-full bg-green-500" />
        Online
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1 text-xs font-medium text-red-500 dark:text-red-400">
      <span className="h-2 w-2 rounded-full bg-red-500" />
      Offline
    </span>
  )
}

function relativeTime(iso: string) {
  const diff = Date.now() - new Date(iso).getTime()
  if (diff < 5_000) return 'just now'
  if (diff < 60_000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  return `${Math.floor(diff / 3_600_000)}h ago`
}

export default function ClusterPage() {
  const queryClient = useQueryClient()

  const { data: nodes = [], isLoading } = useQuery({
    queryKey: ['cluster-nodes'],
    queryFn: () => clusterApi.listNodes(),
    refetchInterval: 10_000,
  })

  const healthMutation = useMutation({
    mutationFn: () => clusterApi.checkHealth(),
  })

  // Re-fetch on nodes.updated WebSocket events.
  useEffect(() => {
    const ws = new WebSocket(buildWebSocketUrl())
    ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data as string)
        if (msg?.type === 'nodes.updated') {
          queryClient.invalidateQueries({ queryKey: ['cluster-nodes'] })
        }
      } catch {
        // ignore parse errors
      }
    }
    return () => ws.close()
  }, [queryClient])

  const online = nodes.filter((n) => n.status === 'online').length
  const offline = nodes.filter((n) => n.status === 'offline').length

  return (
    <div className="space-y-6 p-6">
      <SectionHeader
        title="Cluster"
        description="Registered nodes and their health status"
        action={
          <button
            type="button"
            onClick={() => healthMutation.mutate()}
            disabled={healthMutation.isPending || nodes.length === 0}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 px-4 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-60 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
          >
            <RefreshCw className={`h-4 w-4 ${healthMutation.isPending ? 'animate-spin' : ''}`} />
            {healthMutation.isPending ? 'Checking…' : 'Check health'}
          </button>
        }
      />

      {/* Summary cards */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <StatCard
          label="Total nodes"
          value={String(nodes.length)}
          description="Registered in this cluster"
          icon={Network}
          tone="bg-blue-50 text-blue-600 dark:bg-blue-900/30 dark:text-blue-400"
        />
        <StatCard
          label="Online"
          value={String(online)}
          description={`Seen within the last 30s`}
          icon={Activity}
          tone="bg-green-50 text-green-600 dark:bg-green-900/30 dark:text-green-400"
        />
        <StatCard
          label="Offline"
          value={String(offline)}
          description="Heartbeat not received recently"
          icon={WifiOff}
          tone={offline > 0 ? 'bg-red-50 text-red-600 dark:bg-red-900/30 dark:text-red-400' : 'bg-gray-50 text-gray-400 dark:bg-slate-800 dark:text-slate-500'}
        />
      </div>

      {/* Warning banner */}
      {!isLoading && online === 0 && (
        <div className="flex items-start gap-3 rounded-lg border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-700/50 dark:bg-amber-900/20 dark:text-amber-300">
          <WifiOff className="mt-0.5 h-4 w-4 shrink-0" />
          <span>
            <strong>No online nodes found.</strong> Workflow tasks will not execute until a worker node comes online.
          </span>
        </div>
      )}

      {/* Health check results */}
      {(healthMutation.data || healthMutation.isPending || healthMutation.error) && (
        <div className="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
          <div className="border-b border-gray-200 px-6 py-4 dark:border-slate-800">
            <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">Health check results</h2>
            {healthMutation.data && (
              <p className="mt-0.5 text-xs text-gray-500 dark:text-slate-400">
                Checked at {new Date(healthMutation.data[0]?.checkedAt ?? '').toLocaleTimeString()}
              </p>
            )}
          </div>
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-gray-200 text-sm dark:divide-slate-800">
              <thead className="bg-gray-50 dark:bg-slate-800/60">
                <tr>
                  {['Node ID', 'Address', 'Result', 'HTTP Status', 'Latency'].map((h) => (
                    <th
                      key={h}
                      className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400"
                    >
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-slate-800">
                {healthMutation.isPending && (
                  <tr>
                    <td colSpan={5} className="px-4 py-6 text-center text-gray-400 dark:text-slate-500">
                      Probing nodes…
                    </td>
                  </tr>
                )}
                {healthMutation.error && (
                  <tr>
                    <td colSpan={5} className="px-4 py-6 text-center text-red-500 dark:text-red-400">
                      {(healthMutation.error as Error).message}
                    </td>
                  </tr>
                )}
                {healthMutation.data?.map((r: NodeHealthResult) => (
                  <tr key={r.id} className="hover:bg-gray-50 dark:hover:bg-slate-800/40">
                    <td className="px-4 py-3 font-mono text-xs text-gray-700 dark:text-slate-300">
                      {r.id.length > 20 ? r.id.slice(0, 20) + '…' : r.id}
                    </td>
                    <td className="px-4 py-3 font-mono text-xs text-gray-500 dark:text-slate-400">
                      {r.address || '—'}
                    </td>
                    <td className="px-4 py-3">
                      {r.ok ? (
                        <span className="inline-flex items-center gap-1 text-xs font-medium text-green-600 dark:text-green-400">
                          <CheckCircle className="h-3.5 w-3.5" /> Healthy
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1 text-xs font-medium text-red-600 dark:text-red-400" title={r.error}>
                          <XCircle className="h-3.5 w-3.5" /> {r.error ? 'Unreachable' : 'Unhealthy'}
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-slate-400">
                      {r.status ? (
                        <span className={r.ok ? 'text-green-600 dark:text-green-400' : 'text-red-600 dark:text-red-400'}>
                          {r.status}
                        </span>
                      ) : '—'}
                    </td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-slate-400">
                      {r.latencyMs > 0 ? `${r.latencyMs} ms` : '—'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Nodes table */}
      <div className="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <div className="overflow-x-auto">
          <table className="min-w-full divide-y divide-gray-200 dark:divide-slate-800 text-sm">
            <thead className="bg-gray-50 dark:bg-slate-800/60">
              <tr>
                {['Node ID', 'Role', 'Status', 'Address', 'Version', 'Hostname', 'Max Concurrent', 'Capabilities', 'Last Seen'].map((h) => (
                  <th
                    key={h}
                    className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400"
                  >
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100 dark:divide-slate-800">
              {isLoading && (
                <tr>
                  <td colSpan={9} className="px-4 py-8 text-center text-gray-400 dark:text-slate-500">
                    Loading nodes…
                  </td>
                </tr>
              )}
              {!isLoading && nodes.length === 0 && (
                <tr>
                  <td colSpan={9} className="px-4 py-8 text-center text-gray-400 dark:text-slate-500">
                    No nodes registered yet.
                  </td>
                </tr>
              )}
              {nodes.map((node) => (
                <tr key={node.id} className="hover:bg-gray-50 dark:hover:bg-slate-800/40">
                  <td className="px-4 py-3 font-mono text-xs text-gray-700 dark:text-slate-300">
                    <span title={node.id}>{node.id.length > 20 ? node.id.slice(0, 20) + '…' : node.id}</span>
                  </td>
                  <td className="px-4 py-3">{roleBadge(node.role)}</td>
                  <td className="px-4 py-3">{statusBadge(node.status)}</td>
                  <td className="px-4 py-3">
                    {node.address ? (
                      <a
                        href={node.address}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="font-mono text-xs text-blue-600 hover:underline dark:text-blue-400"
                      >
                        {node.address}
                      </a>
                    ) : (
                      <span className="text-gray-400 dark:text-slate-500">—</span>
                    )}
                  </td>
                  <td className="px-4 py-3 font-mono text-xs text-gray-600 dark:text-slate-400">{node.version || '—'}</td>
                  <td className="px-4 py-3 text-xs text-gray-600 dark:text-slate-400">{node.hostname || '—'}</td>
                  <td className="px-4 py-3 text-center text-xs text-gray-600 dark:text-slate-400">
                    {node.role === 'controller' ? (
                      <span className="text-gray-400 dark:text-slate-500">—</span>
                    ) : (
                      node.maxConcurrent
                    )}
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex flex-wrap gap-1">
                      {node.capabilities.length === 0 ? (
                        <span className="text-gray-400 dark:text-slate-500">—</span>
                      ) : (
                        node.capabilities.slice(0, 4).map((cap) => (
                          <span
                            key={cap}
                            className="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-xs text-gray-600 dark:bg-slate-700 dark:text-slate-300"
                          >
                            {cap}
                          </span>
                        ))
                      )}
                      {node.capabilities.length > 4 && (
                        <span className="rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-500 dark:bg-slate-700 dark:text-slate-400">
                          +{node.capabilities.length - 4}
                        </span>
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-3 text-xs text-gray-500 dark:text-slate-400" title={node.lastSeenAt}>
                    <span className="flex items-center gap-1">
                      <Cpu className="h-3 w-3" />
                      {relativeTime(node.lastSeenAt)}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}
