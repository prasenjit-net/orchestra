import { useEffect, useRef, useState } from 'react'
import { Braces } from 'lucide-react'

export interface PrecedingStep {
  name: string
  activityName: string
  exampleOutput?: Record<string, unknown>
}

export interface MappingField {
  path: string
  type?: string
  required?: boolean
}

interface Props {
  inputFields?: MappingField[]
  precedingSteps: PrecedingStep[]
  signalNames?: string[]
  onSelect: (expression: string) => void
}

function flatKeys(obj: Record<string, unknown>, prefix = ''): string[] {
  return Object.keys(obj).flatMap((k) => {
    const full = prefix ? `${prefix}.${k}` : k
    const val = obj[k]
    if (val && typeof val === 'object' && !Array.isArray(val)) {
      return [full, ...flatKeys(val as Record<string, unknown>, full)]
    }
    return [full]
  })
}

export default function ContextExpressionPicker({ inputFields = [], precedingSteps, signalNames = [], onSelect }: Readonly<Props>) {
  const [open, setOpen] = useState(false)
  const [dropdownPos, setDropdownPos] = useState<{ top: number; left: number }>({ top: 0, left: 0 })
  const buttonRef = useRef<HTMLButtonElement>(null)
  const dropdownRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const handler = (e: MouseEvent) => {
      if (
        buttonRef.current && !buttonRef.current.contains(e.target as Node) &&
        dropdownRef.current && !dropdownRef.current.contains(e.target as Node)
      ) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  const handleToggle = () => {
    if (!open && buttonRef.current) {
      const rect = buttonRef.current.getBoundingClientRect()
      const dropdownHeight = 320
      const spaceBelow = window.innerHeight - rect.bottom
      const top = spaceBelow >= dropdownHeight ? rect.bottom + 4 : rect.top - dropdownHeight - 4
      setDropdownPos({ top, left: rect.left })
    }
    setOpen((v) => !v)
  }

  const pick = (expr: string) => {
    onSelect(expr)
    setOpen(false)
  }
  const lastStep = precedingSteps[precedingSteps.length - 1]
  const lastStepKeys = lastStep ? flatKeys(lastStep.exampleOutput ?? {}) : []
  const stepOutputSections = precedingSteps
    .map((step) => ({ step, keys: flatKeys(step.exampleOutput ?? {}) }))
    .filter((section) => section.keys.length > 0)

  return (
    <div className="shrink-0">
      <button
        ref={buttonRef}
        type="button"
        title="Insert mapped value"
        onClick={handleToggle}
        className="flex h-full items-center rounded-md border border-gray-200 bg-white px-1.5 text-[11px] font-mono font-semibold text-gray-500 transition-colors hover:border-primary-400 hover:text-primary-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-400 dark:hover:border-primary-500 dark:hover:text-primary-400"
      >
        <Braces className="h-3.5 w-3.5" />
      </button>

      {open && (
        <div
          ref={dropdownRef}
          style={{ top: dropdownPos.top, left: dropdownPos.left }}
          className="fixed z-[9999] w-72 overflow-hidden rounded-xl border border-gray-200 bg-white shadow-xl dark:border-slate-700 dark:bg-slate-900">
          <div className="max-h-80 overflow-y-auto">
            {inputFields.length > 0 && (
              <Section label="Start input">
                {inputFields.map((field) => (
                  <Item
                    key={field.path}
                    label={`{{${field.path}}}`}
                    detail={[field.type, field.required ? 'required' : 'optional'].filter(Boolean).join(' · ')}
                    onPick={pick}
                  />
                ))}
              </Section>
            )}

            {/* Last */}
            {lastStepKeys.length > 0 && (
              <Section label="Last step output">
                {lastStepKeys.map((k) => (
                  <Item key={k} label={`{{last.${k}}}`} onPick={pick} />
                ))}
              </Section>
            )}

            {/* Named step outputs */}
            {stepOutputSections.map(({ step, keys }) => (
              <Section key={step.name} label={`steps.${step.name}`}>
                {keys.map((k) => (
                  <Item key={k} label={`{{steps.${step.name}.${k}}}`} onPick={pick} />
                ))}
              </Section>
            ))}

            {/* Signals */}
            {signalNames.length > 0 && (
              <Section label="Signals">
                {signalNames.flatMap((sig) => [
                  <Item key={`${sig}.payload`} label={`{{signals.${sig}.lastPayload}}`} onPick={pick} />,
                  <Item key={`${sig}.count`} label={`{{signals.${sig}.count}}`} onPick={pick} />,
                ])}
              </Section>
            )}

            {inputFields.length === 0 && stepOutputSections.length === 0 && signalNames.length === 0 && (
              <p className="px-3 py-4 text-xs text-gray-400 dark:text-slate-500">No mapped values available yet.</p>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

function Section({ label, children }: Readonly<{ label: string; children: React.ReactNode }>) {
  return (
    <div>
      <div className="sticky top-0 bg-gray-50 px-3 py-1 text-[10px] font-semibold uppercase tracking-wide text-gray-400 dark:bg-slate-800 dark:text-slate-500">
        {label}
      </div>
      {children}
    </div>
  )
}

function Item({ label, detail, onPick }: Readonly<{ label: string; detail?: string; onPick: (v: string) => void }>) {
  return (
    <button
      type="button"
      onClick={() => onPick(label)}
      className="block w-full px-3 py-1.5 text-left transition-colors hover:bg-primary-50 hover:text-primary-700 dark:hover:bg-primary-900/20 dark:hover:text-primary-300"
    >
      <span className="block font-mono text-xs text-gray-700 dark:text-slate-300">{label}</span>
      {detail ? <span className="mt-0.5 block text-[10px] font-medium text-gray-400 dark:text-slate-500">{detail}</span> : null}
    </button>
  )
}
