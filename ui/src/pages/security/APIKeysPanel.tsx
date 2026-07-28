import { useState, type FormEvent } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Copy, KeyRound, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { useAuth } from '../../auth/AuthProvider'
import { apiKeysApi, workflowApi, type APIKeyInput } from '../../services/api'
import type { APIKeyGrant, APIKeyRecord, APIKeySecret } from '../../types'
import SecurityDialog from './SecurityDialog'

const actions: APIKeyGrant['action'][] = ['start', 'signal', 'status.read', 'result.read']

interface KeyGrantDraft extends APIKeyGrant {
  draftId: string
}

const emptyGrant = (): KeyGrantDraft => ({
  draftId: crypto.randomUUID(),
  workflowDefinitionId: '', action: 'start', instanceScope: 'own',
  allowPinnedVersions: false, allowCallbackUrl: false, signalNames: [],
})

interface KeyDraft extends APIKeyInput {
  grants: KeyGrantDraft[]
  expiresAt: string
}

const emptyDraft = (): KeyDraft => ({ name: '', description: '', expiresAt: '', grants: [emptyGrant()] })

function toInput(key: APIKeyRecord): KeyDraft {
  return {
    name: key.name,
    description: key.description,
    expiresAt: key.expiresAt ? key.expiresAt.slice(0, 16) : '',
    grants: key.grants.map((grant) => ({ ...grant, draftId: crypto.randomUUID(), signalNames: grant.signalNames ?? [] })),
  }
}

function toAPIGrant(grant: KeyGrantDraft): APIKeyGrant {
  return {
    workflowDefinitionId: grant.workflowDefinitionId,
    action: grant.action,
    instanceScope: grant.instanceScope,
    allowPinnedVersions: grant.allowPinnedVersions,
    allowCallbackUrl: grant.allowCallbackUrl,
    signalNames: grant.signalNames,
  }
}

