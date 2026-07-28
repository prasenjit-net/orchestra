import { useState, type FormEvent } from 'react'
import { KeyRound, LogOut } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { LogoFull } from '../components/Logo'

export default function ChangePasswordPage() {
  const { session, changePassword, logout } = useAuth()
  const navigate = useNavigate()
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    setError('')
    if (newPassword !== confirmPassword) {
      setError('New passwords do not match')
      return
    }
    setSubmitting(true)
    try {
      await changePassword(currentPassword, newPassword)
      navigate('/dashboard', { replace: true })
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Password change failed')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="flex min-h-screen items-center justify-center bg-gray-100 px-4 py-12 dark:bg-slate-950">
      <div className="w-full max-w-md">
        <div className="mb-8 flex justify-center"><LogoFull iconSize={42} title="orchestra" /></div>
        <form onSubmit={submit} className="rounded-lg border border-gray-200 bg-white p-7 shadow-sm dark:border-slate-800 dark:bg-slate-900">
          <h1 className="text-xl font-semibold text-gray-900 dark:text-slate-100">Change password</h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-slate-400">Signed in as {session?.user.username}</p>
          <div className="mt-6 space-y-4">
            {[
              { label: 'Current password', value: currentPassword, set: setCurrentPassword, autoComplete: 'current-password' },
              { label: 'New password', value: newPassword, set: setNewPassword, autoComplete: 'new-password' },
              { label: 'Confirm new password', value: confirmPassword, set: setConfirmPassword, autoComplete: 'new-password' },
            ].map((field) => (
              <label key={field.label} className="block text-sm font-medium text-gray-700 dark:text-slate-300">
                {field.label}
                <input
                  type="password"
                  autoComplete={field.autoComplete}
                  value={field.value}
                  onChange={(event) => field.set(event.target.value)}
                  minLength={12}
                  maxLength={128}
                  required
                  className="mt-1.5 w-full rounded-md border border-gray-300 bg-white px-3 py-2.5 text-gray-900 outline-none focus:border-primary-500 focus:ring-2 focus:ring-primary-100 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100 dark:focus:ring-primary-900"
                />
              </label>
            ))}
          </div>
          {error && <div role="alert" className="mt-4 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">{error}</div>}
          <button type="submit" disabled={submitting} className="mt-6 inline-flex w-full items-center justify-center gap-2 rounded-md bg-primary-600 px-4 py-2.5 text-sm font-semibold text-white hover:bg-primary-700 disabled:opacity-60">
            <KeyRound className="h-4 w-4" />
            {submitting ? 'Updating...' : 'Update password'}
          </button>
          {!session?.user.mustChangePassword && (
            <button type="button" onClick={() => void logout()} className="mt-3 inline-flex w-full items-center justify-center gap-2 rounded-md border border-gray-300 px-4 py-2.5 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-slate-700 dark:text-slate-300 dark:hover:bg-slate-800">
              <LogOut className="h-4 w-4" /> Sign out
            </button>
          )}
        </form>
      </div>
    </main>
  )
}
