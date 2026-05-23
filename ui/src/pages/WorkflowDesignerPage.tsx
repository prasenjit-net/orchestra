import { type CSSProperties, type MouseEvent as ReactMouseEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  addEdge,
  Background,
  type Connection,
  Controls,
  type Edge,
  Handle,
  MarkerType,
  MiniMap,
  type Node,
  type NodeChange,
  type NodeProps,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  useReactFlow,
} from '@xyflow/react'
import { AlertCircle, ArrowLeft, CheckCircle2, ChevronDown, ChevronRight, Clock3, FileText, Globe, Grip, Save, Send, SquareTerminal, Trash2, TriangleAlert, X } from 'lucide-react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { scriptsApi, workflowApi } from '../services/api'
import type { Script, WorkflowActivity, WorkflowDefinitionDocument } from '../types'

type InputRow = {
  id: string
  key: string
  value: string
}

type ActivityNodeData = {
  label: string
  activityName: string
  description: string
  inputRows: InputRow[]
  maxAttempts: number
  backoffSeconds: number
}

type BasicNodeData = {
  label: string
}

type ContextReference = {
  label: string
  template: string
  description: string
}

type CanvasContextMenuState = {
  x: number
  y: number
  flowPosition: { x: number; y: number }
  category: string | null
}

type DesignerNodeData = ActivityNodeData | BasicNodeData

type FlowNode = Node<DesignerNodeData>
type ActivityFlowNode = Node<ActivityNodeData, 'activity'>

const startNodeID = 'workflow-start'
const endNodeID = 'workflow-end'

function makeID(prefix: string) {
  return `${prefix}-${Math.random().toString(36).slice(2, 10)}`
}

function formatCategory(category: string) {
  if (!category) {
    return 'General'
  }
  return category.replace(/[-_]/g, ' ').replace(/\b\w/g, (letter) => letter.toUpperCase())
}

function activityDisplayName(activity: Pick<WorkflowActivity, 'name' | 'displayName'>) {
  return activity.displayName?.trim() || formatCategory(activity.name)
}

function stringifyValue(value: unknown) {
  if (value === undefined || value === null) {
    return ''
  }
  if (typeof value === 'string') {
    return value
  }
  if (typeof value === 'number' || typeof value === 'boolean') {
    return String(value)
  }
  return JSON.stringify(value)
}

function makeInputRow(key = '', value = ''): InputRow {
  return { id: makeID('input'), key, value }
}

function rowsFromInput(input: unknown, exampleInput?: unknown): InputRow[] {
  const source = typeof input === 'object' && input && !Array.isArray(input)
    ? (input as Record<string, unknown>)
    : typeof exampleInput === 'object' && exampleInput && !Array.isArray(exampleInput)
      ? (exampleInput as Record<string, unknown>)
      : null

  if (source) {
    const rows = Object.entries(source).map(([key, value]) => makeInputRow(key, stringifyValue(value)))
    if (rows.length > 0) {
      return rows
    }
  }

  if (input !== undefined && input !== null && !source) {
    return [makeInputRow('value', stringifyValue(input))]
  }

  return [makeInputRow()]
}

function parseValue(raw: string): unknown {
  const trimmed = raw.trim()
  if (!trimmed) {
    return ''
  }

  if (trimmed === 'true') {
    return true
  }
  if (trimmed === 'false') {
    return false
  }
  if (trimmed === 'null') {
    return null
  }
  if (/^-?\d+(\.\d+)?$/.test(trimmed)) {
    return Number(trimmed)
  }
  if (trimmed.startsWith('{') || trimmed.startsWith('[') || trimmed.startsWith('"')) {
    try {
      return JSON.parse(trimmed)
    } catch {
      return raw
    }
  }

  return raw
}

function buildInputPayload(rows: InputRow[]) {
  const payload: Record<string, unknown> = {}
  for (const row of rows) {
    const key = row.key.trim()
    if (!key) {
      continue
    }
    payload[key] = parseValue(row.value)
  }
  return payload
}

function findInputRowValue(rows: InputRow[], key: string) {
  return rows.find((row) => row.key === key)?.value ?? ''
}

function upsertInputRow(rows: InputRow[], key: string, value: string, options?: { removeWhenBlank?: boolean }) {
  if (options?.removeWhenBlank && !value.trim()) {
    return rows.filter((row) => row.key !== key)
  }

  const existingRow = rows.find((row) => row.key === key)
  if (existingRow) {
    return rows.map((row) => (row.id === existingRow.id ? { ...row, value } : row))
  }

  return [...rows, makeInputRow(key, value)]
}

function formatStructuredEditorValue(value: unknown) {
  if (value === undefined || value === null || value === '') {
    return ''
  }
  if (typeof value === 'string') {
    return value
  }
  return JSON.stringify(value, null, 2)
}

function formatStringListValue(value: unknown) {
  if (!Array.isArray(value)) {
    return ''
  }
  return value.map((item) => String(item)).join(', ')
}

function collectContextReferences(nodes: Node[], edges: Edge[], currentNodeID: string): ContextReference[] {
  const nodeMap = new Map(nodes.map((node) => [node.id, node]))
  const outgoing = new Map<string, string[]>()
  for (const edge of edges) {
    outgoing.set(edge.source, [...(outgoing.get(edge.source) ?? []), edge.target])
  }

  const orderedPreviousNodes: ActivityFlowNode[] = []
  const visited = new Set<string>()
  let currentID = startNodeID
  while (!visited.has(currentID) && currentID !== currentNodeID) {
    visited.add(currentID)
    const nextTargets = outgoing.get(currentID) ?? []
    if (nextTargets.length === 0) {
      break
    }
    const nextID = nextTargets[0]
    if (nextID === currentNodeID || nextID === endNodeID) {
      break
    }
    const nextNode = nodeMap.get(nextID)
    if (nextNode?.type === 'activity') {
      orderedPreviousNodes.push(nextNode as ActivityFlowNode)
    }
    currentID = nextID
  }

  const references: ContextReference[] = [
    {
      label: 'Latest completed step',
      template: '{{last}}',
      description: 'Uses the full output from the most recently completed step.',
    },
    {
      label: 'Latest completed field',
      template: '{{last.field}}',
      description: 'Replace "field" with a property from the latest step output.',
    },
    {
      label: 'Signal payload',
      template: '{{signals.approval.lastPayload}}',
      description: 'Reads the most recent payload for a workflow signal named approval.',
    },
    {
      label: 'Signal count',
      template: '{{signals.approval.count}}',
      description: 'Reads how many times a workflow signal was received.',
    },
  ]

  for (const previousNode of orderedPreviousNodes) {
    const stepName = previousNode.data.label.trim() || previousNode.data.activityName
    references.push({
      label: `${stepName} output`,
      template: `{{steps.${stepName}}}`,
      description: 'References the full output object for this earlier step.',
    })

    const seenKeys = new Set<string>()
    for (const row of previousNode.data.inputRows) {
      const key = row.key.trim()
      if (!key || seenKeys.has(key)) {
        continue
      }
      seenKeys.add(key)
      references.push({
        label: `${stepName}.${key}`,
        template: `{{steps.${stepName}.${key}}}`,
        description: 'Common field path based on this step configuration. Adjust deeper fields as needed.',
      })
    }
  }

  return references
}

