import type { Edge, Node } from '@xyflow/react'
import type { MappingField, PrecedingStep } from '../components/ContextExpressionPicker'
import type { JSONSchemaDocument, WorkflowActivity, WorkflowTransitionCondition } from '../types'

export type InputRow = {
  id: string
  key: string
  value: string
}

export type ActivityNodeData = {
  label: string
  activityName: string
  description: string
  inputRows: InputRow[]
  maxAttempts: number
  backoffSeconds: number
}

export type BasicNodeData = {
  label: string
}

export type BoundaryNodeKind = 'start' | 'end'

export type ContextReference = {
  label: string
  template: string
  description: string
}

export type CanvasContextMenuState = {
  x: number
  y: number
  flowPosition: { x: number; y: number }
  category: string | null
}

export type EdgeConditionData = {
  label?: string
  condition?: WorkflowTransitionCondition
}

export type DesignerNodeData = ActivityNodeData | BasicNodeData

export type FlowNode = Node<DesignerNodeData>
export type ActivityFlowNode = Node<ActivityNodeData, 'activity'>

export const startNodeID = 'workflow-start'
export const endNodeID = 'workflow-end'
export const terminalTransitionTarget = '__end__'

const stepNamePattern = /^[a-z0-9_-]+$/

export function makeID(prefix: string) {
  return `${prefix}-${crypto.randomUUID()}`
}

export function toStepID(value: string) {
  return value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^-|-$/g, '')
}

export function makeStepName(seed: string) {
  return `${toStepID(seed) || 'step'}-${crypto.randomUUID().slice(0, 4)}`
}

export function validateStepName(name: string) {
  if (!name) {
    throw new Error('Each step needs a name.')
  }
  if (!stepNamePattern.test(name)) {
    throw new Error(`Step "${name}" must use only lowercase letters, numbers, "_" or "-".`)
  }
}

export function formatCategory(category: string) {
  if (!category) {
    return 'General'
  }
  return category.replace(/[-_]/g, ' ').replace(/\b\w/g, (letter) => letter.toUpperCase())
}

export function activityDisplayName(activity: Pick<WorkflowActivity, 'name' | 'displayName'>) {
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

export function makeInputRow(key = '', value = ''): InputRow {
  return { id: makeID('input'), key, value }
}

export function rowsFromInput(input: unknown, exampleInput?: unknown): InputRow[] {
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

export function parseValue(raw: string): unknown {
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

export function buildInputPayload(rows: InputRow[]) {
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

export function outputRowsFromSchema(schema?: JSONSchemaDocument): InputRow[] {
  const properties = schema?.schema.properties
  if (!properties || typeof properties !== 'object' || Array.isArray(properties)) {
    return [makeInputRow('result', '{{last}}')]
  }
  const rows = Object.keys(properties).map((key) => makeInputRow(key, `{{last.${key}}}`))
  return rows.length ? rows : [makeInputRow('result', '{{last}}')]
}

export function schemaPropertyNames(schema?: JSONSchemaDocument) {
  const properties = schema?.schema.properties
  if (!properties || typeof properties !== 'object' || Array.isArray(properties)) {
    return []
  }
  return Object.keys(properties)
}

function schemaTypeLabel(schema: unknown) {
  if (!schema || typeof schema !== 'object' || Array.isArray(schema)) {
    return 'value'
  }
  const rawType = (schema as Record<string, unknown>).type
  if (Array.isArray(rawType)) {
    return rawType.map(String).join('|')
  }
  return typeof rawType === 'string' ? rawType : 'value'
}

export function schemaMappingFields(schema?: JSONSchemaDocument): MappingField[] {
  const collect = (node: unknown, prefix: string): MappingField[] => {
    if (!node || typeof node !== 'object' || Array.isArray(node)) {
      return []
    }
    const objectNode = node as Record<string, unknown>
    const properties = objectNode.properties
    if (!properties || typeof properties !== 'object' || Array.isArray(properties)) {
      return []
    }
    const required = new Set(Array.isArray(objectNode.required) ? objectNode.required.map(String) : [])
    return Object.entries(properties as Record<string, unknown>).flatMap(([key, child]) => {
      const path = `${prefix}.${key}`
      const field: MappingField = {
        path,
        type: schemaTypeLabel(child),
        required: required.has(key),
      }
      return [field, ...collect(child, path)]
    })
  }

  return collect(schema?.schema, 'input')
}

export function findInputRowValue(rows: InputRow[], key: string) {
  return rows.find((row) => row.key === key)?.value ?? ''
}

export function upsertInputRow(rows: InputRow[], key: string, value: string, options?: { removeWhenBlank?: boolean }) {
  if (options?.removeWhenBlank && !value.trim()) {
    return rows.filter((row) => row.key !== key)
  }

  const existingRow = rows.find((row) => row.key === key)
  if (existingRow) {
    return rows.map((row) => (row.id === existingRow.id ? { ...row, value } : row))
  }

  return [...rows, makeInputRow(key, value)]
}

export function formatStructuredEditorValue(value: unknown) {
  if (value === undefined || value === null || value === '') {
    return ''
  }
  if (typeof value === 'string') {
    return value
  }
  return JSON.stringify(value, null, 2)
}

export function formatStringListValue(value: unknown) {
  if (!Array.isArray(value)) {
    return ''
  }
  return value.map((item) => String(item)).join(', ')
}

function orderedPrecedingNodes(nodes: Node[], edges: Edge[], currentNodeID: string): ActivityFlowNode[] {
  const nodeMap = new Map(nodes.map((node) => [node.id, node]))
  const outgoing = new Map<string, string[]>()
  for (const edge of edges) {
    outgoing.set(edge.source, [...(outgoing.get(edge.source) ?? []), edge.target])
  }

  const orderedNodes: ActivityFlowNode[] = []
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
      orderedNodes.push(nextNode as ActivityFlowNode)
    }
    currentID = nextID
  }
  return orderedNodes
}

function describeInputField(field: MappingField) {
  const details = ['Start input']
  if (field.type) details.push(field.type)
  if (field.required) details.push('required')
  return `${details.join(' · ')}.`
}

export function collectContextReferences(nodes: Node[], edges: Edge[], currentNodeID: string, inputFields: MappingField[]): ContextReference[] {
  const orderedPreviousNodes = orderedPrecedingNodes(nodes, edges, currentNodeID)

  const references: ContextReference[] = inputFields.map((field) => ({
    label: field.path,
    template: `{{${field.path}}}`,
    description: describeInputField(field),
  }))

  for (const previousNode of orderedPreviousNodes) {
    const stepName = previousNode.data.label.trim()
    if (!stepName) {
      continue
    }
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

export function getPrecedingSteps(
  nodeId: string,
  nodes: Node[],
  edges: Edge[],
  activitiesByName: Map<string, WorkflowActivity>,
): PrecedingStep[] {
  const nodeMap = new Map(nodes.map((n) => [n.id, n]))

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
      const stepName = data.label.trim()
      if (!stepName) {
        continue
      }
      result.push({
        name: stepName,
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
