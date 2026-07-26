import { Navigate, Outlet, useLocation } from 'react-router-dom'
import { useAuth } from './AuthProvider'

export default function ProtectedRoute() {
  const { session, isLoading } = useAuth()
  const location = useLocation()

  if (isLoading) {
    return <div className="flex min-h-screen items-center justify-center bg-gray-50 text-sm text-gray-500 dark:bg-slate-950 dark:text-slate-400">Loading...</div>
  }
  if (!session) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />
  }
  if (session.user.mustChangePassword && location.pathname !== '/change-password') {
    return <Navigate to="/change-password" replace />
  }

  return <Outlet />
}

export function RequirePermission({ permission }: { permission: string }) {
  const { hasPermission } = useAuth()
  if (!hasPermission(permission)) {
    return <Navigate to="/dashboard" replace />
  }
  return <Outlet />
}
