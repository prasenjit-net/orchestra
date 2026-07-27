/* eslint-disable react-refresh/only-export-components */
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { authApi, setCSRFToken } from '../services/api'
import type { SessionResponse } from '../types'

interface AuthContextValue {
  session: SessionResponse | null
  isLoading: boolean
  hasPermission: (permission: string) => boolean
  login: (username: string, password: string) => Promise<SessionResponse>
  logout: () => Promise<void>
  changePassword: (currentPassword: string, newPassword: string) => Promise<SessionResponse>
  refresh: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: Readonly<{ children: ReactNode }>) {
  const [session, setSession] = useState<SessionResponse | null>(null)
  const [isLoading, setIsLoading] = useState(true)

  const applySession = useCallback((next: SessionResponse | null) => {
    setSession(next)
    setCSRFToken(next?.csrfToken ?? '')
  }, [])

  const refresh = useCallback(async () => {
    try {
      applySession(await authApi.session())
    } catch {
      applySession(null)
    } finally {
      setIsLoading(false)
    }
  }, [applySession])

  useEffect(() => {
    void refresh()
  }, [refresh])

  useEffect(() => {
    const handleUnauthorized = () => applySession(null)
    window.addEventListener('orchestra:unauthorized', handleUnauthorized)
    return () => window.removeEventListener('orchestra:unauthorized', handleUnauthorized)
  }, [applySession])

  const login = useCallback(async (username: string, password: string) => {
    const next = await authApi.login(username, password)
    applySession(next)
    return next
  }, [applySession])

  const logout = useCallback(async () => {
    try {
      await authApi.logout()
    } finally {
      applySession(null)
    }
  }, [applySession])

  const changePassword = useCallback(async (currentPassword: string, newPassword: string) => {
    const next = await authApi.changePassword(currentPassword, newPassword)
    applySession(next)
    return next
  }, [applySession])

  const value = useMemo<AuthContextValue>(() => ({
    session,
    isLoading,
    hasPermission: (permission) => session?.permissions.includes(permission) ?? false,
    login,
    logout,
    changePassword,
    refresh,
  }), [session, isLoading, login, logout, changePassword, refresh])

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  const value = useContext(AuthContext)
  if (!value) {
    throw new Error('useAuth must be used within AuthProvider')
  }
  return value
}

export function PermissionGate({ permission, children }: Readonly<{ permission: string; children: ReactNode }>) {
  const { hasPermission } = useAuth()
  return hasPermission(permission) ? children : null
}
