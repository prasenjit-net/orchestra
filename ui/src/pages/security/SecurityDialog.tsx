import { useEffect, useId, useRef, type ReactNode } from 'react'
import { X } from 'lucide-react'

interface SecurityDialogProps {
  title: string
  children: ReactNode
  onClose: () => void
}

export default function SecurityDialog({ title, children, onClose }: Readonly<SecurityDialogProps>) {
  const dialogRef = useRef<HTMLDialogElement>(null)
  const titleId = useId()

  useEffect(() => {
    const dialog = dialogRef.current
    dialog?.showModal()
    return () => dialog?.close()
  }, [])

  return (
    <dialog
      ref={dialogRef}
      aria-labelledby={titleId}
      onCancel={(event) => {
        event.preventDefault()
        onClose()
      }}
      className="m-auto w-full max-w-3xl border-0 bg-transparent p-4 text-gray-900 backdrop:bg-slate-950/50 dark:text-slate-100"
    >
      <div className="max-h-[90vh] overflow-y-auto rounded-lg border border-gray-200 bg-white shadow-xl dark:border-slate-700 dark:bg-slate-900">
        <div className="sticky top-0 z-10 flex items-center justify-between border-b border-gray-200 bg-white px-5 py-4 dark:border-slate-700 dark:bg-slate-900">
          <h2 id={titleId} className="text-base font-semibold">{title}</h2>
          <button
            type="button"
            onClick={onClose}
            className="flex h-8 w-8 items-center justify-center rounded-md text-gray-500 hover:bg-gray-100 dark:text-slate-400 dark:hover:bg-slate-800"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        {children}
      </div>
    </dialog>
  )
}
