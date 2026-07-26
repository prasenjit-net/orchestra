import { useEffect, useState, type FormEvent } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Eye, EyeOff, LogIn } from 'lucide-react'
import { Navigate, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { LogoFull } from '../components/Logo'
import { metaApi } from '../services/api'

export default function LoginPage() {
  const { session, isLoading, login } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const { data: meta } = useQuery({ queryKey: ['public-meta'], queryFn: metaApi.getPublic, staleTime: Infinity })

  useEffect(() => {
    document.title = `Sign in | ${meta?.name ?? 'Orchestra'}`
  }, [meta?.name])

  if (!isLoading && session) {
    return <Navigate to={session.user.mustChangePassword ? '/change-password' : '/dashboard'} replace />
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    setError('')
    setSubmitting(true)
    try {
      const next = await login(username, password)
      const requested = (location.state as { from?: string } | null)?.from
      navigate(next.user.mustChangePassword ? '/change-password' : requested || '/dashboard', { replace: true })
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Sign in failed')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="flex min-h-screen items-center justify-center bg-gray-100 px-4 py-12 dark:bg-slate-950">
      <div className="w-full max-w-sm">
        <div className="mb-8 flex justify-center">
          <LogoFull iconSize={42} title={meta?.name ?? 'orchestra'} />
        </div>
        <form onSubmit={submit} className="rounded-lg border border-gray-200 bg-white p-7 shadow-sm dark:border-slate-800 dark:bg-slate-900">
          <h1 className="text-xl font-semibold text-gray-900 dark:text-slate-100">Sign in</h1>
          <div className="mt-6 space-y-4">
            <label className="block text-sm font-medium text-gray-700 dark:text-slate-300">
              Username
              <input
                autoFocus
                autoComplete="username"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                className="mt-1.5 w-full rounded-md border border-gray-300 bg-white px-3 py-2.5 text-gray-900 outline-none focus:border-primary-500 focus:ring-2 focus:ring-primary-100 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100 dark:focus:ring-primary-900"
                required
              />
            </label>
            <label className="block text-sm font-medium text-gray-700 dark:text-slate-300">
              Password
              <span className="relative mt-1.5 block">
                <input
                  type={showPassword ? 'text' : 'password'}
                  autoComplete="current-password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  className="w-full rounded-md border border-gray-300 bg-white px-3 py-2.5 pr-11 text-gray-900 outline-none focus:border-primary-500 focus:ring-2 focus:ring-primary-100 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100 dark:focus:ring-primary-900"
                  required
                />
                <button
                  type="button"
                  onClick={() => setShowPassword((visible) => !visible)}
                  className="absolute inset-y-0 right-0 flex w-11 items-center justify-center text-gray-500 hover:text-gray-900 dark:text-slate-400 dark:hover:text-slate-100"
                  aria-label={showPassword ? 'Hide password' : 'Show password'}
                  title={showPassword ? 'Hide password' : 'Show password'}
                >
                  {showPassword ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                </button>
              </span>
            </label>
          </div>
          {error && <div role="alert" className="mt-4 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">{error}</div>}
          <button
            type="submit"
            disabled={submitting}
            className="mt-6 inline-flex w-full items-center justify-center gap-2 rounded-md bg-primary-600 px-4 py-2.5 text-sm font-semibold text-white hover:bg-primary-700 disabled:cursor-not-allowed disabled:opacity-60"
          >
            <LogIn className="h-4 w-4" />
            {submitting ? 'Signing in...' : 'Sign in'}
          </button>
        </form>
      </div>
    </main>
  )
}