function activityVisual(activityName: string) {
  switch (activityName) {
    case 'http-request':
      return {
        width: 240,
        minHeight: 108,
        containerClass: 'rounded-2xl border-sky-300 bg-sky-50 text-sky-900 dark:border-sky-700 dark:bg-sky-950/40 dark:text-sky-100',
        badgeClass: 'bg-sky-100 text-sky-700 dark:bg-sky-900/50 dark:text-sky-200',
        iconClass: 'bg-sky-100 text-sky-700 dark:bg-sky-900/40 dark:text-sky-200',
        icon: Globe,
        shapeStyle: undefined as CSSProperties | undefined,
      }
    case 'delay':
      return {
        width: 220,
        minHeight: 92,
        containerClass: 'rounded-full border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-100',
        badgeClass: 'bg-amber-100 text-amber-700 dark:bg-amber-900/50 dark:text-amber-200',
        iconClass: 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-200',
        icon: Clock3,
        shapeStyle: undefined as CSSProperties | undefined,
      }
    case 'fail':
      return {
        width: 196,
        minHeight: 120,
        containerClass: 'border-red-300 bg-red-50 text-red-900 dark:border-red-700 dark:bg-red-950/40 dark:text-red-100',
        badgeClass: 'bg-red-100 text-red-700 dark:bg-red-900/50 dark:text-red-200',
        iconClass: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-200',
        icon: TriangleAlert,
        shapeStyle: { clipPath: 'polygon(14% 0%, 86% 0%, 100% 50%, 86% 100%, 14% 100%, 0% 50%)' } as CSSProperties,
      }
    case 'log':
      return {
        width: 208,
        minHeight: 102,
        containerClass: 'rounded-xl border-violet-300 bg-violet-50 text-violet-900 dark:border-violet-700 dark:bg-violet-950/40 dark:text-violet-100',
        badgeClass: 'bg-violet-100 text-violet-700 dark:bg-violet-900/50 dark:text-violet-200',
        iconClass: 'bg-violet-100 text-violet-700 dark:bg-violet-900/40 dark:text-violet-200',
        icon: FileText,
        shapeStyle: undefined as CSSProperties | undefined,
      }
    case 'noop':
      return {
        width: 188,
        minHeight: 88,
        containerClass: 'rounded-lg border-slate-300 bg-white text-slate-900 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100',
        badgeClass: 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200',
        iconClass: 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200',
        icon: SquareTerminal,
        shapeStyle: undefined as CSSProperties | undefined,
      }
    default:
      return {
        width: 204,
        minHeight: 96,
        containerClass: 'rounded-xl border-primary-300 bg-white text-gray-900 dark:border-primary-700 dark:bg-slate-900 dark:text-slate-100',
        badgeClass: 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200',
        iconClass: 'bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300',
        icon: Grip,
        shapeStyle: undefined as CSSProperties | undefined,
      }
  }
}

function ActivityNode({ data, selected }: NodeProps<ActivityFlowNode>) {
  const visual = activityVisual(data.activityName)
  const Icon = visual.icon

  return (
    <div
      style={{ width: visual.width, minHeight: visual.minHeight, ...visual.shapeStyle }}
      className={`border px-3 py-2.5 shadow-sm ${
        visual.containerClass
      } ${
        selected
          ? 'border-primary-500 ring-2 ring-primary-200 dark:ring-primary-900/40'
          : ''
      }`}
    >
      <Handle type="target" position={Position.Left} className="!h-3 !w-3 !border-2 !border-white !bg-primary-500 dark:!border-slate-900" />
      <div className="flex items-start gap-2.5">
        <div className={`rounded-md p-1.5 ${visual.iconClass}`}>
          <Icon className="h-3.5 w-3.5" />
        </div>
        <div className="min-w-0">
          <div className="truncate text-xs font-semibold text-gray-900 dark:text-slate-100">{data.label || 'Untitled step'}</div>
          <div className={`mt-1 inline-flex rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${visual.badgeClass}`}>
            {data.activityName}
          </div>
          <p className="mt-1.5 line-clamp-2 text-[11px] text-gray-500 dark:text-slate-400">{data.description}</p>
        </div>
      </div>
      <Handle type="source" position={Position.Right} className="!h-3 !w-3 !border-2 !border-white !bg-primary-500 dark:!border-slate-900" />
    </div>
  )
}

