import { useMemo, useState, type FormEvent } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound, Plus, Search } from 'lucide-react'
import { usersApi } from '../../services/api'
import type { AuthUser, EntitlementEffect, UserRole } from '../../types'
import { useAuth } from '../../auth/AuthProvider'
import SecurityDialog from './SecurityDialog'

const roles: UserRole[] = ['admin', 'developer', 'observer']

interface CreateDraft {
  username: string
  displayName: string
  role: UserRole
  password: string
}

const emptyCreate: CreateDraft = { username: '', displayName: '', role: 'observer', password: '' }

export default function UserManagementPanel() {
  const { hasPermission } = useAuth()
  const queryClient = useQueryClient()
  const [search, setSearch] = useState('')
  const [createOpen, setCreateOpen] = useState(false)
  const [createDraft, setCreateDraft] = useState<CreateDraft>(emptyCreate)
  const [editing, setEditing] = useState<AuthUser | null>(null)
  const [editRole, setEditRole] = useState<UserRole>('observer')
  const [editStatus, setEditStatus] = useState('active')
  const [editDisplayName, setEditDisplayName] = useState('')
  const [overrides, setOverrides] = useState<Record<string, 'inherit' | EntitlementEffect>>({})
  const [temporaryPassword, setTemporaryPassword] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const { data, isLoading } = useQuery({ queryKey: ['security-users', search], queryFn: () => usersApi.list(search) })
  const { data: permissionData } = useQuery({ queryKey: ['permissions'], queryFn: usersApi.permissions })

  const permissions = useMemo(() => permissionData?.permissions ?? [], [permissionData])
  const canManageUsers = hasPermission('user.manage')
  const canManageEntitlements = hasPermission('entitlement.manage')

  const openEditor = (user: AuthUser) => {
    const next: Record<string, 'inherit' | EntitlementEffect> = {}
    for (const entitlement of user.entitlements ?? []) next[entitlement.permission] = entitlement.effect
    setEditing(user)
    setEditRole(user.role)
    setEditStatus(user.status)
    setEditDisplayName(user.displayName)
    setOverrides(next)
    setError('')
  }

  const createUser = async (event: FormEvent) => {
    event.preventDefault()
    setSaving(true)
    setError('')
    try {
      const result = await usersApi.create({
        username: createDraft.username,
        displayName: createDraft.displayName,
        role: createDraft.role,
        password: createDraft.password || undefined,
      })
      setCreateOpen(false)
      setCreateDraft(emptyCreate)
      setTemporaryPassword(result.temporaryPassword ?? '')
      await queryClient.invalidateQueries({ queryKey: ['security-users'] })
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'User creation failed')
    } finally {
      setSaving(false)
    }
  }

  const saveUser = async (event: FormEvent) => {
    event.preventDefault()
    if (!editing) return
    setSaving(true)
    setError('')
    try {
      if (canManageUsers) {
        await usersApi.update(editing.id, {
          username: editing.username,
          displayName: editDisplayName,
          role: editRole,
          status: editStatus,
        })
      }
      if (canManageEntitlements) {
        await usersApi.replaceEntitlements(
          editing.id,
          Object.entries(overrides)
            .filter(([, effect]) => effect !== 'inherit')
            .map(([permission, effect]) => ({ permission, effect: effect as EntitlementEffect })),
        )
      }
      setEditing(null)
      await queryClient.invalidateQueries({ queryKey: ['security-users'] })
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'User update failed')
    } finally {
      setSaving(false)
    }
  }

  const resetPassword = async () => {
    if (!editing || !window.confirm(`Reset the password for ${editing.username}?`)) return
    setSaving(true)
    setError('')
    try {
      const result = await usersApi.resetPassword(editing.id)
      setTemporaryPassword(result.temporaryPassword)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Password reset failed')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div>
      <div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <label className="relative block w-full max-w-sm">
          <Search className="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-gray-400" />
          <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search users" className="w-full rounded-md border border-gray-300 bg-white py-2 pl-9 pr-3 text-sm dark:border-slate-700 dark:bg-slate-900" />
        </label>
        {canManageUsers && <button type="button" onClick={() => { setCreateOpen(true); setError('') }} className="inline-flex items-center justify-center gap-2 rounded-md bg-primary-600 px-3.5 py-2 text-sm font-semibold text-white hover:bg-primary-700"><Plus className="h-4 w-4" /> Add user</button>}
      </div>
      <div className="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-slate-800 dark:bg-slate-900">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead className="border-b border-gray-200 bg-gray-50 text-xs uppercase text-gray-500 dark:border-slate-800 dark:bg-slate-950/50 dark:text-slate-400"><tr><th className="px-4 py-3">User</th><th className="px-4 py-3">Role</th><th className="px-4 py-3">Status</th><th className="px-4 py-3">Last sign in</th></tr></thead>
            <tbody className="divide-y divide-gray-100 dark:divide-slate-800">
              {data?.users.map((user) => (
                <tr key={user.id} onClick={() => (canManageUsers || canManageEntitlements) && openEditor(user)} className={canManageUsers || canManageEntitlements ? 'cursor-pointer hover:bg-gray-50 dark:hover:bg-slate-800/60' : ''}>
                  <td className="px-4 py-3"><div className="font-medium text-gray-900 dark:text-slate-100">{user.displayName || user.username}</div><div className="text-xs text-gray-500 dark:text-slate-400">{user.username}</div></td>
                  <td className="px-4 py-3 capitalize">{user.role}</td>
                  <td className="px-4 py-3"><span className={user.status === 'active' ? 'text-emerald-700 dark:text-emerald-300' : 'text-gray-500'}>{user.status}</span>{user.mustChangePassword && <div className="text-xs text-amber-700 dark:text-amber-300">Password change required</div>}</td>
                  <td className="px-4 py-3 text-gray-500 dark:text-slate-400">{user.lastLoginAt ? new Date(user.lastLoginAt).toLocaleString() : 'Never'}</td>
                </tr>
              ))}
              {!isLoading && !data?.users.length && <tr><td colSpan={4} className="px-4 py-10 text-center text-gray-500">No users found</td></tr>}
            </tbody>
          </table>
        </div>
      </div>

      {createOpen && (
        <SecurityDialog title="Add user" onClose={() => setCreateOpen(false)}>
          <form onSubmit={createUser} className="space-y-4 p-5">
            <div className="grid gap-4 sm:grid-cols-2">
              <label className="text-sm font-medium">Username<input autoFocus value={createDraft.username} onChange={(event) => setCreateDraft({ ...createDraft, username: event.target.value })} required className="mt-1.5 w-full rounded-md border border-gray-300 bg-white px-3 py-2 dark:border-slate-700 dark:bg-slate-950" /></label>
              <label className="text-sm font-medium">Display name<input value={createDraft.displayName} onChange={(event) => setCreateDraft({ ...createDraft, displayName: event.target.value })} className="mt-1.5 w-full rounded-md border border-gray-300 bg-white px-3 py-2 dark:border-slate-700 dark:bg-slate-950" /></label>
            </div>
            <label className="block text-sm font-medium">Role<select value={createDraft.role} onChange={(event) => setCreateDraft({ ...createDraft, role: event.target.value as UserRole })} className="mt-1.5 w-full rounded-md border border-gray-300 bg-white px-3 py-2 capitalize dark:border-slate-700 dark:bg-slate-950">{roles.map((role) => <option key={role}>{role}</option>)}</select></label>
            <label className="block text-sm font-medium">Temporary password<input type="password" minLength={12} maxLength={128} value={createDraft.password} onChange={(event) => setCreateDraft({ ...createDraft, password: event.target.value })} placeholder="Generate automatically" className="mt-1.5 w-full rounded-md border border-gray-300 bg-white px-3 py-2 dark:border-slate-700 dark:bg-slate-950" /></label>
            {error && <p className="text-sm text-red-600 dark:text-red-300">{error}</p>}
            <div className="flex justify-end gap-2"><button type="button" onClick={() => setCreateOpen(false)} className="rounded-md border border-gray-300 px-3.5 py-2 text-sm dark:border-slate-700">Cancel</button><button type="submit" disabled={saving} className="rounded-md bg-primary-600 px-3.5 py-2 text-sm font-semibold text-white disabled:opacity-60">Create user</button></div>
          </form>
        </SecurityDialog>
      )}

      {editing && (
        <SecurityDialog title={editing.username} onClose={() => setEditing(null)}>
          <form onSubmit={saveUser} className="space-y-5 p-5">
            <div className="grid gap-4 sm:grid-cols-3">
              <label className="text-sm font-medium sm:col-span-1">Display name<input disabled={!canManageUsers} value={editDisplayName} onChange={(event) => setEditDisplayName(event.target.value)} className="mt-1.5 w-full rounded-md border border-gray-300 bg-white px-3 py-2 disabled:opacity-60 dark:border-slate-700 dark:bg-slate-950" /></label>
              <label className="text-sm font-medium">Role<select disabled={!canManageUsers} value={editRole} onChange={(event) => setEditRole(event.target.value as UserRole)} className="mt-1.5 w-full rounded-md border border-gray-300 bg-white px-3 py-2 capitalize disabled:opacity-60 dark:border-slate-700 dark:bg-slate-950">{roles.map((role) => <option key={role}>{role}</option>)}</select></label>
              <label className="text-sm font-medium">Status<select disabled={!canManageUsers} value={editStatus} onChange={(event) => setEditStatus(event.target.value)} className="mt-1.5 w-full rounded-md border border-gray-300 bg-white px-3 py-2 capitalize disabled:opacity-60 dark:border-slate-700 dark:bg-slate-950"><option value="active">Active</option><option value="disabled">Disabled</option></select></label>
            </div>
            <div>
              <div className="mb-2 text-sm font-semibold text-gray-900 dark:text-slate-100">Entitlement overrides</div>
              <div className="max-h-72 overflow-y-auto rounded-md border border-gray-200 dark:border-slate-700">
                {permissions.map((permission) => (
                  <label key={permission} className="flex items-center justify-between gap-4 border-b border-gray-100 px-3 py-2 text-sm last:border-0 dark:border-slate-800">
                    <span className="font-mono text-xs">{permission}</span>
                    <select disabled={!canManageEntitlements} value={overrides[permission] ?? 'inherit'} onChange={(event) => setOverrides({ ...overrides, [permission]: event.target.value as 'inherit' | EntitlementEffect })} className="rounded-md border border-gray-300 bg-white px-2 py-1 text-xs disabled:opacity-60 dark:border-slate-700 dark:bg-slate-950"><option value="inherit">Inherit</option><option value="allow">Allow</option><option value="deny">Deny</option></select>
                  </label>
                ))}
              </div>
            </div>
            {error && <p className="text-sm text-red-600 dark:text-red-300">{error}</p>}
            <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-between">
              {canManageUsers ? <button type="button" onClick={() => void resetPassword()} disabled={saving} className="inline-flex items-center justify-center gap-2 rounded-md border border-gray-300 px-3.5 py-2 text-sm dark:border-slate-700"><KeyRound className="h-4 w-4" /> Reset password</button> : <span />}
              <div className="flex justify-end gap-2"><button type="button" onClick={() => setEditing(null)} className="rounded-md border border-gray-300 px-3.5 py-2 text-sm dark:border-slate-700">Cancel</button><button type="submit" disabled={saving} className="rounded-md bg-primary-600 px-3.5 py-2 text-sm font-semibold text-white disabled:opacity-60">Save changes</button></div>
            </div>
          </form>
        </SecurityDialog>
      )}

      {temporaryPassword && (
        <SecurityDialog title="Temporary password" onClose={() => setTemporaryPassword('')}>
          <div className="p-5"><div className="break-all rounded-md bg-gray-100 p-4 font-mono text-sm text-gray-900 dark:bg-slate-950 dark:text-slate-100">{temporaryPassword}</div><button type="button" onClick={() => void navigator.clipboard.writeText(temporaryPassword)} className="mt-4 rounded-md bg-primary-600 px-3.5 py-2 text-sm font-semibold text-white">Copy password</button></div>
        </SecurityDialog>
      )}
    </div>
  )
}