export default function APIKeysPanel() {
  const queryClient = useQueryClient()
  const { hasPermission } = useAuth()
  const [editing, setEditing] = useState<APIKeyRecord | null | 'new'>(null)
  const [draft, setDraft] = useState<KeyDraft>(emptyDraft)
  const [revealed, setRevealed] = useState<APIKeySecret | null>(null)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const { data, isLoading } = useQuery({ queryKey: ['api-keys'], queryFn: apiKeysApi.list })
  const { data: definitions } = useQuery({ queryKey: ['workflow-definitions'], queryFn: workflowApi.listDefinitions })
  const canCreate = hasPermission('api_key.create')
  const canManage = hasPermission('api_key.manage_own') || hasPermission('api_key.manage_all')

  const updateGrant = (index: number, patch: Partial<APIKeyGrant>) => {
    setDraft((current) => ({ ...current, grants: current.grants.map((grant, itemIndex) => itemIndex === index ? { ...grant, ...patch } : grant) }))
  }

  const openNew = () => {
    setEditing('new')
    setDraft(emptyDraft())
    setError('')
  }

  const openEdit = (key: APIKeyRecord) => {
    setEditing(key)
    setDraft(toInput(key))
    setError('')
  }

  const save = async (event: FormEvent) => {
    event.preventDefault()
    if (!editing) return
    setSaving(true)
    setError('')
    const input: APIKeyInput = {
      name: draft.name,
      description: draft.description,
      expiresAt: draft.expiresAt ? new Date(draft.expiresAt).toISOString() : undefined,
      grants: draft.grants.map(toAPIGrant),
    }
    try {
      if (editing === 'new') {
        const created = await apiKeysApi.create(input)
        setRevealed(created)
      } else {
        await apiKeysApi.update(editing.id, input)
      }
      setEditing(null)
      await queryClient.invalidateQueries({ queryKey: ['api-keys'] })
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'API key update failed')
    } finally {
      setSaving(false)
    }
  }

  const rotate = async (key: APIKeyRecord) => {
    if (!window.confirm(`Rotate ${key.name}? The current secret will stop working immediately.`)) return
    try {
      setRevealed(await apiKeysApi.rotate(key.id))
      await queryClient.invalidateQueries({ queryKey: ['api-keys'] })
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'API key rotation failed')
    }
  }

  const revoke = async (key: APIKeyRecord) => {
    if (!window.confirm(`Revoke ${key.name}?`)) return
    try {
      await apiKeysApi.revoke(key.id)
      await queryClient.invalidateQueries({ queryKey: ['api-keys'] })
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'API key revocation failed')
    }
  }

  return (
    <div>
      <div className="mb-4 flex items-center justify-between gap-4">
        <div className="text-sm text-gray-500 dark:text-slate-400">{data?.total ?? 0} keys</div>
        {canCreate && <button type="button" onClick={openNew} className="inline-flex items-center gap-2 rounded-md bg-primary-600 px-3.5 py-2 text-sm font-semibold text-white hover:bg-primary-700"><Plus className="h-4 w-4" /> Create key</button>}
      </div>
      {error && !editing && <div className="mb-4 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-950/30 dark:text-red-300">{error}</div>}
      <div className="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-slate-800 dark:bg-slate-900">
        <div className="overflow-x-auto"><table className="w-full text-left text-sm"><thead className="border-b border-gray-200 bg-gray-50 text-xs uppercase text-gray-500 dark:border-slate-800 dark:bg-slate-950/50 dark:text-slate-400"><tr><th className="px-4 py-3">Key</th><th className="px-4 py-3">Status</th><th className="px-4 py-3">Expires</th><th className="px-4 py-3">Last used</th><th className="px-4 py-3 text-right">Actions</th></tr></thead><tbody className="divide-y divide-gray-100 dark:divide-slate-800">
          {data?.apiKeys.map((key) => (
            <tr key={key.id}>
              <td className="px-4 py-3"><div className="font-medium text-gray-900 dark:text-slate-100">{key.name}</div><div className="font-mono text-xs text-gray-500">orch_{key.keyPrefix}_...</div><div className="mt-1 text-xs text-gray-500">{key.grants.length} workflow grants</div></td>
              <td className="px-4 py-3 capitalize">{key.status}</td>
              <td className="px-4 py-3 text-gray-500 dark:text-slate-400">{key.expiresAt ? new Date(key.expiresAt).toLocaleDateString() : 'Never'}</td>
              <td className="px-4 py-3 text-gray-500 dark:text-slate-400">{key.lastUsedAt ? new Date(key.lastUsedAt).toLocaleString() : 'Never'}</td>
              <td className="px-4 py-3"><div className="flex justify-end gap-1">{canManage && <><button type="button" onClick={() => openEdit(key)} title="Edit key" aria-label="Edit key" className="flex h-8 w-8 items-center justify-center rounded-md text-gray-500 hover:bg-gray-100 dark:hover:bg-slate-800"><Pencil className="h-4 w-4" /></button><button type="button" onClick={() => void rotate(key)} disabled={key.status !== 'active'} title="Rotate key" aria-label="Rotate key" className="flex h-8 w-8 items-center justify-center rounded-md text-gray-500 hover:bg-gray-100 disabled:opacity-40 dark:hover:bg-slate-800"><RefreshCw className="h-4 w-4" /></button><button type="button" onClick={() => void revoke(key)} disabled={key.status !== 'active'} title="Revoke key" aria-label="Revoke key" className="flex h-8 w-8 items-center justify-center rounded-md text-red-600 hover:bg-red-50 disabled:opacity-40 dark:hover:bg-red-950/40"><Trash2 className="h-4 w-4" /></button></>}</div></td>
            </tr>
          ))}
          {!isLoading && !data?.apiKeys.length && <tr><td colSpan={5} className="px-4 py-10 text-center text-gray-500">No API keys</td></tr>}
        </tbody></table></div>
      </div>

      {editing && (
        <SecurityDialog title={editing === 'new' ? 'Create API key' : `Edit ${editing.name}`} onClose={() => setEditing(null)}>
          <form onSubmit={save} className="space-y-5 p-5">
            <div className="grid gap-4 sm:grid-cols-2"><label className="text-sm font-medium">Name<input autoFocus value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} required maxLength={128} className="mt-1.5 w-full rounded-md border border-gray-300 bg-white px-3 py-2 dark:border-slate-700 dark:bg-slate-950" /></label><label className="text-sm font-medium">Expires at<input type="datetime-local" value={draft.expiresAt} onChange={(event) => setDraft({ ...draft, expiresAt: event.target.value })} className="mt-1.5 w-full rounded-md border border-gray-300 bg-white px-3 py-2 dark:border-slate-700 dark:bg-slate-950" /></label></div>
            <label className="block text-sm font-medium">Description<textarea value={draft.description} onChange={(event) => setDraft({ ...draft, description: event.target.value })} rows={2} maxLength={1024} className="mt-1.5 w-full rounded-md border border-gray-300 bg-white px-3 py-2 dark:border-slate-700 dark:bg-slate-950" /></label>
            <div>
              <div className="mb-2 flex items-center justify-between"><span className="text-sm font-semibold">Workflow grants</span><button type="button" onClick={() => setDraft({ ...draft, grants: [...draft.grants, emptyGrant()] })} className="inline-flex items-center gap-1 rounded-md border border-gray-300 px-2.5 py-1.5 text-xs dark:border-slate-700"><Plus className="h-3.5 w-3.5" /> Add grant</button></div>
              <div className="space-y-3">
                {draft.grants.map((grant, index) => (
                  <div key={grant.draftId} className="rounded-md border border-gray-200 p-3 dark:border-slate-700">
                    <div className="grid gap-3 sm:grid-cols-[minmax(0,2fr)_minmax(0,1fr)_minmax(0,1fr)_auto]">
                      <label className="text-xs font-medium">Workflow<select value={grant.workflowDefinitionId} onChange={(event) => updateGrant(index, { workflowDefinitionId: event.target.value })} required className="mt-1 w-full rounded-md border border-gray-300 bg-white px-2 py-2 text-sm dark:border-slate-700 dark:bg-slate-950"><option value="">Select workflow</option>{definitions?.definitions.map((definition) => <option value={definition.id} key={definition.id}>{definition.name}</option>)}</select></label>
                      <label className="text-xs font-medium">Action<select value={grant.action} onChange={(event) => updateGrant(index, { action: event.target.value as APIKeyGrant['action'] })} className="mt-1 w-full rounded-md border border-gray-300 bg-white px-2 py-2 text-sm dark:border-slate-700 dark:bg-slate-950">{actions.map((action) => <option key={action}>{action}</option>)}</select></label>
                      <label className="text-xs font-medium">Instance scope<select value={grant.instanceScope} onChange={(event) => updateGrant(index, { instanceScope: event.target.value as APIKeyGrant['instanceScope'] })} className="mt-1 w-full rounded-md border border-gray-300 bg-white px-2 py-2 text-sm dark:border-slate-700 dark:bg-slate-950"><option value="own">Own</option><option value="definition">Definition</option></select></label>
                      <button type="button" onClick={() => setDraft({ ...draft, grants: draft.grants.filter((_, itemIndex) => itemIndex !== index) })} disabled={draft.grants.length === 1} title="Remove grant" aria-label="Remove grant" className="mt-5 flex h-9 w-9 items-center justify-center rounded-md text-red-600 hover:bg-red-50 disabled:opacity-30 dark:hover:bg-red-950/40"><Trash2 className="h-4 w-4" /></button>
                    </div>
                    <div className="mt-3 flex flex-wrap gap-4 text-xs"><label className="flex items-center gap-2"><input type="checkbox" checked={grant.allowPinnedVersions} onChange={(event) => updateGrant(index, { allowPinnedVersions: event.target.checked })} /> Pinned versions</label><label className="flex items-center gap-2"><input type="checkbox" checked={grant.allowCallbackUrl} onChange={(event) => updateGrant(index, { allowCallbackUrl: event.target.checked })} /> Callback URL</label>{grant.action === 'signal' && <label className="min-w-56 flex-1">Signal names<input value={(grant.signalNames ?? []).join(', ')} onChange={(event) => updateGrant(index, { signalNames: event.target.value.split(',').map((value) => value.trim()).filter(Boolean) })} placeholder="Any signal" className="ml-2 rounded-md border border-gray-300 bg-white px-2 py-1 dark:border-slate-700 dark:bg-slate-950" /></label>}</div>
                  </div>
                ))}
              </div>
            </div>
            {error && <p className="text-sm text-red-600 dark:text-red-300">{error}</p>}
            <div className="flex justify-end gap-2"><button type="button" onClick={() => setEditing(null)} className="rounded-md border border-gray-300 px-3.5 py-2 text-sm dark:border-slate-700">Cancel</button><button type="submit" disabled={saving} className="rounded-md bg-primary-600 px-3.5 py-2 text-sm font-semibold text-white disabled:opacity-60">{editing === 'new' ? 'Create key' : 'Save changes'}</button></div>
          </form>
        </SecurityDialog>
      )}

      {revealed && (
        <SecurityDialog title="API key secret" onClose={() => setRevealed(null)}>
          <div className="p-5"><div className="flex items-start gap-2 rounded-md bg-gray-100 p-4 dark:bg-slate-950"><code className="min-w-0 flex-1 break-all text-sm">{revealed.secret}</code><button type="button" onClick={() => void navigator.clipboard.writeText(revealed.secret)} title="Copy secret" aria-label="Copy secret" className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md hover:bg-white dark:hover:bg-slate-800"><Copy className="h-4 w-4" /></button></div><div className="mt-4 flex items-center gap-2 text-sm text-amber-700 dark:text-amber-300"><KeyRound className="h-4 w-4" /> This secret is shown once.</div></div>
        </SecurityDialog>
      )}
    </div>
  )
}