function ScriptActivityFields({
  node,
  payload,
  setField,
  onUpdate,
}: {
  node: ActivityFlowNode
  payload: unknown
  setField: (key: string, value: string, options?: { removeWhenBlank?: boolean }) => void
  onUpdate: (updater: (data: ActivityNodeData) => ActivityNodeData) => void
}) {
  const savedMode = Boolean(findInputRowValue(node.data.inputRows, 'scriptId'))

  const scriptsQuery = useQuery({
    queryKey: ['scripts'],
    queryFn: scriptsApi.list,
  })

  const scripts: Script[] = scriptsQuery.data?.scripts ?? []
  const selectedScript = scripts.find((s) => s.id === findInputRowValue(node.data.inputRows, 'scriptId'))

  const switchToSaved = () => {
    onUpdate((data) => ({
      ...data,
      inputRows: data.inputRows.filter((r) => r.key !== 'script'),
    }))
  }

  const switchToInline = () => {
    onUpdate((data) => ({
      ...data,
      inputRows: data.inputRows.filter((r) => r.key !== 'scriptId'),
    }))
  }

  const selectScript = (script: Script) => {
    onUpdate((data) => {
      let rows = data.inputRows.filter((r) => r.key !== 'script' && r.key !== 'scriptId')
      rows = upsertInputRow(rows, 'scriptId', script.id)
      if (script.language) rows = upsertInputRow(rows, 'language', script.language)
      if (script.timeoutMs) rows = upsertInputRow(rows, 'timeoutMs', String(script.timeoutMs))
      if (script.exports?.length) rows = upsertInputRow(rows, 'exports', JSON.stringify(script.exports))
      return { ...data, inputRows: rows }
    })
  }

  return (
    <div className="space-y-3">
      {/* Mode toggle */}
      <div className="flex items-center gap-1 rounded-lg border border-gray-200 p-1 dark:border-slate-700">
        <button
          type="button"
          onClick={switchToInline}
          className={`flex-1 rounded-md px-3 py-1.5 text-xs font-semibold transition-colors ${!savedMode ? 'bg-primary-600 text-white' : 'text-gray-600 hover:bg-gray-100 dark:text-slate-300 dark:hover:bg-slate-800'}`}
        >
          Inline
        </button>
        <button
          type="button"
          onClick={switchToSaved}
          className={`flex-1 rounded-md px-3 py-1.5 text-xs font-semibold transition-colors ${savedMode ? 'bg-primary-600 text-white' : 'text-gray-600 hover:bg-gray-100 dark:text-slate-300 dark:hover:bg-slate-800'}`}
        >
          Saved script
        </button>
      </div>

      {savedMode ? (
        <>
          <div>
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Script</label>
            <select
              value={findInputRowValue(node.data.inputRows, 'scriptId')}
              onChange={(event) => {
                const s = scripts.find((sc) => sc.id === event.target.value)
                if (s) selectScript(s)
              }}
              className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
            >
              <option value="">— choose a saved script —</option>
              {scripts.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name} ({s.language})
                </option>
              ))}
            </select>
          </div>
          {selectedScript ? (
            <div>
              <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">
                Source preview (read-only — edit on Scripts page)
              </label>
              <textarea
                readOnly
                rows={10}
                value={selectedScript.source}
                className="w-full rounded-lg border border-gray-200 bg-slate-950 px-3 py-2 font-mono text-sm text-slate-400 outline-none dark:border-slate-700"
              />
            </div>
          ) : null}
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <div>
              <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Timeout ms override</label>
              <input
                type="number"
                min={1}
                value={findInputRowValue(node.data.inputRows, 'timeoutMs')}
                onChange={(event) => setField('timeoutMs', event.target.value, { removeWhenBlank: true })}
                placeholder="from saved script"
                className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
              />
            </div>
            <div>
              <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Exports override</label>
              <input
                value={formatStringListValue((payload as Record<string, unknown>).exports)}
                onChange={(event) =>
                  setField(
                    'exports',
                    JSON.stringify(
                      event.target.value
                        .split(',')
                        .map((item) => item.trim())
                        .filter(Boolean),
                    ),
                    { removeWhenBlank: true },
                  )
                }
                placeholder="from saved script"
                className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
              />
            </div>
          </div>
        </>
      ) : (
        <>
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <div>
              <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Language</label>
              <input
                value={findInputRowValue(node.data.inputRows, 'language') || 'starlark'}
                onChange={(event) => setField('language', event.target.value || 'starlark')}
                placeholder="starlark"
                className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
              />
            </div>
            <div>
              <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Timeout ms</label>
              <input
                type="number"
                min={1}
                value={findInputRowValue(node.data.inputRows, 'timeoutMs')}
                onChange={(event) => setField('timeoutMs', event.target.value, { removeWhenBlank: true })}
                placeholder="100"
                className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
              />
            </div>
          </div>
          <div>
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Exports</label>
            <input
              value={formatStringListValue((payload as Record<string, unknown>).exports)}
              onChange={(event) =>
                setField(
                  'exports',
                  JSON.stringify(
                    event.target.value
                      .split(',')
                      .map((item) => item.trim())
                      .filter(Boolean),
                  ),
                  { removeWhenBlank: true },
                )
              }
              placeholder="result, extra_value"
              className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
            />
          </div>
          <div>
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Script</label>
            <textarea
              rows={10}
              value={String((payload as Record<string, unknown>).script ?? '')}
              onChange={(event) => setField('script', event.target.value)}
              placeholder={'result = {"message": strings.upper(input["name"])}'}
              className="w-full rounded-lg border border-gray-200 bg-slate-950 px-3 py-2 font-mono text-sm text-slate-100 outline-none transition-colors focus:border-primary-500 dark:border-slate-700"
            />
          </div>
          <div className="rounded-lg border border-dashed border-gray-300 p-3 text-[11px] text-gray-500 dark:border-slate-700 dark:text-slate-400">
            Use the builtins <span className="font-mono">json</span>, <span className="font-mono">strings</span>, <span className="font-mono">collections</span>, <span className="font-mono">workflow</span>, and <span className="font-mono">asserts</span>. Workflow context is available in <span className="font-mono">ctx</span>.
          </div>
        </>
      )}
      <div>
        <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Data JSON</label>
        <textarea
          rows={4}
          value={formatStructuredEditorValue((payload as Record<string, unknown>).data)}
          onChange={(event) => setField('data', event.target.value, { removeWhenBlank: true })}
          placeholder='{"name":"orchestra"}'
          className="w-full rounded-lg border border-gray-200 bg-slate-950 px-3 py-2 font-mono text-sm text-slate-100 outline-none transition-colors focus:border-primary-500 dark:border-slate-700"
        />
      </div>
    </div>
  )
}

