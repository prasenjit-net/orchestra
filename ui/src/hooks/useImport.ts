import { useRef, useState } from 'react'
import { importExportApi } from '../services/api'
import type { ImportAnalysis, ImportBundle } from '../types'

export interface ImportState {
  bundle: ImportBundle | null
  analysis: ImportAnalysis | null
  isAnalyzing: boolean
  isApplying: boolean
  error: string | null
}

export function useImport(onSuccess: () => void) {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [state, setState] = useState<ImportState>({
    bundle: null,
    analysis: null,
    isAnalyzing: false,
    isApplying: false,
    error: null,
  })

  const openFilePicker = () => fileInputRef.current?.click()

  const handleFile = async (file: File) => {
    setState((s) => ({ ...s, isAnalyzing: true, error: null }))
    try {
      const text = await file.text()
      const bundle = JSON.parse(text) as ImportBundle
      const analysis = await importExportApi.analyze(bundle)

      // No conflicts → apply immediately without showing modal.
      if (analysis.conflicts.length === 0) {
        setState((s) => ({ ...s, isAnalyzing: false, isApplying: true }))
        await importExportApi.apply(bundle, [])
        setState({ bundle: null, analysis: null, isAnalyzing: false, isApplying: false, error: null })
        onSuccess()
      } else {
        setState((s) => ({ ...s, bundle, analysis, isAnalyzing: false }))
      }
    } catch (err) {
      setState((s) => ({ ...s, isAnalyzing: false, error: (err as Error).message }))
    }
  }

  const onFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (file) void handleFile(file)
    e.target.value = '' // allow re-picking same file
  }

  const confirm = async (overrideIds: string[]) => {
    if (!state.bundle) return
    setState((s) => ({ ...s, isApplying: true, error: null }))
    try {
      await importExportApi.apply(state.bundle, overrideIds)
      setState({ bundle: null, analysis: null, isAnalyzing: false, isApplying: false, error: null })
      onSuccess()
    } catch (err) {
      setState((s) => ({ ...s, isApplying: false, error: (err as Error).message }))
    }
  }

  const close = () =>
    setState({ bundle: null, analysis: null, isAnalyzing: false, isApplying: false, error: null })

  return { fileInputRef, openFilePicker, onFileChange, confirm, close, state }
}
