/* eslint-disable react-refresh/only-export-components */
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'

export type ThemeMode = 'light' | 'dark' | 'system'

interface ThemeContextValue {
  themeMode: ThemeMode
  setThemeMode: (mode: ThemeMode) => void
}

const themeStorageKey = 'orchestra-theme'
const systemThemeQuery = '(prefers-color-scheme: dark)'
const ThemeContext = createContext<ThemeContextValue | null>(null)

const isThemeMode = (value: string | null): value is ThemeMode =>
  value === 'light' || value === 'dark' || value === 'system'

const getStoredTheme = (): ThemeMode => {
  const stored = window.localStorage.getItem(themeStorageKey)
  return isThemeMode(stored) ? stored : 'system'
}

const applyTheme = (mode: ThemeMode) => {
  const prefersDark = window.matchMedia(systemThemeQuery).matches
  const useDark = mode === 'dark' || (mode === 'system' && prefersDark)
  document.documentElement.classList.toggle('dark', useDark)
  document.documentElement.style.colorScheme = useDark ? 'dark' : 'light'
}

export const initializeTheme = () => {
  applyTheme(getStoredTheme())
}

export function ThemeProvider({ children }: Readonly<{ children: ReactNode }>) {
  const [themeMode, setThemeModeState] = useState<ThemeMode>(getStoredTheme)

  useEffect(() => {
    const systemTheme = window.matchMedia(systemThemeQuery)
    const handleSystemThemeChange = () => {
      if (themeMode === 'system') {
        applyTheme(themeMode)
      }
    }

    applyTheme(themeMode)
    window.localStorage.setItem(themeStorageKey, themeMode)
    systemTheme.addEventListener('change', handleSystemThemeChange)
    return () => systemTheme.removeEventListener('change', handleSystemThemeChange)
  }, [themeMode])

  useEffect(() => {
    const handleStorageChange = (event: StorageEvent) => {
      if (event.key === themeStorageKey && isThemeMode(event.newValue)) {
        setThemeModeState(event.newValue)
      }
    }

    window.addEventListener('storage', handleStorageChange)
    return () => window.removeEventListener('storage', handleStorageChange)
  }, [])

  const setThemeMode = useCallback((mode: ThemeMode) => {
    setThemeModeState(mode)
  }, [])

  const value = useMemo(() => ({ themeMode, setThemeMode }), [themeMode, setThemeMode])
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}

export function useTheme() {
  const value = useContext(ThemeContext)
  if (!value) {
    throw new Error('useTheme must be used within ThemeProvider')
  }
  return value
}
