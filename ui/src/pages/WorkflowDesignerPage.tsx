import { type CSSProperties, type MouseEvent as ReactMouseEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  addEdge,
  Background,
  BaseEdge,
  type Connection,
  Controls,
  type Edge,
  type EdgeProps,
  EdgeLabelRenderer,
  getSmoothStepPath,
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
import { AlertCircle, ArrowLeft, Bot, CheckCircle2, ChevronDown, ChevronRight, ClipboardList, Clock3, Code2, FileText, GitBranch, Globe, Grip, Plus, Radio, Save, Send, Shuffle, SquareTerminal, Trash2, TriangleAlert, UserCheck, Users, Webhook, X } from 'lucide-react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { agentsApi, scriptsApi, workflowApi } from '../services/api'
import type { Agent, Script, WorkflowActivity, WorkflowDefinitionDocument, WorkflowStepTransition, WorkflowTransitionCondition } from '../types'
import ContextExpressionPicker, { type PrecedingStep } from '../components/ContextExpressionPicker'

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

type EdgeConditionData = {
  label?: string
  condition?: WorkflowTransitionCondition
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

function getPrecedingSteps(
  nodeId: string,
  nodes: Node[],
  edges: Edge[],
  activitiesByName: Map<string, WorkflowActivity>,
): PrecedingStep[] {
  const nodeMap = new Map(nodes.map((n) => [n.id, n]))

  // Build ancestor set via reverse BFS from nodeId
  const incoming = new Map<string, string[]>()
  for (const edge of edges) {
    incoming.set(edge.target, [...(incoming.get(edge.target) ?? []), edge.source])
  }
  const ancestors = new Set<string>()
  const queue = [nodeId]
  while (queue.length > 0) {
    const current = queue.shift()!
    for (const src of incoming.get(current) ?? []) {
      if (!ancestors.has(src)) {
        ancestors.add(src)
        queue.push(src)
      }
    }
  }

  // Walk forward from Start, collect activity nodes that are ancestors of nodeId
  const outgoing = new Map<string, string[]>()
  for (const edge of edges) {
    outgoing.set(edge.source, [...(outgoing.get(edge.source) ?? []), edge.target])
  }
  const result: PrecedingStep[] = []
  const visited = new Set<string>()
  const fwdQueue = [startNodeID]
  while (fwdQueue.length > 0) {
    const current = fwdQueue.shift()!
    if (visited.has(current) || current === nodeId) continue
    visited.add(current)
    const node = nodeMap.get(current)
    if (node?.type === 'activity') {
      const data = (node as ActivityFlowNode).data
      const activity = activitiesByName.get(data.activityName)
      result.push({
        name: data.label.trim() || data.activityName,
        activityName: data.activityName,
        exampleOutput: activity?.exampleOutput,
      })
    }
    for (const target of outgoing.get(current) ?? []) {
      if (ancestors.has(target) || target === nodeId) {
        fwdQueue.push(target)
      }
    }
  }
  return result
}

function activityVisual(activityName: string) {
  switch (activityName) {
    // ── Integration ──────────────────────────────────────────────────────
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
    case 'webhook':
      return {
        width: 232,
        minHeight: 104,
        containerClass: 'rounded-2xl border-blue-300 bg-blue-50 text-blue-900 dark:border-blue-700 dark:bg-blue-950/40 dark:text-blue-100',
        badgeClass: 'bg-blue-100 text-blue-700 dark:bg-blue-900/50 dark:text-blue-200',
        iconClass: 'bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-200',
        icon: Webhook,
        shapeStyle: undefined as CSSProperties | undefined,
      }
    // ── AI ───────────────────────────────────────────────────────────────
    case 'agent':
      return {
        width: 232,
        minHeight: 108,
        containerClass: 'rounded-2xl border-purple-300 bg-purple-50 text-purple-900 dark:border-purple-700 dark:bg-purple-950/40 dark:text-purple-100',
        badgeClass: 'bg-purple-100 text-purple-700 dark:bg-purple-900/50 dark:text-purple-200',
        iconClass: 'bg-purple-100 text-purple-700 dark:bg-purple-900/40 dark:text-purple-200',
        icon: Bot,
        shapeStyle: undefined as CSSProperties | undefined,
      }
    // ── Compute ───────────────────────────────────────────────────────────
    case 'script':
      return {
        width: 216,
        minHeight: 100,
        containerClass: 'rounded-lg border-emerald-300 bg-emerald-50 text-emerald-900 dark:border-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-100',
        badgeClass: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/50 dark:text-emerald-200',
        iconClass: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-200',
        icon: Code2,
        shapeStyle: undefined as CSSProperties | undefined,
      }
    case 'transform':
      return {
        width: 208,
        minHeight: 96,
        containerClass: 'rounded-xl border-lime-300 bg-lime-50 text-lime-900 dark:border-lime-700 dark:bg-lime-950/40 dark:text-lime-100',
        badgeClass: 'bg-lime-100 text-lime-700 dark:bg-lime-900/50 dark:text-lime-200',
        iconClass: 'bg-lime-100 text-lime-700 dark:bg-lime-900/40 dark:text-lime-200',
        icon: Shuffle,
        shapeStyle: undefined as CSSProperties | undefined,
      }
    // ── Control flow ──────────────────────────────────────────────────────
    case 'branch':
      return {
        width: 196,
        minHeight: 116,
        containerClass: 'border-orange-300 bg-orange-50 text-orange-900 dark:border-orange-700 dark:bg-orange-950/40 dark:text-orange-100',
        badgeClass: 'bg-orange-100 text-orange-700 dark:bg-orange-900/50 dark:text-orange-200',
        iconClass: 'bg-orange-100 text-orange-700 dark:bg-orange-900/40 dark:text-orange-200',
        icon: GitBranch,
        // Hexagon — universally recognised as a decision/routing node
        shapeStyle: { clipPath: 'polygon(14% 0%, 86% 0%, 100% 50%, 86% 100%, 14% 100%, 0% 50%)' } as CSSProperties,
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
        // Arrow-right chevron — "dead end, hard stop"
        shapeStyle: { clipPath: 'polygon(0% 0%, 82% 0%, 100% 50%, 82% 100%, 0% 100%)' } as CSSProperties,
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
    // ── Signals / human ───────────────────────────────────────────────────
    case 'wait-signal':
      return {
        width: 216,
        minHeight: 96,
        containerClass: 'rounded-xl border-indigo-300 bg-indigo-50 text-indigo-900 dark:border-indigo-700 dark:bg-indigo-950/40 dark:text-indigo-100',
        badgeClass: 'bg-indigo-100 text-indigo-700 dark:bg-indigo-900/50 dark:text-indigo-200',
        iconClass: 'bg-indigo-100 text-indigo-700 dark:bg-indigo-900/40 dark:text-indigo-200',
        icon: Radio,
        shapeStyle: undefined as CSSProperties | undefined,
      }
    case 'approval':
      return {
        width: 216,
        minHeight: 96,
        containerClass: 'rounded-xl border-teal-300 bg-teal-50 text-teal-900 dark:border-teal-700 dark:bg-teal-950/40 dark:text-teal-100',
        badgeClass: 'bg-teal-100 text-teal-700 dark:bg-teal-900/50 dark:text-teal-200',
        iconClass: 'bg-teal-100 text-teal-700 dark:bg-teal-900/40 dark:text-teal-200',
        icon: UserCheck,
        shapeStyle: undefined as CSSProperties | undefined,
      }
    case 'manual-task':
      return {
        width: 216,
        minHeight: 96,
        containerClass: 'rounded-xl border-rose-300 bg-rose-50 text-rose-900 dark:border-rose-700 dark:bg-rose-950/40 dark:text-rose-100',
        badgeClass: 'bg-rose-100 text-rose-700 dark:bg-rose-900/50 dark:text-rose-200',
        iconClass: 'bg-rose-100 text-rose-700 dark:bg-rose-900/40 dark:text-rose-200',
        icon: ClipboardList,
        shapeStyle: undefined as CSSProperties | undefined,
      }
    case 'human-wait':
      return {
        width: 216,
        minHeight: 96,
        containerClass: 'rounded-xl border-cyan-300 bg-cyan-50 text-cyan-900 dark:border-cyan-700 dark:bg-cyan-950/40 dark:text-cyan-100',
        badgeClass: 'bg-cyan-100 text-cyan-700 dark:bg-cyan-900/50 dark:text-cyan-200',
        iconClass: 'bg-cyan-100 text-cyan-700 dark:bg-cyan-900/40 dark:text-cyan-200',
        icon: Users,
        shapeStyle: undefined as CSSProperties | undefined,
      }
    // ── Fallback (custom/unknown activities) ──────────────────────────────
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

function ConditionalEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
  markerEnd,
  selected,
}: EdgeProps<Edge<EdgeConditionData>>) {
  const [edgePath, labelX, labelY] = getSmoothStepPath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition })
  const hasCondition = Boolean((data as EdgeConditionData | undefined)?.condition)
  const label = (data as EdgeConditionData | undefined)?.label

  return (
    <>
      <BaseEdge
        id={id}
        path={edgePath}
        markerEnd={markerEnd}
        style={{
          strokeDasharray: hasCondition ? '6 3' : undefined,
          stroke: selected ? '#8b5cf6' : undefined,
        }}
      />
      {label && (
        <EdgeLabelRenderer>
          <div
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px,${labelY}px)` }}
            className="pointer-events-none absolute rounded bg-white px-1.5 py-0.5 text-[10px] font-semibold text-primary-700 shadow-sm dark:bg-slate-900 dark:text-primary-300"
          >
            {label}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  )
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

function AgentActivityFields({
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
  const agentsQuery = useQuery({
    queryKey: ['agents'],
    queryFn: agentsApi.list,
  })

  const agents: Agent[] = agentsQuery.data?.agents ?? []
  const selectedAgentId = findInputRowValue(node.data.inputRows, 'agentId')
  const selectedAgent = agents.find((a) => a.id === selectedAgentId)

  const selectAgent = (agent: Agent) => {
    onUpdate((data) => {
      const rows = upsertInputRow(data.inputRows, 'agentId', agent.id)
      return { ...data, inputRows: rows }
    })
  }

  return (
    <div className="space-y-3">
      <div>
        <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Agent</label>
        <select
          value={selectedAgentId}
          onChange={(event) => {
            const a = agents.find((ag) => ag.id === event.target.value)
            if (a) selectAgent(a)
          }}
          className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
        >
          <option value="">— choose a saved agent —</option>
          {agents.map((a) => (
            <option key={a.id} value={a.id}>
              {a.name} ({a.model})
            </option>
          ))}
        </select>
      </div>
      {selectedAgent ? (
        <div className="rounded-lg border border-gray-200 bg-gray-50 p-3 text-[11px] dark:border-slate-700 dark:bg-slate-900">
          <p className="font-semibold text-gray-700 dark:text-slate-300">{selectedAgent.model}</p>
          {selectedAgent.systemPrompt ? (
            <p className="mt-1 line-clamp-3 text-gray-500 dark:text-slate-400">{selectedAgent.systemPrompt}</p>
          ) : null}
        </div>
      ) : null}
      <div>
        <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Prompt</label>
        <textarea
          rows={4}
          value={String((payload as Record<string, unknown>).prompt ?? '')}
          onChange={(event) => setField('prompt', event.target.value)}
          placeholder="Summarize the following: {{.input}}"
          className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
        />
        <p className="mt-1 text-[11px] text-gray-400 dark:text-slate-500">Go template — workflow context is available as <span className="font-mono">.</span></p>
      </div>
      <div>
        <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Messages (conversation history JSON)</label>
        <textarea
          rows={4}
          value={formatStructuredEditorValue((payload as Record<string, unknown>).messages)}
          onChange={(event) => setField('messages', event.target.value, { removeWhenBlank: true })}
          placeholder='[{"role":"assistant","content":"Hello!"}]'
          className="w-full rounded-lg border border-gray-200 bg-slate-950 px-3 py-2 font-mono text-sm text-slate-100 outline-none transition-colors focus:border-primary-500 dark:border-slate-700"
        />
        <p className="mt-1 text-[11px] text-gray-400 dark:text-slate-500">Optional prior turns injected before the prompt. Leave blank for single-turn.</p>
      </div>
      <div>
        <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Data JSON</label>
        <textarea
          rows={3}
          value={formatStructuredEditorValue((payload as Record<string, unknown>).data)}
          onChange={(event) => setField('data', event.target.value, { removeWhenBlank: true })}
          placeholder='{"key":"value"}'
          className="w-full rounded-lg border border-gray-200 bg-slate-950 px-3 py-2 font-mono text-sm text-slate-100 outline-none transition-colors focus:border-primary-500 dark:border-slate-700"
        />
      </div>
    </div>
  )
}

type BranchCase = { id: string; label: string; path: string; operator: string; value: string; target: string }

function BranchActivityFields({
  node,
  stepNames,
  precedingSteps,
  onUpdate,
}: {
  node: ActivityFlowNode
  stepNames: string[]
  precedingSteps: PrecedingStep[]
  onUpdate: (updater: (data: ActivityNodeData) => ActivityNodeData) => void
}) {
  const raw = buildInputPayload(node.data.inputRows)
  const rawCases = Array.isArray((raw as Record<string, unknown>).cases) ? ((raw as Record<string, unknown>).cases as Record<string, unknown>[]) : []
  const [cases, setCases] = useState<BranchCase[]>(() =>
    rawCases.map((c) => ({
      id: makeID('case'),
      label: String(c.label ?? ''),
      path: String(c.path ?? ''),
      operator: String(c.operator ?? 'eq'),
      value: String(c.value ?? ''),
      target: String(c.target ?? ''),
    })),
  )

  const syncToNode = (next: BranchCase[]) => {
    setCases(next)
    const serialised = next.map((c) => ({
      label: c.label,
      path: c.path,
      operator: c.operator,
      value: parseValue(c.value),
      target: c.target,
    }))
    onUpdate((data) => ({ ...data, inputRows: [makeInputRow('cases', JSON.stringify(serialised))] }))
  }

  const addCase = () => syncToNode([...cases, { id: makeID('case'), label: '', path: '', operator: 'eq', value: '', target: '' }])
  const removeCase = (id: string) => syncToNode(cases.filter((c) => c.id !== id))
  const updateCase = (id: string, patch: Partial<BranchCase>) => syncToNode(cases.map((c) => (c.id === id ? { ...c, ...patch } : c)))

  return (
    <div className="space-y-3">
      {cases.length === 0 && (
        <p className="text-xs text-gray-500 dark:text-slate-400">No cases yet. Add one to define branching conditions.</p>
      )}
      {cases.map((c) => (
        <div key={c.id} className="rounded-lg border border-gray-200 p-3 dark:border-slate-700 space-y-2">
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Label</label>
              <input
                value={c.label}
                onChange={(e) => updateCase(c.id, { label: e.target.value })}
                placeholder="approved"
                className="w-full rounded border border-gray-200 bg-white px-2 py-1.5 text-sm text-gray-900 outline-none focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
              />
            </div>
            <div>
              <label className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Target step</label>
              <select
                value={c.target}
                onChange={(e) => updateCase(c.id, { target: e.target.value })}
                className="w-full rounded border border-gray-200 bg-white px-2 py-1.5 text-sm text-gray-900 outline-none focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
              >
                <option value="">— pick step —</option>
                {stepNames.map((s) => <option key={s} value={s}>{s}</option>)}
              </select>
            </div>
          </div>
          <div className="grid grid-cols-[minmax(0,1fr)_120px_minmax(0,0.8fr)_auto] gap-2 items-end">
            <div>
              <label className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Path</label>
              <div className="flex gap-1">
                <input
                  value={c.path}
                  onChange={(e) => updateCase(c.id, { path: e.target.value })}
                  placeholder="steps.prev.status"
                  className="min-w-0 flex-1 rounded border border-gray-200 bg-white px-2 py-1.5 text-sm text-gray-900 outline-none focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                />
                <ContextExpressionPicker
                  precedingSteps={precedingSteps}
                  onSelect={(expr) => updateCase(c.id, { path: expr.replace(/^\{\{|\}\}$/g, '') })}
                />
              </div>
            </div>
            <div>
              <label className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Operator</label>
              <select
                value={c.operator}
                onChange={(e) => updateCase(c.id, { operator: e.target.value })}
                className="w-full rounded border border-gray-200 bg-white px-2 py-1.5 text-sm text-gray-900 outline-none focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
              >
                {['eq', 'neq', 'exists', 'not_exists', 'truthy', 'falsy'].map((op) => <option key={op} value={op}>{op}</option>)}
              </select>
            </div>
            <div>
              <label className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-gray-400 dark:text-slate-500">Value</label>
              <input
                value={c.value}
                onChange={(e) => updateCase(c.id, { value: e.target.value })}
                placeholder="200"
                className="w-full rounded border border-gray-200 bg-white px-2 py-1.5 text-sm text-gray-900 outline-none focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
              />
            </div>
            <button
              type="button"
              onClick={() => removeCase(c.id)}
              className="rounded border border-gray-200 px-2 py-1.5 text-[11px] text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:border-slate-700 dark:text-slate-400 dark:hover:bg-red-950/20 dark:hover:text-red-400"
            >
              Remove
            </button>
          </div>
        </div>
      ))}
      <button
        type="button"
        onClick={addCase}
        className="flex items-center gap-1.5 rounded-lg border border-dashed border-gray-300 px-3 py-2 text-xs font-semibold text-gray-600 transition-colors hover:border-primary-400 hover:text-primary-700 dark:border-slate-700 dark:text-slate-300 dark:hover:border-primary-600 dark:hover:text-primary-300"
      >
        <Plus className="h-3.5 w-3.5" />
        Add case
      </button>
    </div>
  )
}

function ActivityPropertiesModal({
  node,
  contextReferences,
  precedingSteps,
  stepNames,
  onClose,
  onDelete,
  onUpdate,
}: {
  node: ActivityFlowNode
  contextReferences: ContextReference[]
  precedingSteps: PrecedingStep[]
  stepNames: string[]
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
          <div key={row.id} className="grid grid-cols-[0.9fr_1fr_auto_auto] gap-2">
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
            <ContextExpressionPicker
              precedingSteps={precedingSteps}
              onSelect={(expr) =>
                onUpdate((data) => ({
                  ...data,
                  inputRows: data.inputRows.map((inputRow) =>
                    inputRow.id === row.id ? { ...inputRow, value: expr } : inputRow,
                  ),
                }))
              }
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
      case 'agent':
        return <AgentActivityFields node={node} payload={payload} setField={setField} onUpdate={onUpdate} />
      case 'branch':
        return <BranchActivityFields node={node} stepNames={stepNames} precedingSteps={precedingSteps} onUpdate={onUpdate} />
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

const CONDITION_OPERATORS = ['eq', 'neq', 'exists', 'not_exists', 'truthy', 'falsy'] as const

function EdgeConditionModal({
  edgeId,
  initialData,
  precedingSteps,
  onSave,
  onClose,
}: {
  edgeId: string
  initialData: EdgeConditionData
  precedingSteps: PrecedingStep[]
  onSave: (edgeId: string, data: EdgeConditionData) => void
  onClose: () => void
}) {
  const [label, setLabel] = useState(initialData.label ?? '')
  const [hasCondition, setHasCondition] = useState(Boolean(initialData.condition))
  const [path, setPath] = useState(initialData.condition?.path ?? '')
  const [operator, setOperator] = useState(initialData.condition?.operator ?? 'eq')
  const [value, setValue] = useState(String(initialData.condition?.value ?? ''))

  const save = () => {
    const data: EdgeConditionData = {
      label: label.trim() || undefined,
      condition: hasCondition && path.trim() ? { path: path.trim(), operator, value: parseValue(value) as WorkflowTransitionCondition['value'] } : undefined,
    }
    onSave(edgeId, data)
    onClose()
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/60 p-4 backdrop-blur-sm">
      <div className="w-full max-w-md overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-2xl dark:border-slate-800 dark:bg-slate-900">
        <div className="flex items-center justify-between border-b border-gray-200 px-5 py-4 dark:border-slate-800">
          <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">Transition settings</h2>
          <button type="button" onClick={onClose} className="rounded-lg border border-gray-200 p-2 text-gray-500 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-400 dark:hover:bg-slate-800">
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="space-y-4 px-5 py-4">
          <div>
            <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Label (optional)</label>
            <input
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="e.g. approved, status 200"
              className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
            />
          </div>
          <div className="flex items-center gap-3">
            <label className="text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Condition</label>
            <div className="flex items-center gap-1 rounded-lg border border-gray-200 p-0.5 dark:border-slate-700">
              <button type="button" onClick={() => setHasCondition(false)}
                className={`rounded-md px-3 py-1 text-xs font-semibold transition-colors ${!hasCondition ? 'bg-primary-600 text-white' : 'text-gray-600 hover:bg-gray-100 dark:text-slate-300 dark:hover:bg-slate-800'}`}>
                Always
              </button>
              <button type="button" onClick={() => setHasCondition(true)}
                className={`rounded-md px-3 py-1 text-xs font-semibold transition-colors ${hasCondition ? 'bg-primary-600 text-white' : 'text-gray-600 hover:bg-gray-100 dark:text-slate-300 dark:hover:bg-slate-800'}`}>
                When
              </button>
            </div>
          </div>
          {hasCondition && (
            <div className="space-y-3 rounded-lg border border-gray-200 p-3 dark:border-slate-700">
              <div>
                <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Path</label>
                <div className="flex gap-1">
                  <input
                    value={path}
                    onChange={(e) => setPath(e.target.value)}
                    placeholder="steps.fetch.statusCode"
                    className="min-w-0 flex-1 rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                  />
                  <ContextExpressionPicker
                    precedingSteps={precedingSteps}
                    onSelect={(expr) => setPath(expr.replace(/^\{\{|\}\}$/g, ''))}
                  />
                </div>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Operator</label>
                  <select
                    value={operator}
                    onChange={(e) => setOperator(e.target.value)}
                    className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                  >
                    {CONDITION_OPERATORS.map((op) => <option key={op} value={op}>{op}</option>)}
                  </select>
                </div>
                <div>
                  <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Value</label>
                  <input
                    value={value}
                    onChange={(e) => setValue(e.target.value)}
                    placeholder="200"
                    className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                  />
                </div>
              </div>
            </div>
          )}
        </div>
        <div className="flex justify-end gap-3 border-t border-gray-200 px-5 py-4 dark:border-slate-800">
          <button type="button" onClick={onClose} className="rounded-lg border border-gray-200 px-4 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800">Cancel</button>
          <button type="button" onClick={save} className="rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700">Save</button>
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

function makeBaseEdge(source: string, target: string, data?: EdgeConditionData): Edge {
  return {
    id: `${source}-${target}-${makeID('edge')}`,
    source,
    target,
    type: 'conditional',
    markerEnd: { type: MarkerType.ArrowClosed },
    data: data ?? {},
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

  // Build nodeId lookup by step name
  const nodeIdByName = new Map<string, string>()
  const stepNodes: { node: ActivityFlowNode; step: WorkflowDefinitionDocument['steps'][number]; index: number }[] = []

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
    nodeIdByName.set(step.name, node.id)
    stepNodes.push({ node, step, index })
  })

  // Connect Start → first step
  if (stepNodes.length > 0) {
    edges.push(makeBaseEdge(startNodeID, stepNodes[0].node.id))
  } else {
    edges.push(makeBaseEdge(startNodeID, endNodeID))
  }

  // For each step, wire edges based on transitions
  stepNodes.forEach(({ node, step, index }) => {
    const transitions: WorkflowStepTransition[] | null | undefined = step.transitions
    if (transitions == null) {
      // Linear: go to next step or End
      const nextId = index + 1 < stepNodes.length ? stepNodes[index + 1].node.id : endNodeID
      edges.push(makeBaseEdge(node.id, nextId))
    } else if (transitions.length === 0) {
      // Explicit terminal
      edges.push(makeBaseEdge(node.id, endNodeID))
    } else {
      // Branching: one edge per transition
      for (const t of transitions) {
        const targetId = nodeIdByName.get(t.to) ?? endNodeID
        edges.push(makeBaseEdge(node.id, targetId, {
          label: t.label,
          condition: t.condition as EdgeConditionData['condition'],
        }))
      }
    }
  })

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
  const outgoingEdges = new Map<string, Edge[]>()
  for (const edge of edges) {
    outgoingEdges.set(edge.source, [...(outgoingEdges.get(edge.source) ?? []), edge])
  }

  const startTargets = outgoingEdges.get(startNodeID) ?? []
  if (startTargets.length !== 1) {
    throw new Error('The start node must connect to exactly one step.')
  }

  // BFS from start to discover step order
  const orderedStepIds: string[] = []
  const visited = new Set<string>()
  const bfsQueue: string[] = [startTargets[0].target]

  while (bfsQueue.length > 0) {
    const currentID = bfsQueue.shift()!
    if (visited.has(currentID) || currentID === endNodeID) continue
    visited.add(currentID)

    const node = nodeMap.get(currentID)
    if (!node || node.type !== 'activity') continue

    orderedStepIds.push(currentID)
    for (const edge of outgoingEdges.get(currentID) ?? []) {
      if (!visited.has(edge.target)) {
        bfsQueue.push(edge.target)
      }
    }
  }

  if (orderedStepIds.length !== activityNodes.length) {
    throw new Error('All activity nodes must be reachable from Start.')
  }

  // Build name→index map for transition validation
  const stepNameByNodeId = new Map<string, string>()
  for (const nodeId of orderedStepIds) {
    const node = nodeMap.get(nodeId) as ActivityFlowNode
    stepNameByNodeId.set(nodeId, node.data.label.trim() || node.data.activityName)
  }

  const steps = orderedStepIds.map((nodeId, index) => {
    const node = nodeMap.get(nodeId) as ActivityFlowNode
    const nodeData = node.data
    const stepName = nodeData.label.trim() || nodeData.activityName

    const outs = outgoingEdges.get(nodeId) ?? []
    const toActivity = outs.filter((e) => e.target !== endNodeID)
    const toEnd = outs.filter((e) => e.target === endNodeID)

    let transitions: WorkflowStepTransition[] | undefined
    const isLastStep = index === orderedStepIds.length - 1

    if (toActivity.length === 0 && toEnd.length > 0 && !isLastStep) {
      // Explicit terminal (non-last step that only connects to End)
      transitions = []
    } else if (toActivity.length === 1 && toEnd.length === 0) {
      const edgeData = toActivity[0].data as EdgeConditionData | undefined
      const nextName = stepNameByNodeId.get(toActivity[0].target)
      const nextIndex = orderedStepIds.indexOf(toActivity[0].target)
      // Simple linear to immediate next step with no condition/label → omit transitions (linear fallback)
      if (!edgeData?.condition && !edgeData?.label && nextIndex === index + 1 && nextName) {
        transitions = undefined
      } else {
        transitions = [{
          to: nextName ?? toActivity[0].target,
          label: edgeData?.label,
          condition: edgeData?.condition,
        }]
      }
    } else if (toActivity.length > 1 || (toActivity.length >= 1 && toEnd.length >= 1)) {
      // Branching: build explicit transitions for each outgoing edge
      transitions = outs.map((e) => {
        const edgeData = e.data as EdgeConditionData | undefined
        const targetName = e.target === endNodeID ? '__end__' : (stepNameByNodeId.get(e.target) ?? e.target)
        return {
          to: targetName,
          label: edgeData?.label,
          condition: edgeData?.condition,
        }
      }).filter((t) => t.to !== '__end__') // transitions to End are handled by empty array or nil
      if (transitions.length === 0) transitions = []
    } else {
      // Last step or only connects to End naturally — nil transitions (linear fallback)
      transitions = undefined
    }

    return {
      name: stepName,
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
      transitions,
    }
  })

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
  const [editingEdgeID, setEditingEdgeID] = useState<string | null>(null)

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
  const edgeTypes = useMemo(() => ({ conditional: ConditionalEdge }), [])

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
  const precedingSteps = useMemo(
    () => (editingNode ? getPrecedingSteps(editingNode.id, nodes, edges, activitiesByName) : []),
    [editingNode, nodes, edges, activitiesByName],
  )
  const allStepNames = useMemo(
    () => (nodes.filter((n) => n.type === 'activity') as ActivityFlowNode[]).map((n) => n.data.label.trim() || n.data.activityName),
    [nodes],
  )
  const editingEdge = useMemo(
    () => (editingEdgeID ? (edges.find((e) => e.id === editingEdgeID) ?? null) : null),
    [editingEdgeID, edges],
  )
  const editingEdgePrecedingSteps = useMemo(() => {
    if (!editingEdge) return []
    return getPrecedingSteps(editingEdge.source, nodes, edges, activitiesByName)
  }, [editingEdge, nodes, edges, activitiesByName])

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

  const onConnect = useCallback(
    (connection: Connection) => {
      if (!connection.source || !connection.target) {
        return
      }
      if (connection.source === endNodeID || connection.target === startNodeID || connection.source === connection.target) {
        setPageError('Cannot draw edges from End, to Start, or to the same node.')
        return
      }
      setEdges((currentEdges) =>
        addEdge({
          ...connection,
          type: 'conditional',
          markerEnd: { type: MarkerType.ArrowClosed },
          data: {},
        },
        // Only prevent exact duplicate edges (same source AND target)
        currentEdges.filter((edge) => !(edge.source === connection.source && edge.target === connection.target)),
        ),
      )
      setPageError(null)
    },
    [setEdges],
  )

  const onEdgeClick = useCallback(
    (_: ReactMouseEvent, edge: Edge) => {
      setEditingEdgeID(edge.id)
    },
    [],
  )

  const saveEdgeCondition = useCallback(
    (edgeId: string, data: EdgeConditionData) => {
      setEdges((currentEdges) =>
        currentEdges.map((e) => (e.id === edgeId ? { ...e, data } : e)),
      )
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
            onEdgeClick={onEdgeClick}
            fitView
            nodeTypes={nodeTypes}
            edgeTypes={edgeTypes}
            defaultEdgeOptions={{ type: 'conditional', markerEnd: { type: MarkerType.ArrowClosed }, data: {} }}
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
          precedingSteps={precedingSteps}
          stepNames={allStepNames}
          onClose={() => setEditingNodeID(null)}
          onDelete={() => removeNodeByID(editingNode.id)}
          onUpdate={(updater) => updateNodeData(editingNode.id, updater)}
        />
      ) : null}
      {editingEdge ? (
        <EdgeConditionModal
          edgeId={editingEdge.id}
          initialData={(editingEdge.data as EdgeConditionData | undefined) ?? {}}
          precedingSteps={editingEdgePrecedingSteps}
          onSave={saveEdgeCondition}
          onClose={() => setEditingEdgeID(null)}
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