function ActivityPropertiesModal({
  node,
  contextReferences,
  onClose,
  onDelete,
  onUpdate,
}: {
  node: ActivityFlowNode
  contextReferences: ContextReference[]
  onClose: () => void
  onDelete: () => void
  onUpdate: (updater: (data: ActivityNodeData) => ActivityNodeData) => void
}) {
  const payload = buildInputPayload(node.data.inputRows)
  const [copiedTemplate, setCopiedTemplate] = useState<string | null>(null)
  const [isContextExpanded, setIsContextExpanded] = useState(false)

  const setField = (key: string, value: string, options?: { removeWhenBlank?: boolean }) => {
    onUpdate((data) => ({
      ...data,
      inputRows: upsertInputRow(data.inputRows, key, value, options),
    }))
  }

  const copyTemplate = async (template: string) => {
    try {
      await navigator.clipboard.writeText(template)
      setCopiedTemplate(template)
    } catch {
      setCopiedTemplate(null)
    }
  }

  const renderGenericRows = () => (
    <div>
      <div className="mb-2 flex items-center justify-between gap-2">
        <label className="block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Input fields</label>
        <button
          type="button"
          onClick={() =>
            onUpdate((data) => ({
              ...data,
              inputRows: [...data.inputRows, makeInputRow()],
            }))
          }
          className="rounded-lg border border-gray-200 px-2 py-1 text-[10px] font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
        >
          Add field
        </button>
      </div>
      <div className="space-y-2">
        {node.data.inputRows.map((row) => (
          <div key={row.id} className="grid grid-cols-[0.9fr_1.1fr_auto] gap-2">
            <input
              value={row.key}
              onChange={(event) =>
                onUpdate((data) => ({
                  ...data,
                  inputRows: data.inputRows.map((inputRow) =>
                    inputRow.id === row.id ? { ...inputRow, key: event.target.value } : inputRow,
                  ),
                }))
              }
              placeholder="field"
              className="rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
            />
            <input
              value={row.value}
              onChange={(event) =>
                onUpdate((data) => ({
                  ...data,
                  inputRows: data.inputRows.map((inputRow) =>
                    inputRow.id === row.id ? { ...inputRow, value: event.target.value } : inputRow,
                  ),
                }))
              }
              placeholder="value"
              className="rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
            />
            <button
              type="button"
              onClick={() =>
                onUpdate((data) => ({
                  ...data,
                  inputRows: data.inputRows.length === 1 ? [makeInputRow()] : data.inputRows.filter((inputRow) => inputRow.id !== row.id),
                }))
              }
              className="rounded-lg border border-gray-200 px-2 py-2 text-[10px] font-semibold uppercase tracking-wide text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
            >
              Remove
            </button>
          </div>
        ))}
      </div>
      <p className="mt-2 text-[11px] text-gray-500 dark:text-slate-400">
        Values are auto-typed when possible. Use JSON for nested objects and arrays.
      </p>
    </div>
  )

  const renderActivityFields = () => {
    switch (node.data.activityName) {
      case 'log':
        return (
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <div className="md:col-span-2">
              <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Message</label>
              <input
                value={findInputRowValue(node.data.inputRows, 'message')}
                onChange={(event) => setField('message', event.target.value)}
                placeholder="Workflow started"
                className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
              />
            </div>
            <div>
              <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Level</label>
              <select
                value={findInputRowValue(node.data.inputRows, 'level') || 'info'}
                onChange={(event) => setField('level', event.target.value)}
                className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
              >
                <option value="debug">debug</option>
                <option value="info">info</option>
                <option value="warn">warn</option>
                <option value="error">error</option>
              </select>
            </div>
          </div>
        )
      case 'noop':
        return (
          <div>
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Note</label>
            <input
              value={findInputRowValue(node.data.inputRows, 'note')}
              onChange={(event) => setField('note', event.target.value, { removeWhenBlank: true })}
              placeholder="Optional note"
              className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
            />
          </div>
        )
      case 'fail':
        return (
          <div>
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Failure message</label>
            <input
              value={findInputRowValue(node.data.inputRows, 'message')}
              onChange={(event) => setField('message', event.target.value)}
              placeholder="Something failed"
              className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
            />
          </div>
        )
      case 'delay':
        return (
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <div>
              <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Duration seconds</label>
              <input
                type="number"
                min={0}
                value={findInputRowValue(node.data.inputRows, 'durationSeconds')}
                onChange={(event) => setField('durationSeconds', event.target.value, { removeWhenBlank: true })}
                placeholder="30"
                className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
              />
            </div>
            <div>
              <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Wait until</label>
              <input
                value={findInputRowValue(node.data.inputRows, 'until')}
                onChange={(event) => setField('until', event.target.value, { removeWhenBlank: true })}
                placeholder="2026-05-22T12:00:00Z"
                className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
              />
            </div>
            <p className="md:col-span-2 text-[11px] text-gray-500 dark:text-slate-400">
              Set either a relative duration or an absolute timestamp. Leave the unused field empty.
            </p>
          </div>
        )
      case 'http-request':
        return (
          <div className="space-y-3">
            <div className="grid grid-cols-1 gap-3 md:grid-cols-[140px_minmax(0,1fr)]">
              <div>
                <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Method</label>
                <select
                  value={findInputRowValue(node.data.inputRows, 'method') || 'GET'}
                  onChange={(event) => setField('method', event.target.value)}
                  className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                >
                  {['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map((method) => (
                    <option key={method} value={method}>
                      {method}
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">URL</label>
                <input
                  value={findInputRowValue(node.data.inputRows, 'url')}
                  onChange={(event) => setField('url', event.target.value)}
                  placeholder="https://api.example.com/resource"
                  className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                />
              </div>
            </div>
            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              <div>
                <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Timeout seconds</label>
                <input
                  type="number"
                  min={0}
                  value={findInputRowValue(node.data.inputRows, 'timeoutSeconds')}
                  onChange={(event) => setField('timeoutSeconds', event.target.value, { removeWhenBlank: true })}
                  placeholder="30"
                  className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                />
              </div>
              <div>
                <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Expected status</label>
                <input
                  type="number"
                  min={100}
                  max={599}
                  value={findInputRowValue(node.data.inputRows, 'expectedStatus')}
                  onChange={(event) => setField('expectedStatus', event.target.value, { removeWhenBlank: true })}
                  placeholder="200"
                  className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                />
              </div>
            </div>
            <div>
              <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Headers JSON</label>
              <textarea
                rows={5}
                value={formatStructuredEditorValue((payload as Record<string, unknown>).headers)}
                onChange={(event) => setField('headers', event.target.value, { removeWhenBlank: true })}
                placeholder='{"Authorization":"Bearer ..."}'
                className="w-full rounded-lg border border-gray-200 bg-slate-950 px-3 py-2 font-mono text-sm text-slate-100 outline-none transition-colors focus:border-primary-500 dark:border-slate-700"
              />
            </div>
            <div>
              <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Body</label>
              <textarea
                rows={6}
                value={formatStructuredEditorValue((payload as Record<string, unknown>).body)}
                onChange={(event) => setField('body', event.target.value, { removeWhenBlank: true })}
                placeholder='{"hello":"world"}'
                className="w-full rounded-lg border border-gray-200 bg-slate-950 px-3 py-2 font-mono text-sm text-slate-100 outline-none transition-colors focus:border-primary-500 dark:border-slate-700"
              />
            </div>
          </div>
        )
      case 'script':
        return <ScriptActivityFields node={node} payload={payload} setField={setField} onUpdate={onUpdate} />
      default:
        return renderGenericRows()
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/60 p-4 backdrop-blur-sm">
      <div className="flex max-h-[90vh] w-full max-w-3xl flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-2xl dark:border-slate-800 dark:bg-slate-900">
        <div className="flex items-start justify-between gap-4 border-b border-gray-200 px-5 py-4 dark:border-slate-800">
          <div>
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-lg font-semibold text-gray-900 dark:text-slate-100">Edit activity properties</h2>
              <span className="rounded-full bg-slate-100 px-2.5 py-1 text-[10px] font-semibold uppercase tracking-wide text-slate-700 dark:bg-slate-800 dark:text-slate-200">
                {node.data.activityName}
              </span>
            </div>
            <p className="mt-1 text-sm text-gray-500 dark:text-slate-400">{node.data.description}</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg border border-gray-200 p-2 text-gray-500 transition-colors hover:bg-gray-50 hover:text-gray-900 dark:border-slate-700 dark:text-slate-400 dark:hover:bg-slate-800 dark:hover:text-slate-100"
            aria-label="Close activity editor"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
          <div className="space-y-5">
            <div>
              <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Step name</label>
              <input
                value={node.data.label}
                onChange={(event) => onUpdate((data) => ({ ...data, label: event.target.value }))}
                className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
              />
            </div>

            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              <div>
                <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Max attempts</label>
                <input
                  type="number"
                  min={1}
                  value={node.data.maxAttempts}
                  onChange={(event) => onUpdate((data) => ({ ...data, maxAttempts: Number(event.target.value) || 1 }))}
                  className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                />
              </div>
              <div>
                <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Backoff seconds</label>
                <input
                  type="number"
                  min={0}
                  value={node.data.backoffSeconds}
                  onChange={(event) => onUpdate((data) => ({ ...data, backoffSeconds: Number(event.target.value) || 0 }))}
                  className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                />
              </div>
            </div>

            <div>
            <div>
              <div className="mb-3">
                <h3 className="text-sm font-semibold text-gray-900 dark:text-slate-100">Activity-specific properties</h3>
                <p className="mt-1 text-xs text-gray-500 dark:text-slate-400">This editor is tailored to the selected activity so workflow authors rarely need raw JSON.</p>
              </div>
              {renderActivityFields()}
            </div>
            </div>

            {node.data.activityName !== 'http-request' && node.data.activityName !== 'delay' && node.data.activityName !== 'log' && node.data.activityName !== 'noop' && node.data.activityName !== 'fail' ? null : (
            <div className="rounded-lg border border-dashed border-gray-300 p-3 text-[11px] text-gray-500 dark:border-slate-700 dark:text-slate-400">
              Double-click the node again any time to reopen this editor.
            </div>
            )}

            <div className="rounded-xl border border-primary-200 bg-primary-50/70 dark:border-primary-900/40 dark:bg-primary-950/20">
            <button
              type="button"
              onClick={() => setIsContextExpanded((current) => !current)}
              className="flex w-full items-start justify-between gap-3 px-3 py-3 text-left"
            >
              <div>
                <h3 className="text-sm font-semibold text-gray-900 dark:text-slate-100">Available context values</h3>
                <p className="mt-1 text-xs text-gray-600 dark:text-slate-300">
                  Copy a template to reuse data from earlier steps or workflow signals.
                </p>
              </div>
              <div className="mt-0.5 flex items-center gap-2">
                {copiedTemplate ? (
                  <span className="rounded-full bg-white px-2 py-1 text-[10px] font-semibold uppercase tracking-wide text-primary-700 shadow-sm dark:bg-slate-900 dark:text-primary-200">
                    Copied
                  </span>
                ) : null}
                {isContextExpanded ? <ChevronDown className="h-4 w-4 text-primary-700 dark:text-primary-200" /> : <ChevronRight className="h-4 w-4 text-primary-700 dark:text-primary-200" />}
              </div>
            </button>
            {isContextExpanded ? (
              <div className="border-t border-primary-200 px-3 pb-3 pt-3 dark:border-primary-900/40">
                <div className="space-y-2">
                  {contextReferences.map((reference) => (
                    <div key={`${reference.label}-${reference.template}`} className="rounded-lg border border-primary-100 bg-white/80 p-2.5 dark:border-primary-900/30 dark:bg-slate-900/70">
                      <div className="flex items-start justify-between gap-3">
                        <div className="min-w-0">
                          <div className="text-xs font-semibold text-gray-900 dark:text-slate-100">{reference.label}</div>
                          <div className="mt-1 rounded-md bg-slate-950 px-2 py-1 font-mono text-[11px] text-slate-100">
                            {reference.template}
                          </div>
                          <p className="mt-1 text-[11px] text-gray-500 dark:text-slate-400">{reference.description}</p>
                        </div>
                        <button
                          type="button"
                          onClick={() => void copyTemplate(reference.template)}
                          className="shrink-0 rounded-lg border border-primary-200 px-2.5 py-1.5 text-[10px] font-semibold uppercase tracking-wide text-primary-700 transition-colors hover:bg-primary-100 dark:border-primary-900/40 dark:text-primary-200 dark:hover:bg-primary-900/30"
                        >
                          Copy
                        </button>
                      </div>
                    </div>
                  ))}
                </div>
                <p className="mt-3 text-[11px] text-gray-500 dark:text-slate-400">
                  Step names with spaces are supported. Use additional dot paths after the copied root to reach nested fields.
                </p>
              </div>
            ) : null}
            </div>
          </div>
        </div>

        <div className="flex items-center justify-between gap-3 border-t border-gray-200 px-5 py-4 dark:border-slate-800">
          <button
            type="button"
            onClick={onDelete}
            className="inline-flex items-center gap-2 rounded-lg border border-red-200 px-3 py-2 text-sm font-semibold text-red-700 transition-colors hover:bg-red-50 dark:border-red-900/40 dark:text-red-300 dark:hover:bg-red-950/20"
          >
            <Trash2 className="h-4 w-4" />
            Delete step
          </button>
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700"
          >
            Done
          </button>
        </div>
      </div>
    </div>
  )
}

function makeStartNode(): FlowNode {
  return {
    id: startNodeID,
    type: 'input',
    position: { x: 80, y: 220 },
    sourcePosition: Position.Right,
    draggable: true,
    selectable: true,
    data: { label: 'Start' },
    style: {
      width: 120,
      borderRadius: 14,
      border: '1px solid #34d399',
      background: '#ecfdf5',
      color: '#065f46',
      fontWeight: 700,
    },
  }
}

function makeEndNode(): FlowNode {
  return {
    id: endNodeID,
    type: 'output',
    position: { x: 980, y: 220 },
    targetPosition: Position.Left,
    draggable: true,
    selectable: true,
    data: { label: 'End' },
    style: {
      width: 120,
      borderRadius: 14,
      border: '1px solid #cbd5e1',
      background: '#f8fafc',
      color: '#0f172a',
      fontWeight: 700,
    },
  }
}

function makeBaseEdge(source: string, target: string): Edge {
  return {
    id: `${source}-${target}-${makeID('edge')}`,
    source,
    target,
    type: 'smoothstep',
    markerEnd: { type: MarkerType.ArrowClosed },
  }
}

function createActivityNode(activity: WorkflowActivity, position: { x: number; y: number }, label?: string, input?: unknown): ActivityFlowNode {
  return {
    id: makeID('step'),
    type: 'activity',
    position,
    sourcePosition: Position.Right,
    targetPosition: Position.Left,
    data: {
      label: label ?? `${activityDisplayName(activity)} step`,
      activityName: activity.name,
      description: activity.description,
      inputRows: rowsFromInput(input, activity.exampleInput),
      maxAttempts: 1,
      backoffSeconds: 0,
    },
  }
}

function initialGraph() {
  return {
    nodes: [makeStartNode(), makeEndNode()],
    edges: [makeBaseEdge(startNodeID, endNodeID)],
  }
}

function buildGraphFromDefinition(definition: WorkflowDefinitionDocument, activitiesByName: Map<string, WorkflowActivity>) {
  const nodes: FlowNode[] = [makeStartNode(), makeEndNode()]
  const edges: Edge[] = []

  let previousID = startNodeID
  definition.steps.forEach((step, index) => {
    const activity = activitiesByName.get(step.activity) ?? {
      name: step.activity,
      description: 'Custom workflow activity',
      category: 'custom',
    }
    const node = createActivityNode(activity, step.layout ?? { x: 260 + index * 240, y: 220 }, step.name, step.input)
    node.id = `step-${index + 1}`
    node.data.maxAttempts = step.retry?.maxAttempts ?? 1
    node.data.backoffSeconds = step.retry?.backoffSeconds ?? 0
    nodes.push(node)
    edges.push(makeBaseEdge(previousID, node.id))
    previousID = node.id
  })

  edges.push(makeBaseEdge(previousID, endNodeID))

  return { nodes, edges }
}

function compileDocument(name: string, description: string, nodes: Node[], edges: Edge[]): WorkflowDefinitionDocument {
  if (!name.trim()) {
    throw new Error('Workflow name is required.')
  }

  const activityNodes = nodes.filter((node) => node.type === 'activity') as ActivityFlowNode[]
  if (activityNodes.length === 0) {
    throw new Error('Add at least one activity node before saving.')
  }

  const nodeMap = new Map(nodes.map((node) => [node.id, node]))
  const outgoing = new Map<string, string[]>()
  const incoming = new Map<string, string[]>()

  for (const edge of edges) {
    outgoing.set(edge.source, [...(outgoing.get(edge.source) ?? []), edge.target])
    incoming.set(edge.target, [...(incoming.get(edge.target) ?? []), edge.source])
  }

  const startTargets = outgoing.get(startNodeID) ?? []
  if (startTargets.length !== 1) {
    throw new Error('The start node must connect to exactly one next step.')
  }

  const steps = []
  const visited = new Set<string>()
  let currentID = startTargets[0]

  while (currentID !== endNodeID) {
    if (visited.has(currentID)) {
      throw new Error('Workflow graph contains a cycle or duplicate path.')
    }
    visited.add(currentID)

    const node = nodeMap.get(currentID)
    if (!node || node.type !== 'activity') {
      throw new Error('Only a single linear activity path is supported in this designer right now.')
    }

    const nodeData = (node as ActivityFlowNode).data
    const nextTargets = outgoing.get(currentID) ?? []
    const previousSources = incoming.get(currentID) ?? []

    if (previousSources.length !== 1) {
      throw new Error(`Step "${nodeData.label || nodeData.activityName}" must have exactly one incoming arrow.`)
    }
    if (nextTargets.length !== 1) {
      throw new Error(`Step "${nodeData.label || nodeData.activityName}" must have exactly one outgoing arrow.`)
    }

    steps.push({
      name: nodeData.label.trim() || nodeData.activityName,
      activity: nodeData.activityName,
      input: buildInputPayload(nodeData.inputRows),
      retry: {
        maxAttempts: Math.max(1, Number(nodeData.maxAttempts) || 1),
        backoffSeconds: Math.max(0, Number(nodeData.backoffSeconds) || 0),
      },
      layout: {
        x: Math.round(node.position.x),
        y: Math.round(node.position.y),
      },
    })

    currentID = nextTargets[0]
  }

  if (visited.size !== activityNodes.length) {
    throw new Error('All activity nodes must be connected in a single path from Start to End.')
  }

  return {
    name: name.trim(),
    description: description.trim(),
    steps,
  }
}

function WorkflowDesignerCanvas() {
  const { definitionId } = useParams<{ definitionId: string }>()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const reactFlowWrapper = useRef<HTMLDivElement | null>(null)
  const contextMenuRef = useRef<HTMLDivElement | null>(null)
  const { screenToFlowPosition } = useReactFlow()

  const [workflowName, setWorkflowName] = useState('')
  const [workflowDescription, setWorkflowDescription] = useState('')
  const [notice, setNotice] = useState<string | null>(null)
  const [pageError, setPageError] = useState<string | null>(null)
  const [editingNodeID, setEditingNodeID] = useState<string | null>(null)
  const [isDesktop, setIsDesktop] = useState(() => (typeof window === 'undefined' ? true : window.innerWidth >= 1024))
  const [contextMenu, setContextMenu] = useState<CanvasContextMenuState | null>(null)

  const activitiesQuery = useQuery({
    queryKey: ['workflow-activities'],
    queryFn: workflowApi.listActivities,
  })

  const definitionQuery = useQuery({
    queryKey: ['workflow-definition', definitionId],
    queryFn: () => workflowApi.getDefinition(definitionId as string),
    enabled: Boolean(definitionId),
  })

  const activities = useMemo(() => activitiesQuery.data?.activities ?? [], [activitiesQuery.data?.activities])
  const activitiesByName = useMemo(() => new Map(activities.map((activity) => [activity.name, activity])), [activities])
  const activityCategories = useMemo(() => [...new Set(activities.map((activity) => activity.category))], [activities])
  const nodeTypes = useMemo(() => ({ activity: ActivityNode }), [])

  const initialState = useMemo(() => initialGraph(), [])
  const [nodes, setNodes, onNodesChange] = useNodesState(initialState.nodes)
  const [edges, setEdges, onEdgesChange] = useEdgesState(initialState.edges)

  const handleNodesChange = useCallback(
    (changes: NodeChange<FlowNode>[]) => {
      onNodesChange(changes)
    },
    [onNodesChange],
  )

  useEffect(() => {
    if (typeof window === 'undefined') {
      return
    }

    const media = window.matchMedia('(min-width: 1024px)')
    const updateDesktop = () => setIsDesktop(media.matches)
    updateDesktop()

    media.addEventListener('change', updateDesktop)
    return () => media.removeEventListener('change', updateDesktop)
  }, [])

  useEffect(() => {
    if (!definitionId) {
      const nextState = initialGraph()
      setNodes(nextState.nodes)
      setEdges(nextState.edges)
      setWorkflowName('')
      setWorkflowDescription('')
      return
    }

    if (!definitionQuery.data) {
      return
    }

    const nextState = buildGraphFromDefinition(definitionQuery.data.document, activitiesByName)
    setNodes(nextState.nodes)
    setEdges(nextState.edges)
    setWorkflowName(definitionQuery.data.document.name)
    setWorkflowDescription(definitionQuery.data.document.description)
  }, [definitionId, definitionQuery.data, activitiesByName, setEdges, setNodes])

  const selectedNode = nodes.find((node) => node.selected && node.type === 'activity') as ActivityFlowNode | undefined
  const editingNode = nodes.find((node) => node.id === editingNodeID && node.type === 'activity') as ActivityFlowNode | undefined
  const contextReferences = useMemo(
    () => (editingNode ? collectContextReferences(nodes, edges, editingNode.id) : []),
    [editingNode, nodes, edges],
  )

  useEffect(() => {
    if (editingNodeID && !editingNode) {
      setEditingNodeID(null)
    }
  }, [editingNode, editingNodeID])

  useEffect(() => {
    if (!contextMenu) {
      return
    }

    const handlePointerDown = (event: MouseEvent) => {
      if (contextMenuRef.current?.contains(event.target as Element)) {
        return
      }
      setContextMenu(null)
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setContextMenu(null)
      }
    }

    window.addEventListener('pointerdown', handlePointerDown)
    window.addEventListener('keydown', handleKeyDown)
    return () => {
      window.removeEventListener('pointerdown', handlePointerDown)
      window.removeEventListener('keydown', handleKeyDown)
    }
  }, [contextMenu])

  const updateNodeData = useCallback(
    (nodeID: string, updater: (data: ActivityNodeData) => ActivityNodeData) => {
      setNodes((currentNodes) =>
        currentNodes.map((node) => {
          if (node.id !== nodeID || node.type !== 'activity') {
            return node
          }
          return { ...node,           data: updater((node as ActivityFlowNode).data) }
        }),
      )
    },
    [setNodes],
  )

  const insertNodeIntoTerminalPath = useCallback(
    (newNode: FlowNode) => {
      const terminalEdge = edges.find((edge) => edge.target === endNodeID)
      if (!terminalEdge) {
        setNodes((currentNodes) => [...currentNodes, newNode])
        return
      }

      setNodes((currentNodes) => [...currentNodes, newNode])
      setEdges((currentEdges) => [
        ...currentEdges.filter((edge) => edge.id !== terminalEdge.id),
        makeBaseEdge(terminalEdge.source, newNode.id),
        makeBaseEdge(newNode.id, endNodeID),
      ])
    },
    [edges, setEdges, setNodes],
  )

  const addActivityToCanvas = useCallback(
    (activity: WorkflowActivity, position: { x: number; y: number }) => {
      insertNodeIntoTerminalPath(createActivityNode(activity, position))
      setContextMenu(null)
    },
    [insertNodeIntoTerminalPath],
  )

  const appendActivity = useCallback(
    (activity: WorkflowActivity) => {
      const terminalEdge = edges.find((edge) => edge.target === endNodeID)
      const tailNode = nodes.find((node) => node.id === (terminalEdge?.source ?? startNodeID))
      addActivityToCanvas(activity, {
        x: (tailNode?.position.x ?? 80) + 240,
        y: tailNode?.position.y ?? 220,
      })
    },
    [addActivityToCanvas, edges, nodes],
  )

  const onConnect = useCallback(
    (connection: Connection) => {
      if (!connection.source || !connection.target) {
        return
      }
      if (connection.source === endNodeID || connection.target === startNodeID || connection.source === connection.target) {
        setPageError('The designer currently supports a single forward path from Start to End.')
        return
      }
      setEdges((currentEdges) =>
        addEdge({
          ...connection,
          type: 'smoothstep',
          markerEnd: { type: MarkerType.ArrowClosed },
        }, currentEdges.filter((edge) => (
          edge.source !== connection.source &&
          edge.target !== connection.target &&
          !(edge.source === connection.source && edge.target === connection.target)
        ))),
      )
      setPageError(null)
    },
    [setEdges],
  )

  const onPaneContextMenu = useCallback(
    (event: ReactMouseEvent | MouseEvent) => {
      event.preventDefault()
      if (!reactFlowWrapper.current) {
        return
      }

      const bounds = reactFlowWrapper.current.getBoundingClientRect()
      const menuWidth = 256
      const menuHeight = 320
      const x = Math.min(Math.max(event.clientX - bounds.left, 8), Math.max(bounds.width - menuWidth - 8, 8))
      const y = Math.min(Math.max(event.clientY - bounds.top, 8), Math.max(bounds.height - menuHeight - 8, 8))

      setContextMenu({
        x,
        y,
        flowPosition: screenToFlowPosition({
          x: event.clientX - bounds.left,
          y: event.clientY - bounds.top,
        }),
        category: null,
      })
      setPageError(null)
    },
    [screenToFlowPosition],
  )

  const removeNodeByID = useCallback(
    (nodeID: string) => {
      const targetNode = nodes.find((node) => node.id === nodeID && node.type === 'activity')
      if (!targetNode) {
        return
      }

      const incomingEdge = edges.find((edge) => edge.target === nodeID)
      const outgoingEdge = edges.find((edge) => edge.source === nodeID)

      setNodes((currentNodes) => currentNodes.filter((node) => node.id !== nodeID))
      setEdges((currentEdges) => {
        const withoutSelected = currentEdges.filter((edge) => edge.source !== nodeID && edge.target !== nodeID)
        if (incomingEdge && outgoingEdge) {
          return [...withoutSelected, makeBaseEdge(incomingEdge.source, outgoingEdge.target)]
        }
        return withoutSelected
      })
      setEditingNodeID((current) => (current === nodeID ? null : current))
    },
    [edges, nodes, setEdges, setNodes],
  )

  const createDefinitionMutation = useMutation({
    mutationFn: workflowApi.createDefinition,
    onSuccess: (definition) => {
      setPageError(null)
      setNotice(`Created ${definition.name} v${definition.activeVersion}.`)
      void queryClient.invalidateQueries({ queryKey: ['workflow-definitions'] })
      navigate(`/workflows/${definition.id}/designer`)
    },
    onError: (error: Error) => {
      setNotice(null)
      setPageError(error.message)
    },
  })

  const createDefinitionVersionMutation = useMutation({
    mutationFn: ({ targetDefinitionId, payload }: { targetDefinitionId: string; payload: WorkflowDefinitionDocument }) =>
      workflowApi.createDefinitionVersion(targetDefinitionId, payload),
    onSuccess: (definition) => {
      setPageError(null)
      setNotice(`Saved draft version v${definition.draftVersion ?? definition.latestVersion}.`)
      void Promise.all([
        queryClient.invalidateQueries({ queryKey: ['workflow-definitions'] }),
        queryClient.invalidateQueries({ queryKey: ['workflow-definition', definition.id] }),
      ])
    },
    onError: (error: Error) => {
      setNotice(null)
      setPageError(error.message)
    },
  })

  const publishDefinitionMutation = useMutation({
    mutationFn: ({ targetDefinitionId, version }: { targetDefinitionId: string; version: number }) =>
      workflowApi.publishDefinitionVersion(targetDefinitionId, version),
    onSuccess: (definition) => {
      setPageError(null)
      setNotice(`Published ${definition.name} v${definition.activeVersion}.`)
      void Promise.all([
        queryClient.invalidateQueries({ queryKey: ['workflow-definitions'] }),
        queryClient.invalidateQueries({ queryKey: ['workflow-definition', definition.id] }),
      ])
    },
    onError: (error: Error) => {
      setNotice(null)
      setPageError(error.message)
    },
  })

  const saveDocument = useCallback(() => {
    try {
      const payload = compileDocument(workflowName, workflowDescription, nodes, edges)
      setPageError(null)
      if (definitionId) {
        createDefinitionVersionMutation.mutate({ targetDefinitionId: definitionId, payload })
        return
      }
      createDefinitionMutation.mutate(payload)
    } catch (error) {
      setNotice(null)
      setPageError(error instanceof Error ? error.message : 'Unable to build workflow definition.')
    }
  }, [createDefinitionMutation, createDefinitionVersionMutation, definitionId, edges, nodes, workflowDescription, workflowName])

  if (activitiesQuery.isLoading || (definitionId && definitionQuery.isLoading)) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading designer…</div>
  }

  if (activitiesQuery.error || definitionQuery.error) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Unable to load workflow designer.</div>
  }

  const loadedDefinition = definitionQuery.data
  const hasExistingDraft = Boolean(loadedDefinition?.draftVersion)

  if (!isDesktop) {
    return (
      <div className="flex h-full min-h-0 items-center justify-center bg-gray-50 p-6 dark:bg-slate-950">
        <div className="max-w-lg rounded-2xl border border-gray-200 bg-white p-6 text-center shadow-sm dark:border-slate-800 dark:bg-slate-900">
          <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300">
            <Globe className="h-6 w-6" />
          </div>
          <h1 className="mt-4 text-xl font-semibold text-gray-900 dark:text-slate-100">Workflow designer is desktop only</h1>
          <p className="mt-2 text-sm text-gray-500 dark:text-slate-400">
            Open this page from a desktop or laptop browser to use the full canvas editor, drag activities, and manage node layout.
          </p>
          <div className="mt-5">
            <Link
              to="/workflows"
              className="inline-flex items-center gap-2 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700"
            >
              <ArrowLeft className="h-4 w-4" />
              Back to workflows
            </Link>
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden bg-gray-50 dark:bg-slate-950">
      <div className="shrink-0 border-b border-gray-200 bg-white px-4 py-3 dark:border-slate-800 dark:bg-slate-900">
        <div className="flex flex-col gap-3 xl:flex-row xl:items-center xl:justify-between">
          <div className="space-y-2">
            <Link to="/workflows" className="inline-flex items-center gap-2 text-xs font-medium text-gray-500 transition-colors hover:text-gray-900 dark:text-slate-400 dark:hover:text-slate-100">
              <ArrowLeft className="h-4 w-4" />
              Back to workflows
            </Link>
            <div>
              <h1 className="text-xl font-bold text-gray-900 dark:text-slate-100">{definitionId ? 'Workflow designer' : 'New workflow designer'}</h1>
              <p className="mt-1 text-xs text-gray-500 dark:text-slate-400">
                Right-click the canvas to add activities. Double-click a node to edit its properties.
              </p>
            </div>
          </div>
          <div className="flex flex-wrap gap-2">
            <Link
              to="/operations"
              className="rounded-lg border border-gray-200 px-3 py-2 text-xs font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
            >
              Operations console
            </Link>
            <button
              type="button"
              onClick={saveDocument}
              disabled={createDefinitionMutation.isPending || createDefinitionVersionMutation.isPending || Boolean(definitionId && hasExistingDraft)}
              className="inline-flex items-center gap-2 rounded-lg bg-primary-600 px-3 py-2 text-xs font-semibold text-white transition-colors hover:bg-primary-700 disabled:cursor-not-allowed disabled:opacity-60"
            >
              <Save className="h-4 w-4" />
              {definitionId ? 'Save draft version' : 'Create workflow'}
            </button>
            {definitionId && loadedDefinition?.draftVersion ? (
              <button
                type="button"
                onClick={() =>
                  publishDefinitionMutation.mutate({
                    targetDefinitionId: definitionId,
                    version: loadedDefinition.draftVersion as number,
                  })
                }
                disabled={publishDefinitionMutation.isPending}
                className="inline-flex items-center gap-2 rounded-lg border border-emerald-200 px-3 py-2 text-xs font-semibold text-emerald-700 transition-colors hover:bg-emerald-50 disabled:cursor-not-allowed disabled:opacity-60 dark:border-emerald-900/40 dark:text-emerald-300 dark:hover:bg-emerald-950/20"
              >
                <Send className="h-4 w-4" />
                Publish draft
              </button>
            ) : null}
          </div>
        </div>
        {hasExistingDraft ? (
          <div className="mt-3 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:border-amber-900/40 dark:bg-amber-950/20 dark:text-amber-300">
            This definition already has a draft version. Publish the draft before saving another one from the designer.
          </div>
        ) : null}
        {notice ? (
          <div className="mt-3 rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2 text-xs text-emerald-700 dark:border-emerald-900/40 dark:bg-emerald-950/30 dark:text-emerald-300">
            <div className="flex items-center gap-2">
              <CheckCircle2 className="h-4 w-4" />
              {notice}
            </div>
          </div>
        ) : null}
        {pageError ? (
          <div className="mt-3 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-900/40 dark:bg-red-950/30 dark:text-red-300">
            <div className="flex items-center gap-2">
              <AlertCircle className="h-4 w-4" />
              {pageError}
            </div>
          </div>
        ) : null}
      </div>

      <div className="shrink-0 border-b border-gray-200 bg-white px-4 py-2 dark:border-slate-800 dark:bg-slate-900">
        <div className="flex items-center gap-3">
          <div className="flex min-w-0 flex-1 items-center gap-2">
            <label className="shrink-0 text-[11px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Name</label>
            <input
              value={workflowName}
              onChange={(event) => setWorkflowName(event.target.value)}
              placeholder="Workflow name"
              className="min-w-0 flex-1 rounded-md border border-gray-200 bg-gray-50 px-2.5 py-1.5 text-sm font-medium text-gray-900 outline-none transition-colors focus:border-primary-500 focus:bg-white dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 dark:focus:bg-slate-900"
            />
          </div>
          <div className="flex min-w-0 flex-1 items-center gap-2">
            <label className="shrink-0 text-[11px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Description</label>
            <input
              value={workflowDescription}
              onChange={(event) => setWorkflowDescription(event.target.value)}
              placeholder="Short description"
              className="min-w-0 flex-1 rounded-md border border-gray-200 bg-gray-50 px-2.5 py-1.5 text-sm text-gray-700 outline-none transition-colors focus:border-primary-500 focus:bg-white dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300 dark:focus:bg-slate-900"
            />
          </div>
          {loadedDefinition ? (
            <div className="flex shrink-0 items-center gap-1.5 text-[11px] text-gray-500 dark:text-slate-400">
              <span className="rounded-full bg-gray-100 px-2 py-0.5 font-semibold dark:bg-slate-800">v{loadedDefinition.activeVersion} active</span>
              {loadedDefinition.latestVersion !== loadedDefinition.activeVersion ? (
                <span className="rounded-full bg-gray-100 px-2 py-0.5 dark:bg-slate-800">v{loadedDefinition.latestVersion} latest</span>
              ) : null}
              {loadedDefinition.draftVersion ? (
                <span className="rounded-full bg-amber-100 px-2 py-0.5 font-semibold text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">v{loadedDefinition.draftVersion} draft</span>
              ) : null}
            </div>
          ) : null}
          {selectedNode ? (
            <button
              type="button"
              onClick={() => setEditingNodeID(selectedNode.id)}
              className="shrink-0 rounded-md border border-primary-300 px-2.5 py-1.5 text-[11px] font-semibold text-primary-700 transition-colors hover:bg-primary-50 dark:border-primary-800 dark:text-primary-300 dark:hover:bg-primary-950/30"
            >
              Edit selected step
            </button>
          ) : null}
        </div>
      </div>

      <div className="min-h-0 flex-1">
        <div ref={reactFlowWrapper} className="relative h-full overflow-hidden bg-gray-100 dark:bg-slate-950">
          <ReactFlow
            nodes={nodes}
            edges={edges}
            onNodesChange={handleNodesChange}
            onEdgesChange={onEdgesChange}
            onNodeDoubleClick={(_, node) => {
              if (node.type === 'activity') {
                setEditingNodeID(node.id)
              }
            }}
            onPaneClick={() => setContextMenu(null)}
            onPaneContextMenu={onPaneContextMenu}
            onConnect={onConnect}
            fitView
            nodeTypes={nodeTypes}
            defaultEdgeOptions={{ type: 'smoothstep', markerEnd: { type: MarkerType.ArrowClosed } }}
            className="bg-gray-100 dark:bg-slate-950"
          >
            <Background gap={18} size={1} color="#cbd5e1" />
            <MiniMap pannable zoomable className="!bg-white dark:!bg-slate-900" />
            <Controls showInteractive={false} />
          </ReactFlow>
          {contextMenu ? (
            <div
              ref={contextMenuRef}
              style={{ left: contextMenu.x, top: contextMenu.y }}
              className="absolute z-20 w-64 rounded-xl border border-gray-200 bg-white p-2 shadow-2xl dark:border-slate-700 dark:bg-slate-900"
            >
              {contextMenu.category ? (
                <>
                  <button
                    type="button"
                    onClick={() => setContextMenu((current) => (current ? { ...current, category: null } : null))}
                    className="mb-2 flex w-full items-center justify-between rounded-lg px-2.5 py-2 text-left text-xs font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:text-slate-200 dark:hover:bg-slate-800"
                  >
                    <span>{formatCategory(contextMenu.category)}</span>
                    <ArrowLeft className="h-3.5 w-3.5" />
                  </button>
                  <div className="space-y-1">
                    {activities
                      .filter((activity) => activity.category === contextMenu.category)
                      .map((activity) => (
                        <button
                          key={activity.name}
                          type="button"
                          onClick={() => addActivityToCanvas(activity, contextMenu.flowPosition)}
                          className="block w-full rounded-lg px-2.5 py-2 text-left transition-colors hover:bg-gray-50 dark:hover:bg-slate-800"
                        >
                          <div className="text-xs font-semibold text-gray-900 dark:text-slate-100">{activityDisplayName(activity)}</div>
                          <div className="mt-1 text-[11px] text-gray-500 dark:text-slate-400">{activity.description}</div>
                        </button>
                      ))}
                  </div>
                </>
              ) : (
                <>
                  <div className="px-2.5 py-2">
                    <div className="text-xs font-semibold text-gray-900 dark:text-slate-100">Add activity</div>
                    <div className="mt-1 text-[11px] text-gray-500 dark:text-slate-400">Choose a category first, then pick an activity to insert at this point.</div>
                  </div>
                  <div className="space-y-1">
                    {activityCategories.map((category) => (
                      <button
                        key={category}
                        type="button"
                        onClick={() => setContextMenu((current) => (current ? { ...current, category } : null))}
                        className="flex w-full items-center justify-between rounded-lg px-2.5 py-2 text-left text-xs font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:text-slate-200 dark:hover:bg-slate-800"
                      >
                        <span>{formatCategory(category)}</span>
                        <ChevronRight className="h-3.5 w-3.5" />
                      </button>
                    ))}
                  </div>
                </>
              )}
            </div>
          ) : null}
        </div>
      </div>

      {editingNode ? (
        <ActivityPropertiesModal
          node={editingNode}
          contextReferences={contextReferences}
          onClose={() => setEditingNodeID(null)}
          onDelete={() => removeNodeByID(editingNode.id)}
          onUpdate={(updater) => updateNodeData(editingNode.id, updater)}
        />
      ) : null}
    </div>
  )
}

export default function WorkflowDesignerPage() {
  return (
    <ReactFlowProvider>
      <WorkflowDesignerCanvas />
    </ReactFlowProvider>
  )
}
