import { type CSSProperties, type DragEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react'
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
import { AlertCircle, ArrowLeft, CheckCircle2, Clock3, FileText, Globe, Grip, Plus, Save, Send, SquareTerminal, Trash2, TriangleAlert, X } from 'lucide-react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { workflowApi } from '../services/api'
import type { WorkflowActivity, WorkflowDefinitionDocument } from '../types'

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

type DesignerNodeData = ActivityNodeData | BasicNodeData

type FlowNode = Node<DesignerNodeData>
type ActivityFlowNode = Node<ActivityNodeData, 'activity'>
type PaletteTab = 'all' | string
type SidebarTab = 'metadata' | 'palette'

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

function ActivityPropertiesModal({
  node,
  onClose,
  onDelete,
  onUpdate,
}: {
  node: ActivityFlowNode
  onClose: () => void
  onDelete: () => void
  onUpdate: (updater: (data: ActivityNodeData) => ActivityNodeData) => void
}) {
  const payload = buildInputPayload(node.data.inputRows)

  const setField = (key: string, value: string, options?: { removeWhenBlank?: boolean }) => {
    onUpdate((data) => ({
      ...data,
      inputRows: upsertInputRow(data.inputRows, key, value, options),
    }))
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
              <div className="mb-3">
                <h3 className="text-sm font-semibold text-gray-900 dark:text-slate-100">Activity-specific properties</h3>
                <p className="mt-1 text-xs text-gray-500 dark:text-slate-400">This editor is tailored to the selected activity so workflow authors rarely need raw JSON.</p>
              </div>
              {renderActivityFields()}
            </div>

            {node.data.activityName !== 'http-request' && node.data.activityName !== 'delay' && node.data.activityName !== 'log' && node.data.activityName !== 'noop' && node.data.activityName !== 'fail' ? null : (
              <div className="rounded-lg border border-dashed border-gray-300 p-3 text-[11px] text-gray-500 dark:border-slate-700 dark:text-slate-400">
                Double-click the node again any time to reopen this editor.
              </div>
            )}
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
      label: label ?? `${formatCategory(activity.name)} step`,
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
  const { screenToFlowPosition } = useReactFlow()

  const [workflowName, setWorkflowName] = useState('')
  const [workflowDescription, setWorkflowDescription] = useState('')
  const [notice, setNotice] = useState<string | null>(null)
  const [pageError, setPageError] = useState<string | null>(null)
  const [activePaletteTab, setActivePaletteTab] = useState<PaletteTab>('all')
  const [activeSidebarTab, setActiveSidebarTab] = useState<SidebarTab>('metadata')
  const [editingNodeID, setEditingNodeID] = useState<string | null>(null)
  const [isDesktop, setIsDesktop] = useState(() => (typeof window === 'undefined' ? true : window.innerWidth >= 1024))

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
  const paletteTabs = useMemo(() => ['all', ...new Set(activities.map((activity) => activity.category))], [activities])
  const nodeTypes = useMemo(() => ({ activity: ActivityNode }), [])
  const paletteActivities = useMemo(() => {
    if (activePaletteTab === 'all') {
      return activities
    }
    return activities.filter((activity) => activity.category === activePaletteTab)
  }, [activePaletteTab, activities])

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

  useEffect(() => {
    if (editingNodeID && !editingNode) {
      setEditingNodeID(null)
    }
  }, [editingNode, editingNodeID])

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

  const appendActivity = useCallback(
    (activity: WorkflowActivity) => {
      const terminalEdge = edges.find((edge) => edge.target === endNodeID)
      const tailNode = nodes.find((node) => node.id === (terminalEdge?.source ?? startNodeID))
      const position = {
        x: (tailNode?.position.x ?? 80) + 240,
        y: tailNode?.position.y ?? 220,
      }
      insertNodeIntoTerminalPath(createActivityNode(activity, position))
    },
    [edges, insertNodeIntoTerminalPath, nodes],
  )

  const onConnect = useCallback(
    (connection: Connection) => {
      if (!connection.source || !connection.target) {
        return
      }
      setEdges((currentEdges) =>
        addEdge(
          {
            ...connection,
            type: 'smoothstep',
            markerEnd: { type: MarkerType.ArrowClosed },
          },
          currentEdges.filter((edge) => !(edge.source === connection.source && edge.target === connection.target)),
        ),
      )
    },
    [setEdges],
  )

  const onDragStart = useCallback((event: DragEvent<HTMLButtonElement>, activity: WorkflowActivity) => {
    event.dataTransfer.setData('application/reactflow', activity.name)
    event.dataTransfer.effectAllowed = 'move'
  }, [])

  const onDrop = useCallback(
    (event: DragEvent<HTMLDivElement>) => {
      event.preventDefault()
      const activityName = event.dataTransfer.getData('application/reactflow')
      const activity = activitiesByName.get(activityName)
      if (!activity || !reactFlowWrapper.current) {
        return
      }

      const bounds = reactFlowWrapper.current.getBoundingClientRect()
      const position = screenToFlowPosition({
        x: event.clientX - bounds.left,
        y: event.clientY - bounds.top,
      })

      insertNodeIntoTerminalPath(createActivityNode(activity, position))
    },
    [activitiesByName, insertNodeIntoTerminalPath, screenToFlowPosition],
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
                Build a workflow on the canvas, drag activities from the right palette, and save without editing raw JSON.
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

      <div className="grid min-h-0 flex-1 grid-cols-1 gap-0 xl:grid-cols-[minmax(0,1fr)_320px]">
        <div ref={reactFlowWrapper} className="relative min-h-0 overflow-hidden border-r border-gray-200 bg-gray-100 dark:border-slate-800 dark:bg-slate-950">
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
            onConnect={onConnect}
            onDrop={onDrop}
            onDragOver={(event) => {
              event.preventDefault()
              event.dataTransfer.dropEffect = 'move'
            }}
            fitView
            nodeTypes={nodeTypes}
            defaultEdgeOptions={{ type: 'smoothstep', markerEnd: { type: MarkerType.ArrowClosed } }}
            className="bg-gray-100 dark:bg-slate-950"
          >
            <Background gap={18} size={1} color="#cbd5e1" />
            <MiniMap pannable zoomable className="!bg-white dark:!bg-slate-900" />
            <Controls showInteractive={false} />
          </ReactFlow>
        </div>

        <aside className="flex min-h-0 flex-col overflow-hidden bg-white dark:bg-slate-900">
            <div className="shrink-0 border-b border-gray-200 p-2 dark:border-slate-800">
              <div className="grid grid-cols-2 gap-1 rounded-lg bg-gray-100 p-1 dark:bg-slate-800">
                <button
                  type="button"
                  onClick={() => setActiveSidebarTab('metadata')}
                  className={`rounded-md px-2 py-2 text-xs font-semibold transition-colors ${
                   activeSidebarTab === 'metadata'
                      ? 'bg-white text-gray-900 shadow-sm dark:bg-slate-700 dark:text-slate-100'
                      : 'text-gray-500 hover:text-gray-900 dark:text-slate-400 dark:hover:text-slate-100'
                  }`}
                >
                  Metadata
                </button>
                <button
                  type="button"
                  onClick={() => setActiveSidebarTab('palette')}
                  className={`rounded-md px-2 py-2 text-xs font-semibold transition-colors ${
                   activeSidebarTab === 'palette'
                      ? 'bg-white text-gray-900 shadow-sm dark:bg-slate-700 dark:text-slate-100'
                      : 'text-gray-500 hover:text-gray-900 dark:text-slate-400 dark:hover:text-slate-100'
                  }`}
                >
                  Activity palette
                </button>
              </div>
            </div>

            {activeSidebarTab === 'metadata' ? (
              <div className="min-h-0 flex-1 overflow-y-auto p-3">
                <div className="space-y-3">
                  <div>
                    <h2 className="text-sm font-semibold text-gray-900 dark:text-slate-100">Workflow metadata</h2>
                    <p className="mt-1 text-xs text-gray-500 dark:text-slate-400">Name, description, and version details for this definition.</p>
                  </div>
                  <div>
                    <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Workflow name</label>
                    <input
                      value={workflowName}
                      onChange={(event) => setWorkflowName(event.target.value)}
                      placeholder="Workflow name"
                      className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                    />
                  </div>
                  <div>
                    <label className="mb-1.5 block text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Description</label>
                    <textarea
                      value={workflowDescription}
                      onChange={(event) => setWorkflowDescription(event.target.value)}
                      placeholder="Short description"
                      rows={4}
                      className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                    />
                  </div>
                  {loadedDefinition ? (
                    <div className="rounded-lg border border-gray-200 p-3 dark:border-slate-800">
                      <div className="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-slate-400">Current definition</div>
                      <div className="mt-2 space-y-2 text-xs text-gray-600 dark:text-slate-300">
                        <div><span className="font-semibold">ID:</span> {loadedDefinition.id}</div>
                        <div><span className="font-semibold">Active version:</span> v{loadedDefinition.activeVersion}</div>
                        <div><span className="font-semibold">Latest version:</span> v{loadedDefinition.latestVersion}</div>
                        {loadedDefinition.draftVersion ? <div><span className="font-semibold">Draft version:</span> v{loadedDefinition.draftVersion}</div> : null}
                      </div>
                    </div>
                  ) : (
                    <div className="rounded-lg border border-dashed border-gray-300 p-3 text-xs text-gray-500 dark:border-slate-700 dark:text-slate-400">
                      New workflow definitions start at version 1 when created.
                    </div>
                  )}
                  <div className="rounded-lg border border-primary-200 bg-primary-50 p-3 text-xs text-primary-700 dark:border-primary-900/40 dark:bg-primary-950/20 dark:text-primary-200">
                   Double-click any activity node on the canvas to open its custom properties editor.
                   {selectedNode ? (
                     <button
                       type="button"
                       onClick={() => setEditingNodeID(selectedNode.id)}
                       className="mt-3 inline-flex rounded-lg border border-primary-300 px-2.5 py-1.5 text-[11px] font-semibold transition-colors hover:bg-primary-100 dark:border-primary-800 dark:hover:bg-primary-900/30"
                     >
                       Edit selected step
                     </button>
                   ) : null}
                  </div>
                </div>
              </div>
            ) : activeSidebarTab === 'palette' ? (
              <>
                <div className="shrink-0 border-b border-gray-200 p-3 dark:border-slate-800">
                  <div className="flex items-center justify-between gap-2">
                    <div>
                      <h2 className="text-sm font-semibold text-gray-900 dark:text-slate-100">Activity palette</h2>
                      <p className="mt-1 text-xs text-gray-500 dark:text-slate-400">Drag to canvas or add directly to the main path.</p>
                    </div>
                    <button
                      type="button"
                      onClick={() => setActivePaletteTab('all')}
                      className="rounded-lg border border-gray-200 px-2.5 py-1 text-[11px] font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
                    >
                      Reset
                    </button>
                  </div>
                  <div className="mt-3 flex flex-wrap gap-1.5">
                    {paletteTabs.map((tab) => (
                      <button
                        key={tab}
                        type="button"
                        onClick={() => setActivePaletteTab(tab)}
                        className={`rounded-full px-2.5 py-1 text-[10px] font-semibold uppercase tracking-wide transition-colors ${
                          activePaletteTab === tab
                            ? 'bg-primary-600 text-white'
                            : 'border border-gray-200 text-gray-600 hover:bg-gray-50 dark:border-slate-700 dark:text-slate-300 dark:hover:bg-slate-800'
                        }`}
                      >
                        {tab === 'all' ? 'All' : formatCategory(tab)}
                      </button>
                    ))}
                  </div>
                </div>

                <div className="min-h-0 flex-1 overflow-y-auto p-3">
                  <div className="space-y-2">
                    {paletteActivities.map((activity) => (
                      <div key={activity.name} className="rounded-lg border border-gray-200 p-3 dark:border-slate-800">
                        <div className="flex items-start justify-between gap-2">
                          <div>
                            <div className="text-xs font-semibold text-gray-900 dark:text-slate-100">{activity.name}</div>
                            <div className="mt-1 inline-flex rounded-full bg-slate-100 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-slate-700 dark:bg-slate-800 dark:text-slate-200">
                              {formatCategory(activity.category)}
                            </div>
                          </div>
                          <button
                            type="button"
                            draggable
                            onDragStart={(event) => onDragStart(event, activity)}
                            className="rounded-lg border border-gray-200 px-2 py-1 text-[10px] font-semibold text-gray-600 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-300 dark:hover:bg-slate-800"
                          >
                            Drag
                          </button>
                        </div>
                        <p className="mt-2 text-[11px] text-gray-500 dark:text-slate-400">{activity.description}</p>
                        <button
                          type="button"
                          onClick={() => appendActivity(activity)}
                          className="mt-2 inline-flex items-center gap-1.5 rounded-lg bg-primary-600 px-2.5 py-1.5 text-[10px] font-semibold uppercase tracking-wide text-white transition-colors hover:bg-primary-700"
                        >
                          <Plus className="h-3 w-3" />
                          Add to flow
                        </button>
                      </div>
                    ))}
                  </div>
                </div>
              </>
            ) : null}
          </aside>
      </div>

      {editingNode ? (
        <ActivityPropertiesModal
          node={editingNode}
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
