import { useEffect, useState } from 'react'

function isDarkMode() {
  return document.documentElement.classList.contains('dark')
}

export function useMonacoTheme(): 'vs-dark' | 'vs' {
  const [theme, setTheme] = useState<'vs-dark' | 'vs'>(() => (isDarkMode() ? 'vs-dark' : 'vs'))

  useEffect(() => {
    const observer = new MutationObserver(() => {
      setTheme(isDarkMode() ? 'vs-dark' : 'vs')
    })
    observer.observe(document.documentElement, { attributeFilter: ['class'] })
    return () => observer.disconnect()
  }, [])

  return theme
}
