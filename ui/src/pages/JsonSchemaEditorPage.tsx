import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertCircle, ArrowLeft, Braces, ChevronDown, ChevronRight, Copy, Download, Plus, Save, Search, Trash2, Upload, X } from 'lucide-react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import clsx from 'clsx'
import { jsonSchemasApi } from '../services/api'
import type { CreateJSONSchemaInput, JSONSchemaDocument } from '../types'

type SchemaNodeType = 'object' | 'array' | 'string' | 'number' | 'integer' | 'boolean' | 'null'

interface SchemaNode {
  id: string
  name: string
  type: SchemaNodeType
  title: string
  description: string
  required: boolean
  format: string
  enumValues: string
  defaultValue: string
  constValue: string
  refEnabled: boolean
  ref: string
  refSchemaId: string
  refOnly: boolean
  schemaId: string
  comment: string
  examples: string
  extraKeywords: string
  pattern: string
  minimum: string
  maximum: string
  exclusiveMinimum: string
  exclusiveMaximum: string
  multipleOf: string
  minLength: string
  maxLength: string
  minItems: string
  maxItems: string
  minProperties: string
  maxProperties: string
  deprecated: boolean
  readOnly: boolean
  writeOnly: boolean
  uniqueItems: boolean
  additionalProperties: boolean
  children: SchemaNode[]
  item?: SchemaNode
}

interface VisibleNode {
  node: SchemaNode
  depth: number
  path: string[]
  parentType?: SchemaNodeType
}

const schemaTypes: SchemaNodeType[] = ['object', 'array', 'string', 'number', 'integer', 'boolean', 'null']
const numberFormats = new Set([
  'minimum',
  'maximum',
  'exclusiveMinimum',
  'exclusiveMaximum',
  'multipleOf',
  'minLength',
  'maxLength',
  'minItems',
  'maxItems',
  'minProperties',
  'maxProperties',
])
const knownSchemaKeywords = new Set([
  '$schema',
  '$id',
  '$comment',
  '$ref',
  'type',
  'title',
  'description',
  'format',
  'enum',
  'default',
  'const',
  'examples',
  'pattern',
  'minimum',
  'maximum',
  'exclusiveMinimum',
  'exclusiveMaximum',
  'multipleOf',
  'minLength',
  'maxLength',
  'minItems',
  'maxItems',
  'minProperties',
  'maxProperties',
  'uniqueItems',
  'additionalProperties',
  'deprecated',
  'readOnly',
  'writeOnly',
  'properties',
  'required',
  'items',
])

function makeId() {
  return `node-${crypto.randomUUID()}`
}

function defaultNode(name: string, type: SchemaNodeType, required = false): SchemaNode {
  return {
    id: makeId(),
    name,
    type,
    title: '',
    description: '',
    required,
    format: '',
    enumValues: '',
    defaultValue: '',
    constValue: '',
    refEnabled: false,
    ref: '',
    refSchemaId: '',
    refOnly: false,
    schemaId: '',
    comment: '',
    examples: '',
    extraKeywords: '',
    pattern: '',
    minimum: '',
    maximum: '',
    exclusiveMinimum: '',
    exclusiveMaximum: '',
    multipleOf: '',
    minLength: '',
    maxLength: '',
    minItems: '',
    maxItems: '',
    minProperties: '',
    maxProperties: '',
    deprecated: false,
    readOnly: false,
    writeOnly: false,
    uniqueItems: false,
    additionalProperties: true,
    children: [],
    item: type === 'array' ? defaultNode('items', 'string') : undefined,
  }
}

function emptyRoot() {
  const root = defaultNode('root', 'object')
  root.title = 'Root schema'
  return root
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined
  return value as Record<string, unknown>
}

function detectType(schema: Record<string, unknown>): SchemaNodeType {
  const type = schema.type
  if (typeof type === 'string' && schemaTypes.includes(type as SchemaNodeType)) {
    return type as SchemaNodeType
  }
  if (asRecord(schema.properties)) return 'object'
  if (schema.items) return 'array'
  return 'object'
}

function stringifyLoose(value: unknown) {
  if (value === undefined) return ''
  if (typeof value === 'string') return value
  return JSON.stringify(value)
}

function enumToText(value: unknown) {
  if (!Array.isArray(value)) return ''
  return value.map((item) => stringifyLoose(item)).join(', ')
}

function extraKeywordsFromSchema(schema: Record<string, unknown>) {
  const extra = Object.fromEntries(Object.entries(schema).filter(([key]) => !knownSchemaKeywords.has(key)))
  return Object.keys(extra).length ? JSON.stringify(extra, null, 2) : ''
}

function schemaString(schema: Record<string, unknown>, key: string) {
  const value = schema[key]
  return typeof value === 'string' ? value : ''
}

function applyTextMetadata(node: SchemaNode, schema: Record<string, unknown>) {
  node.title = schemaString(schema, 'title')
  node.description = schemaString(schema, 'description')
  node.format = schemaString(schema, 'format')
  node.schemaId = schemaString(schema, '$id')
  node.comment = schemaString(schema, '$comment')
  node.pattern = schemaString(schema, 'pattern')
}

function applyReferenceMetadata(node: SchemaNode, schema: Record<string, unknown>) {
  node.ref = schemaString(schema, '$ref')
  node.refEnabled = Boolean(node.ref)
  node.refOnly = Boolean(node.ref) && Object.keys(schema).every((key) => ['$ref', 'title', 'description', '$comment'].includes(key))
}

function applyNumericMetadata(node: SchemaNode, schema: Record<string, unknown>) {
  for (const key of numberFormats) {
    const value = schema[key]
    if (typeof value === 'number') {
      node[key as keyof Pick<SchemaNode, 'minimum' | 'maximum' | 'exclusiveMinimum' | 'exclusiveMaximum' | 'multipleOf' | 'minLength' | 'maxLength' | 'minItems' | 'maxItems' | 'minProperties' | 'maxProperties'>] = String(value)
    }
  }
}

function applyNodeStructure(node: SchemaNode, schema: Record<string, unknown>) {
  if (node.type === 'object') {
    const properties = asRecord(schema.properties) ?? {}
    const requiredFields = Array.isArray(schema.required) ? schema.required.filter((item): item is string => typeof item === 'string') : []
    node.children = Object.entries(properties)
      .map(([propertyName, propertySchema]) => fromSchema(asRecord(propertySchema) ?? {}, propertyName, requiredFields.includes(propertyName)))
  }
  if (node.type === 'array') {
    node.item = fromSchema(asRecord(schema.items) ?? { type: 'string' }, 'items')
  }
}

function fromSchema(schema: Record<string, unknown>, name = 'root', required = false): SchemaNode {
  const type = detectType(schema)
  const node = defaultNode(name, type, required)
  applyTextMetadata(node, schema)
  applyReferenceMetadata(node, schema)
  applyNumericMetadata(node, schema)
  node.enumValues = enumToText(schema.enum)
  node.defaultValue = stringifyLoose(schema.default)
  node.constValue = stringifyLoose(schema.const)
  node.examples = Array.isArray(schema.examples) ? JSON.stringify(schema.examples, null, 2) : ''
  node.extraKeywords = extraKeywordsFromSchema(schema)
  node.deprecated = schema.deprecated === true
  node.readOnly = schema.readOnly === true
  node.writeOnly = schema.writeOnly === true
  node.uniqueItems = schema.uniqueItems === true
  node.additionalProperties = schema.additionalProperties !== false

  applyNodeStructure(node, schema)
  return node
}

function parseLoose(text: string): unknown {
  const trimmed = text.trim()
  if (!trimmed) return undefined
  try {
    return JSON.parse(trimmed)
  } catch {
    return trimmed
  }
}

function enumFromText(text: string) {
  return text
    .split(',')
    .map((part) => part.trim())
    .filter(Boolean)
    .map(parseLoose)
}

function parseObject(text: string): Record<string, unknown> | undefined {
  const trimmed = text.trim()
  if (!trimmed) return undefined
  try {
    const parsed = JSON.parse(trimmed)
    return asRecord(parsed)
  } catch {
    return undefined
  }
}

function parseArray(text: string): unknown[] | undefined {
  const trimmed = text.trim()
  if (!trimmed) return undefined
  try {
    const parsed = JSON.parse(trimmed)
    return Array.isArray(parsed) ? parsed : undefined
  } catch {
    return undefined
  }
}

function addNumeric(schema: Record<string, unknown>, key: keyof SchemaNode, value: string) {
  if (!value.trim()) return
  const parsed = Number(value)
  if (Number.isFinite(parsed)) {
    schema[key as string] = parsed
  }
}

function schemaReferenceValue(schema: JSONSchemaDocument) {
  const schemaId = schema.schema.$id
  return typeof schemaId === 'string' && schemaId.trim() ? schemaId.trim() : `orchestra://json-schemas/${schema.id}`
}

function findSchemaByRef(schemas: JSONSchemaDocument[], ref: string) {
  const trimmed = ref.trim()
  if (!trimmed) return undefined
  return schemas.find((schema) => schemaReferenceValue(schema) === trimmed || `orchestra://json-schemas/${schema.id}` === trimmed)
}

function addOptionalText(schema: Record<string, unknown>, key: string, value: string) {
  if (value.trim()) schema[key] = value.trim()
}

function addCommonKeywords(schema: Record<string, unknown>, node: SchemaNode, isRoot: boolean) {
  if (isRoot) schema.$schema = 'https://json-schema.org/draft/2020-12/schema'
  schema.type = node.type
  if (node.refEnabled) addOptionalText(schema, '$ref', node.ref)
  if (isRoot) addOptionalText(schema, '$id', node.schemaId)
  addOptionalText(schema, '$comment', node.comment)
  addOptionalText(schema, 'title', node.title)
  addOptionalText(schema, 'description', node.description)
  addOptionalText(schema, 'format', node.format)
  if (node.enumValues.trim()) schema.enum = enumFromText(node.enumValues)
  const examples = parseArray(node.examples)
  if (examples) schema.examples = examples
  const defaultValue = parseLoose(node.defaultValue)
  if (defaultValue !== undefined) schema.default = defaultValue
  const constValue = parseLoose(node.constValue)
  if (constValue !== undefined) schema.const = constValue
  if (node.deprecated) schema.deprecated = true
  if (node.readOnly) schema.readOnly = true
  if (node.writeOnly) schema.writeOnly = true
}

function addObjectKeywords(schema: Record<string, unknown>, node: SchemaNode) {
  const properties: Record<string, unknown> = {}
  const required = node.children.filter((child) => child.required).map((child) => child.name.trim()).filter(Boolean)
  for (const child of node.children) {
    const key = child.name.trim()
    if (key) properties[key] = toSchema(child)
  }
  schema.properties = properties
  if (required.length > 0) schema.required = required
  schema.additionalProperties = node.additionalProperties
  addNumeric(schema, 'minProperties', node.minProperties)
  addNumeric(schema, 'maxProperties', node.maxProperties)
}

function addArrayKeywords(schema: Record<string, unknown>, node: SchemaNode) {
  schema.items = toSchema(node.item ?? defaultNode('items', 'string'))
  addNumeric(schema, 'minItems', node.minItems)
  addNumeric(schema, 'maxItems', node.maxItems)
  if (node.uniqueItems) schema.uniqueItems = true
}

function addStringKeywords(schema: Record<string, unknown>, node: SchemaNode) {
  addOptionalText(schema, 'pattern', node.pattern)
  addNumeric(schema, 'minLength', node.minLength)
  addNumeric(schema, 'maxLength', node.maxLength)
}

function addNumberKeywords(schema: Record<string, unknown>, node: SchemaNode) {
  addNumeric(schema, 'minimum', node.minimum)
  addNumeric(schema, 'maximum', node.maximum)
  addNumeric(schema, 'exclusiveMinimum', node.exclusiveMinimum)
  addNumeric(schema, 'exclusiveMaximum', node.exclusiveMaximum)
  addNumeric(schema, 'multipleOf', node.multipleOf)
}

function toSchema(node: SchemaNode, isRoot = false): Record<string, unknown> {
  const schema: Record<string, unknown> = parseObject(node.extraKeywords) ?? {}
  if (node.refEnabled && node.ref.trim() && node.refOnly) {
    const refSchema: Record<string, unknown> = { $ref: node.ref.trim() }
    if (node.title.trim()) refSchema.title = node.title.trim()
    if (node.description.trim()) refSchema.description = node.description.trim()
    if (node.comment.trim()) refSchema.$comment = node.comment.trim()
    return refSchema
  }
  addCommonKeywords(schema, node, isRoot)
  if (node.type === 'object') addObjectKeywords(schema, node)
  if (node.type === 'array') addArrayKeywords(schema, node)
  if (node.type === 'string') addStringKeywords(schema, node)
  if (node.type === 'number' || node.type === 'integer') addNumberKeywords(schema, node)

  return schema
}

function updateNode(root: SchemaNode, id: string, updater: (node: SchemaNode) => SchemaNode): SchemaNode {
  if (root.id === id) return updater(root)
  const nextChildren = root.children.map((child) => updateNode(child, id, updater))
  const nextItem = root.item ? updateNode(root.item, id, updater) : undefined
  return { ...root, children: nextChildren, item: nextItem }
}

function findNode(root: SchemaNode, id: string): SchemaNode | undefined {
  if (root.id === id) return root
  for (const child of root.children) {
    const found = findNode(child, id)
    if (found) return found
  }
  return root.item ? findNode(root.item, id) : undefined
}

function removeNode(root: SchemaNode, id: string): SchemaNode {
  let item = root.item
  if (item?.id === id) item = undefined
  else if (item) item = removeNode(item, id)
  return {
    ...root,
    children: root.children.filter((child) => child.id !== id).map((child) => removeNode(child, id)),
    item,
  }
}

function cloneNodeWithNewIds(node: SchemaNode): SchemaNode {
  return {
    ...node,
    id: makeId(),
    children: node.children.map(cloneNodeWithNewIds),
    item: node.item ? cloneNodeWithNewIds(node.item) : undefined,
  }
}

function nextCopyName(baseName: string, siblings: SchemaNode[]) {
  const names = new Set(siblings.map((node) => node.name))
  let name = `${baseName}Copy`
  let index = 2
  while (names.has(name)) {
    name = `${baseName}Copy${index}`
    index += 1
  }
  return name
}

function duplicateNode(root: SchemaNode, id: string): { root: SchemaNode; duplicatedId?: string } {
  const childIndex = root.children.findIndex((child) => child.id === id)
  if (childIndex >= 0) {
    const duplicate = cloneNodeWithNewIds(root.children[childIndex])
    duplicate.name = nextCopyName(duplicate.name, root.children)
    const children = [...root.children]
    children.splice(childIndex + 1, 0, duplicate)
    return { root: { ...root, children }, duplicatedId: duplicate.id }
  }

  let duplicatedId: string | undefined
  const children = root.children.map((child) => {
    const result = duplicateNode(child, id)
    if (result.duplicatedId) duplicatedId = result.duplicatedId
    return result.root
  })
  let item = root.item
  if (item) {
    const result = duplicateNode(item, id)
    if (result.duplicatedId) duplicatedId = result.duplicatedId
    item = result.root
  }
  return { root: { ...root, children, item }, duplicatedId }
}

function nodeMatchesSearch(node: SchemaNode, search: string) {
  const term = search.trim().toLowerCase()
  if (!term) return true
  return [node.name, node.title, node.description, node.type].some((value) => value.toLowerCase().includes(term))
}

function subtreeMatchesSearch(node: SchemaNode, search: string): boolean {
  return nodeMatchesSearch(node, search) || node.children.some((child) => subtreeMatchesSearch(child, search)) || (!!node.item && subtreeMatchesSearch(node.item, search))
}

function collectVisible(root: SchemaNode, expanded: Set<string>, depth = 0, path: string[] = [], parentType?: SchemaNodeType, search = ''): VisibleNode[] {
  if (search.trim() && !subtreeMatchesSearch(root, search)) return []
  const rows: VisibleNode[] = [{ node: root, depth, path, parentType }]
  if (!expanded.has(root.id) && !search.trim()) return rows
  for (const child of root.children) {
    rows.push(...collectVisible(child, expanded, depth + 1, [...path, child.name], root.type, search))
  }
  if (root.item) {
    rows.push(...collectVisible(root.item, expanded, depth + 1, [...path, 'items'], root.type, search))
  }
  return rows
}

function countNodes(root: SchemaNode): number {
  return 1 + root.children.reduce((total, child) => total + countNodes(child), 0) + (root.item ? countNodes(root.item) : 0)
}

function nextFieldName(node: SchemaNode) {
  const names = new Set(node.children.map((child) => child.name))
  let index = node.children.length + 1
  while (names.has(`field${index}`)) index += 1
  return `field${index}`
}

function typeBadgeColor(type: SchemaNodeType) {
  switch (type) {
    case 'object': return 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300'
    case 'array': return 'bg-teal-100 text-teal-700 dark:bg-teal-900/30 dark:text-teal-300'
    case 'string': return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
    case 'number':
    case 'integer': return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
    case 'boolean': return 'bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-300'
    default: return 'bg-gray-100 text-gray-700 dark:bg-slate-800 dark:text-slate-300'
  }
}

function referenceSelectLabel(isLoading: boolean, schemaCount: number) {
  if (isLoading) return 'Loading schemas...'
  if (schemaCount > 0) return 'Manual reference'
  return 'No other schemas available'
}

function schemaIssueSummary(issueCount: number) {
  if (issueCount === 0) return 'Schema checks passed'
  const suffix = issueCount === 1 ? '' : 's'
  return `${issueCount} schema issue${suffix}`
}

function getReferenceWarnings(node: SchemaNode, schemas: JSONSchemaDocument[], currentSchemaId?: string) {
  if (!node.refEnabled) return []
  const warnings: string[] = []
  if (!node.ref.trim()) {
    warnings.push('Choose an existing schema or enter a manual reference.')
  }
  if (currentSchemaId && node.ref.includes(currentSchemaId)) {
    warnings.push('This node appears to reference the schema currently being edited.')
  }
  const matched = findSchemaByRef(schemas, node.ref)
  if (matched && !(typeof matched.schema.$id === 'string' && matched.schema.$id.trim())) {
    warnings.push('Selected schema has no $id, so Orchestra is using a local URI fallback.')
  }
  return warnings
}

function ExpandIcon({ hasChildren, isExpanded }: Readonly<{ hasChildren: boolean; isExpanded: boolean }>) {
  if (!hasChildren) return null
  return isExpanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />
}

function validatePattern(node: SchemaNode, path: string, issues: string[]) {
  if (node.pattern.trim()) {
    try {
      new RegExp(node.pattern)
    } catch {
      issues.push(`${path}: pattern is not a valid regular expression`)
    }
  }
}

function validateRanges(node: SchemaNode, path: string, issues: string[]) {
  const pairs: Array<[string, string, string]> = [
    ['minimum', node.minimum, node.maximum],
    ['exclusiveMinimum', node.exclusiveMinimum, node.exclusiveMaximum],
    ['minLength', node.minLength, node.maxLength],
    ['minItems', node.minItems, node.maxItems],
    ['minProperties', node.minProperties, node.maxProperties],
  ]
  for (const [label, min, max] of pairs) {
    if (min.trim() && max.trim() && Number(min) > Number(max)) {
      issues.push(`${path}: ${label} exceeds the matching maximum`)
    }
  }
}

function validateNumericKeywords(node: SchemaNode, path: string, issues: string[]) {
  for (const [key, value] of Object.entries({
    minimum: node.minimum,
    maximum: node.maximum,
    exclusiveMinimum: node.exclusiveMinimum,
    exclusiveMaximum: node.exclusiveMaximum,
    multipleOf: node.multipleOf,
    minLength: node.minLength,
    maxLength: node.maxLength,
    minItems: node.minItems,
    maxItems: node.maxItems,
    minProperties: node.minProperties,
    maxProperties: node.maxProperties,
  })) {
    if (value.trim() && !Number.isFinite(Number(value))) {
      issues.push(`${path}: ${key} must be numeric`)
    }
  }
  if (node.multipleOf.trim() && Number(node.multipleOf) <= 0) {
    issues.push(`${path}: multipleOf must be greater than zero`)
  }
}

function validateStructuredKeywords(node: SchemaNode, path: string, issues: string[]) {
  if (node.refEnabled && !node.ref.trim()) {
    issues.push(`${path}: $ref is enabled but empty`)
  }
  if (node.examples.trim() && !parseArray(node.examples)) {
    issues.push(`${path}: examples must be a JSON array`)
  }
  if (node.extraKeywords.trim() && !parseObject(node.extraKeywords)) {
    issues.push(`${path}: extra keywords must be a JSON object`)
  }
  if (node.enumValues.trim() && enumFromText(node.enumValues).length === 0) {
    issues.push(`${path}: enum needs at least one value`)
  }
}

function validateNode(node: SchemaNode, path = 'root', issues: string[] = []) {
  validatePattern(node, path, issues)
  validateRanges(node, path, issues)
  validateNumericKeywords(node, path, issues)
  validateStructuredKeywords(node, path, issues)
  for (const child of node.children) {
    validateNode(child, `${path}.${child.name || '<unnamed>'}`, issues)
  }
  if (node.item) {
    validateNode(node.item, `${path}[]`, issues)
  }
  return issues
}

export default function JsonSchemaEditorPage() {
  const { schemaId } = useParams<{ schemaId: string }>()
  const isNew = !schemaId || schemaId === 'new'
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [root, setRoot] = useState<SchemaNode>(() => emptyRoot())
  const [selectedId, setSelectedId] = useState(root.id)
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set([root.id]))
  const [treeSearch, setTreeSearch] = useState('')
  const [pageError, setPageError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [rawImport, setRawImport] = useState('')

  const schemaQuery = useQuery({
    queryKey: ['json-schema', schemaId],
    queryFn: () => jsonSchemasApi.get(schemaId!),
    enabled: !isNew,
  })

  const schemasQuery = useQuery({
    queryKey: ['json-schemas'],
    queryFn: jsonSchemasApi.list,
  })

  useEffect(() => {
    if (!schemaQuery.data) return
    const loadedRoot = fromSchema(schemaQuery.data.schema)
    setName(schemaQuery.data.name)
    setDescription(schemaQuery.data.description)
    setRoot(loadedRoot)
    setSelectedId(loadedRoot.id)
    setExpanded(new Set([loadedRoot.id]))
    setRawImport(JSON.stringify(schemaQuery.data.schema, null, 2))
  }, [schemaQuery.data])

  const generatedSchema = useMemo(() => toSchema(root, true), [root])
  const generatedJSON = useMemo(() => JSON.stringify(generatedSchema, null, 2), [generatedSchema])
  const schemaIssues = useMemo(() => validateNode(root).slice(0, 12), [root])
  const selectedNode = findNode(root, selectedId) ?? root
  const selectedRow = collectVisible(root, new Set([root.id, ...Array.from(expanded)]))
    .find((row) => row.node.id === selectedNode.id)
  const isRootSelected = selectedNode.id === root.id
  const isArrayItem = selectedRow?.parentType === 'array' && selectedNode.name === 'items'
  const availableSchemas = useMemo(
    () => (schemasQuery.data?.schemas ?? []).filter((schema) => schema.id !== schemaId),
    [schemaId, schemasQuery.data?.schemas],
  )
  const matchedReferenceSchema = useMemo(
    () => findSchemaByRef(availableSchemas, selectedNode.ref),
    [availableSchemas, selectedNode.ref],
  )
  const selectedReferenceSchemaId = selectedNode.refSchemaId || matchedReferenceSchema?.id || ''
  const referenceOptionLabel = referenceSelectLabel(schemasQuery.isLoading, availableSchemas.length)
  const referenceWarnings = useMemo(
    () => getReferenceWarnings(selectedNode, availableSchemas, schemaId),
    [availableSchemas, schemaId, selectedNode],
  )

  const createMutation = useMutation({
    mutationFn: jsonSchemasApi.create,
    onSuccess: (schema) => {
      setPageError(null)
      setSaved(true)
      void queryClient.invalidateQueries({ queryKey: ['json-schemas'] })
      navigate(`/json-schemas/${schema.id}/editor`, { replace: true })
    },
    onError: (error: Error) => setPageError(error.message),
  })

  const updateMutation = useMutation({
    mutationFn: (input: CreateJSONSchemaInput) => jsonSchemasApi.update(schemaId!, input),
    onSuccess: (schema) => {
      setPageError(null)
      setSaved(true)
      void queryClient.invalidateQueries({ queryKey: ['json-schemas'] })
      void queryClient.setQueryData(['json-schema', schemaId], schema)
      setTimeout(() => setSaved(false), 2000)
    },
    onError: (error: Error) => setPageError(error.message),
  })

  const deleteMutation = useMutation({
    mutationFn: () => jsonSchemasApi.delete(schemaId!),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['json-schemas'] })
      navigate('/json-schemas', { replace: true })
    },
    onError: (error: Error) => setPageError(error.message),
  })

  const setSelectedNode = (updater: (node: SchemaNode) => SchemaNode) => {
    setRoot((current) => updateNode(current, selectedNode.id, updater))
  }

  const handleSave = () => {
    const input = { name: name.trim(), description: description.trim(), schema: generatedSchema }
    if (!input.name) { setPageError('Name is required.'); return }
    if (isNew) createMutation.mutate(input)
    else updateMutation.mutate(input)
  }

  const handleDelete = () => {
    if (isNew) return
    if (!window.confirm('Delete this JSON schema? Future workflow references to it will need to be updated.')) return
    deleteMutation.mutate()
  }

  const handleAddProperty = () => {
    if (selectedNode.type !== 'object') return
    const child = defaultNode(nextFieldName(selectedNode), 'string')
    setSelectedNode((node) => ({ ...node, children: [...node.children, child] }))
    setSelectedId(child.id)
    setExpanded((current) => new Set(current).add(selectedNode.id))
  }

  const handleEnsureArrayItem = () => {
    if (selectedNode.type !== 'array') return
    const item = defaultNode('items', 'string')
    setSelectedNode((node) => ({ ...node, item }))
    setSelectedId(item.id)
    setExpanded((current) => new Set(current).add(selectedNode.id))
  }

  const handleRemoveNode = () => {
    if (isRootSelected) return
    setRoot((current) => removeNode(current, selectedNode.id))
    setSelectedId(root.id)
  }

  const handleDuplicateNode = () => {
    if (isRootSelected || isArrayItem) return
    let duplicatedId: string | undefined
    setRoot((current) => {
      const result = duplicateNode(current, selectedNode.id)
      duplicatedId = result.duplicatedId
      return result.root
    })
    if (duplicatedId) setSelectedId(duplicatedId)
  }

  const handleTypeChange = (type: SchemaNodeType) => {
    setSelectedNode((node) => ({
      ...node,
      type,
      children: type === 'object' ? node.children : [],
      item: type === 'array' ? node.item ?? defaultNode('items', 'string') : undefined,
    }))
  }

  const handleReferenceToggle = (enabled: boolean) => {
    setSelectedNode((node) => ({
      ...node,
      refEnabled: enabled,
      ref: enabled ? node.ref : '',
      refSchemaId: enabled ? node.refSchemaId : '',
      refOnly: enabled ? node.refOnly : false,
    }))
  }

  const handleReferenceSchemaChange = (id: string) => {
    const target = availableSchemas.find((schema) => schema.id === id)
    setSelectedNode((node) => ({
      ...node,
      refEnabled: true,
      refSchemaId: id,
      ref: target ? schemaReferenceValue(target) : node.ref,
    }))
  }

  const handleApplyRawImport = () => {
    try {
      const parsed = JSON.parse(rawImport)
      const record = asRecord(parsed)
      if (!record) {
        setPageError('Imported schema must be a JSON object.')
        return
      }
      const nextRoot = fromSchema(record)
      setRoot(nextRoot)
      setSelectedId(nextRoot.id)
      setExpanded(new Set([nextRoot.id]))
      setPageError(null)
    } catch (error) {
      setPageError((error as Error).message)
    }
  }

  const handleDownloadRaw = () => {
    const blob = new Blob([generatedJSON], { type: 'application/schema+json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${(name || 'json-schema').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'json-schema'}.schema.json`
    a.click()
    URL.revokeObjectURL(url)
  }

  const handleCopyRaw = async () => {
    await navigator.clipboard.writeText(generatedJSON)
    setSaved(true)
    setTimeout(() => setSaved(false), 1200)
  }

  const visibleRows = collectVisible(root, expanded, 0, [], undefined, treeSearch)
  const isSaving = createMutation.isPending || updateMutation.isPending

  if (!isNew && schemaQuery.isLoading) {
    return <div className="p-8 text-sm text-gray-500 dark:text-slate-400">Loading JSON schema…</div>
  }
  if (!isNew && schemaQuery.error) {
    return <div className="p-8 text-sm text-red-600 dark:text-red-300">Could not load JSON schema.</div>
  }

  return (
    <div className="flex h-full flex-col overflow-hidden">
      <div className="shrink-0 border-b border-gray-200 bg-white px-5 py-3 dark:border-slate-800 dark:bg-slate-900">
        <div className="flex items-center justify-between gap-4">
          <div className="flex items-center gap-3">
            <Link to="/json-schemas" className="inline-flex items-center gap-1.5 text-sm font-medium text-gray-500 transition-colors hover:text-gray-900 dark:text-slate-400 dark:hover:text-slate-100">
              <ArrowLeft className="h-4 w-4" />
              JSON Schemas
            </Link>
            <span className="text-gray-300 dark:text-slate-700">/</span>
            <span className="text-sm font-semibold text-gray-900 dark:text-slate-100">{name || (isNew ? 'New Schema' : '…')}</span>
          </div>
          <div className="flex items-center gap-2">
            {saved && <span className="text-xs font-medium text-emerald-600 dark:text-emerald-400">Saved</span>}
            {pageError && <span className="max-w-xs truncate text-xs font-medium text-red-600 dark:text-red-400">{pageError}</span>}
            {!isNew && (
              <button type="button" onClick={handleDelete} disabled={deleteMutation.isPending} className="inline-flex items-center gap-1.5 rounded-lg border border-red-200 px-3 py-2 text-sm font-semibold text-red-700 transition-colors hover:bg-red-50 disabled:opacity-50 dark:border-red-900/40 dark:text-red-300 dark:hover:bg-red-950/20">
                <Trash2 className="h-4 w-4" />
                Delete
              </button>
            )}
            <button type="button" onClick={handleSave} disabled={isSaving} className="inline-flex items-center gap-1.5 rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-700 disabled:opacity-60">
              <Save className="h-4 w-4" />
              {isSaving ? 'Saving…' : 'Save'}
            </button>
          </div>
        </div>
      </div>

      <div className="shrink-0 border-b border-gray-200 bg-gray-50 px-5 py-2.5 dark:border-slate-800 dark:bg-slate-950">
        <div className="flex flex-wrap items-center gap-4">
          <div className="flex items-center gap-2">
            <label htmlFor="schema-name" className="shrink-0 text-[11px] font-semibold uppercase text-gray-400 dark:text-slate-500">Name</label>
            <input id="schema-name" value={name} onChange={(event) => setName(event.target.value)} placeholder="customer-profile" className="rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100" />
          </div>
          <div className="flex min-w-64 flex-1 items-center gap-2">
            <label htmlFor="schema-description" className="shrink-0 text-[11px] font-semibold uppercase text-gray-400 dark:text-slate-500">Description</label>
            <input id="schema-description" value={description} onChange={(event) => setDescription(event.target.value)} placeholder="Shape of the data this schema describes" className="w-full rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100" />
          </div>
          {!isNew && <span className="font-mono text-[11px] text-gray-400 dark:text-slate-500">id: {schemaId}</span>}
        </div>
      </div>

      <div className="grid min-h-0 flex-1 grid-cols-1 overflow-hidden lg:grid-cols-[360px_minmax(360px,1fr)_440px]">
        <aside className="flex min-h-0 flex-col border-b border-gray-200 bg-white lg:border-b-0 lg:border-r dark:border-slate-800 dark:bg-slate-900">
          <div className="flex shrink-0 items-center justify-between border-b border-gray-200 px-4 py-3 dark:border-slate-800">
            <div>
              <p className="text-sm font-semibold text-gray-900 dark:text-slate-100">Schema Tree</p>
              <p className="text-xs text-gray-400 dark:text-slate-500">{countNodes(root)} nodes</p>
            </div>
            <div className="flex items-center gap-1">
              <button type="button" onClick={handleAddProperty} disabled={selectedNode.type !== 'object'} title="Add property" className="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-gray-200 text-gray-600 transition-colors hover:bg-gray-50 disabled:opacity-40 dark:border-slate-700 dark:text-slate-300 dark:hover:bg-slate-800">
                <Plus className="h-4 w-4" />
              </button>
              <button type="button" onClick={handleDuplicateNode} disabled={isRootSelected || isArrayItem} title="Duplicate node" className="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-gray-200 text-gray-600 transition-colors hover:bg-gray-50 disabled:opacity-40 dark:border-slate-700 dark:text-slate-300 dark:hover:bg-slate-800">
                <Copy className="h-4 w-4" />
              </button>
              <button type="button" onClick={handleRemoveNode} disabled={isRootSelected} title="Delete node" className="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-gray-200 text-gray-600 transition-colors hover:bg-gray-50 disabled:opacity-40 dark:border-slate-700 dark:text-slate-300 dark:hover:bg-slate-800">
                <Trash2 className="h-4 w-4" />
              </button>
            </div>
          </div>
          <div className="shrink-0 border-b border-gray-200 p-3 dark:border-slate-800">
            <div className="flex h-9 items-center gap-2 rounded-lg border border-gray-200 bg-gray-50 px-3 text-gray-500 focus-within:border-primary-500 focus-within:bg-white dark:border-slate-700 dark:bg-slate-950 dark:text-slate-400 dark:focus-within:border-primary-500">
              <Search className="h-4 w-4" />
              <input
                value={treeSearch}
                onChange={(event) => setTreeSearch(event.target.value)}
                placeholder="Find node..."
                className="min-w-0 flex-1 bg-transparent text-sm text-gray-900 outline-none placeholder:text-gray-400 dark:text-slate-100 dark:placeholder:text-slate-500"
              />
              {treeSearch && (
                <button type="button" onClick={() => setTreeSearch('')} className="text-gray-400 transition-colors hover:text-gray-700 dark:hover:text-slate-200" title="Clear search">
                  <X className="h-4 w-4" />
                </button>
              )}
            </div>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto p-3">
            {visibleRows.length === 0 ? (
              <div className="rounded-lg border border-dashed border-gray-200 px-3 py-8 text-center text-xs text-gray-400 dark:border-slate-700 dark:text-slate-500">
                No matching nodes
              </div>
            ) : visibleRows.map(({ node, depth, path }) => {
              const hasChildren = node.children.length > 0 || !!node.item
              const active = node.id === selectedNode.id
              return (
                <div key={node.id} className="mb-1">
                  <div
                    className={clsx(
                      'flex h-10 w-full items-center gap-2 rounded-lg px-2 text-left text-sm transition-colors',
                      active ? 'bg-primary-50 text-primary-800 dark:bg-primary-900/30 dark:text-primary-100' : 'text-gray-700 hover:bg-gray-50 dark:text-slate-300 dark:hover:bg-slate-800',
                    )}
                    style={{ paddingLeft: `${8 + depth * 18}px` }}
                  >
                    <button
                      type="button"
                      disabled={!hasChildren}
                      title={hasChildren ? (expanded.has(node.id) ? 'Collapse node' : 'Expand node') : undefined}
                      onClick={(event) => {
                        event.stopPropagation()
                        setExpanded((current) => {
                          const next = new Set(current)
                          if (next.has(node.id)) next.delete(node.id)
                          else next.add(node.id)
                          return next
                        })
                      }}
                      className="inline-flex h-5 w-5 shrink-0 items-center justify-center rounded text-gray-400 disabled:cursor-default"
                    >
                      <ExpandIcon hasChildren={hasChildren} isExpanded={expanded.has(node.id)} />
                    </button>
                    <button type="button" onClick={() => setSelectedId(node.id)} className="flex min-w-0 flex-1 items-center gap-2 text-left">
                      <span className="min-w-0 flex-1 truncate font-medium">{path.length ? node.name : 'root'}</span>
                      {node.required && <span className="text-[10px] font-semibold text-red-500">req</span>}
                      <span className={clsx('shrink-0 rounded-full px-2 py-0.5 text-[10px] font-semibold', typeBadgeColor(node.type))}>{node.type}</span>
                    </button>
                  </div>
                </div>
              )
            })}
          </div>
        </aside>

        <main className="min-h-0 overflow-y-auto bg-gray-50 p-5 dark:bg-slate-950">
          <div className="space-y-5">
            <div className="rounded-lg border border-gray-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900">
              <div className="mb-4 flex items-center justify-between gap-3">
                <div className="flex items-center gap-2">
                  <Braces className="h-5 w-5 text-primary-600 dark:text-primary-400" />
                  <h2 className="text-base font-semibold text-gray-900 dark:text-slate-100">{isRootSelected ? 'Root Schema' : selectedNode.name}</h2>
                </div>
                {selectedNode.type === 'array' && !selectedNode.item && (
                  <button type="button" onClick={handleEnsureArrayItem} className="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 px-3 py-1.5 text-xs font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800">
                    <Plus className="h-3.5 w-3.5" />
                    Add item schema
                  </button>
                )}
              </div>

              <div className="grid gap-4 md:grid-cols-2">
                <label className="space-y-1.5">
                  <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Property name</span>
                  <input value={isRootSelected ? 'root' : selectedNode.name} disabled={isRootSelected || isArrayItem} onChange={(event) => setSelectedNode((node) => ({ ...node, name: event.target.value }))} className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 disabled:bg-gray-50 disabled:text-gray-400 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100 dark:disabled:bg-slate-900" />
                </label>
                <label className="space-y-1.5">
                  <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Type</span>
                  <select value={selectedNode.type} onChange={(event) => handleTypeChange(event.target.value as SchemaNodeType)} className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100">
                    {schemaTypes.map((type) => <option key={type} value={type}>{type}</option>)}
                  </select>
                </label>
                <label className="space-y-1.5">
                  <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Title</span>
                  <input value={selectedNode.title} onChange={(event) => setSelectedNode((node) => ({ ...node, title: event.target.value }))} className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                </label>
                <label className="space-y-1.5">
                  <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Format</span>
                  <input value={selectedNode.format} onChange={(event) => setSelectedNode((node) => ({ ...node, format: event.target.value }))} placeholder="date-time, email, uri, uuid" className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                </label>
                <label className="space-y-1.5 md:col-span-2">
                  <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Description</span>
                  <textarea value={selectedNode.description} onChange={(event) => setSelectedNode((node) => ({ ...node, description: event.target.value }))} rows={3} className="w-full resize-none rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                </label>
              </div>
            </div>

            <div className="rounded-lg border border-gray-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900">
              <div className="mb-4 flex items-center justify-between gap-3">
                <div>
                  <h3 className="text-sm font-semibold text-gray-900 dark:text-slate-100">Reference</h3>
                  <p className="mt-1 text-xs text-gray-500 dark:text-slate-400">Point this node at another saved JSON schema with $ref.</p>
                </div>
                <label className="flex items-center gap-2 text-sm font-medium text-gray-700 dark:text-slate-200">
                  <input
                    type="checkbox"
                    checked={selectedNode.refEnabled}
                    onChange={(event) => handleReferenceToggle(event.target.checked)}
                    className="h-4 w-4 rounded accent-primary-600"
                  />
                  <span>Use $ref</span>
                </label>
              </div>

              {selectedNode.refEnabled && (
                <div className="grid gap-4 md:grid-cols-2">
                  <label className="space-y-1.5">
                    <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Existing schema</span>
                    <select
                      value={selectedReferenceSchemaId}
                      onChange={(event) => handleReferenceSchemaChange(event.target.value)}
                      disabled={schemasQuery.isLoading || availableSchemas.length === 0}
                      className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 disabled:bg-gray-50 disabled:text-gray-400 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100 dark:disabled:bg-slate-900"
                    >
                      <option value="">{referenceOptionLabel}</option>
                      {availableSchemas.map((schema) => (
                        <option key={schema.id} value={schema.id}>
                          {schema.name}{typeof schema.schema.$id === 'string' && schema.schema.$id.trim() ? '' : ' (local URI)'}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="space-y-1.5">
                    <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">$ref value</span>
                    <input
                      value={selectedNode.ref}
                      onChange={(event) => setSelectedNode((node) => ({ ...node, refEnabled: true, refSchemaId: '', ref: event.target.value }))}
                      placeholder="https://example.com/schema.json#/defs/item"
                      className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 font-mono text-xs outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                    />
                  </label>
                  <label className="flex items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 text-sm text-gray-700 md:col-span-2 dark:border-slate-700 dark:text-slate-200">
                    <input
                      type="checkbox"
                      checked={selectedNode.refOnly}
                      onChange={(event) => setSelectedNode((node) => ({ ...node, refOnly: event.target.checked }))}
                      className="h-4 w-4 rounded accent-primary-600"
                    />
                    <span>Reference only</span>
                  </label>
                  {referenceWarnings.length > 0 && (
                    <div className="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 md:col-span-2 dark:border-amber-900/50 dark:bg-amber-950/20 dark:text-amber-200">
                      {referenceWarnings.map((warning) => (
                        <div key={warning} className="flex items-start gap-2">
                          <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                          <span>{warning}</span>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )}
            </div>

            <div className="rounded-lg border border-gray-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900">
              <h3 className="mb-4 text-sm font-semibold text-gray-900 dark:text-slate-100">Attributes</h3>
              <div className="grid gap-4 md:grid-cols-2">
                {!isRootSelected && !isArrayItem && (
                  <label className="flex items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 text-sm text-gray-700 dark:border-slate-700 dark:text-slate-200">
                    <input type="checkbox" checked={selectedNode.required} onChange={(event) => setSelectedNode((node) => ({ ...node, required: event.target.checked }))} className="h-4 w-4 rounded accent-primary-600" />
                    <span>Required property</span>
                  </label>
                )}
                {selectedNode.type === 'object' && (
                  <label className="flex items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 text-sm text-gray-700 dark:border-slate-700 dark:text-slate-200">
                    <input type="checkbox" checked={selectedNode.additionalProperties} onChange={(event) => setSelectedNode((node) => ({ ...node, additionalProperties: event.target.checked }))} className="h-4 w-4 rounded accent-primary-600" />
                    <span>Allow additional properties</span>
                  </label>
                )}
                {selectedNode.type === 'array' && (
                  <label className="flex items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 text-sm text-gray-700 dark:border-slate-700 dark:text-slate-200">
                    <input type="checkbox" checked={selectedNode.uniqueItems} onChange={(event) => setSelectedNode((node) => ({ ...node, uniqueItems: event.target.checked }))} className="h-4 w-4 rounded accent-primary-600" />
                    <span>Unique items</span>
                  </label>
                )}
                <label className="space-y-1.5 md:col-span-2">
                  <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Enum values</span>
                  <input value={selectedNode.enumValues} onChange={(event) => setSelectedNode((node) => ({ ...node, enumValues: event.target.value }))} placeholder={'"draft", "published", "archived"'} className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                </label>
                <label className="space-y-1.5">
                  <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Default</span>
                  <input value={selectedNode.defaultValue} onChange={(event) => setSelectedNode((node) => ({ ...node, defaultValue: event.target.value }))} placeholder="JSON value or text" className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                </label>
                <label className="space-y-1.5">
                  <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Const</span>
                  <input value={selectedNode.constValue} onChange={(event) => setSelectedNode((node) => ({ ...node, constValue: event.target.value }))} placeholder="JSON value or text" className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                </label>
              </div>
            </div>

            {(selectedNode.type === 'string' || selectedNode.type === 'number' || selectedNode.type === 'integer' || selectedNode.type === 'array') && (
              <div className="rounded-lg border border-gray-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900">
                <h3 className="mb-4 text-sm font-semibold text-gray-900 dark:text-slate-100">Constraints</h3>
                <div className="grid gap-4 md:grid-cols-2">
                  {selectedNode.type === 'string' && (
                    <>
                      <label className="space-y-1.5">
                        <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Min length</span>
                        <input type="number" value={selectedNode.minLength} onChange={(event) => setSelectedNode((node) => ({ ...node, minLength: event.target.value }))} className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                      </label>
                      <label className="space-y-1.5">
                        <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Max length</span>
                        <input type="number" value={selectedNode.maxLength} onChange={(event) => setSelectedNode((node) => ({ ...node, maxLength: event.target.value }))} className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                      </label>
                      <label className="space-y-1.5 md:col-span-2">
                        <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Pattern</span>
                        <input value={selectedNode.pattern} onChange={(event) => setSelectedNode((node) => ({ ...node, pattern: event.target.value }))} placeholder="Regular expression" className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                      </label>
                    </>
                  )}
                  {(selectedNode.type === 'number' || selectedNode.type === 'integer') && (
                    <>
                      <label className="space-y-1.5">
                        <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Minimum</span>
                        <input type="number" value={selectedNode.minimum} onChange={(event) => setSelectedNode((node) => ({ ...node, minimum: event.target.value }))} className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                      </label>
                      <label className="space-y-1.5">
                        <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Maximum</span>
                        <input type="number" value={selectedNode.maximum} onChange={(event) => setSelectedNode((node) => ({ ...node, maximum: event.target.value }))} className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                      </label>
                      <label className="space-y-1.5">
                        <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Exclusive minimum</span>
                        <input type="number" value={selectedNode.exclusiveMinimum} onChange={(event) => setSelectedNode((node) => ({ ...node, exclusiveMinimum: event.target.value }))} className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                      </label>
                      <label className="space-y-1.5">
                        <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Exclusive maximum</span>
                        <input type="number" value={selectedNode.exclusiveMaximum} onChange={(event) => setSelectedNode((node) => ({ ...node, exclusiveMaximum: event.target.value }))} className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                      </label>
                      <label className="space-y-1.5 md:col-span-2">
                        <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Multiple of</span>
                        <input type="number" min="0" value={selectedNode.multipleOf} onChange={(event) => setSelectedNode((node) => ({ ...node, multipleOf: event.target.value }))} className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                      </label>
                    </>
                  )}
                  {selectedNode.type === 'array' && (
                    <>
                      <label className="space-y-1.5">
                        <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Min items</span>
                        <input type="number" value={selectedNode.minItems} onChange={(event) => setSelectedNode((node) => ({ ...node, minItems: event.target.value }))} className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                      </label>
                      <label className="space-y-1.5">
                        <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Max items</span>
                        <input type="number" value={selectedNode.maxItems} onChange={(event) => setSelectedNode((node) => ({ ...node, maxItems: event.target.value }))} className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                      </label>
                    </>
                  )}
                </div>
              </div>
            )}

            {selectedNode.type === 'object' && (
              <div className="rounded-lg border border-gray-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900">
                <h3 className="mb-4 text-sm font-semibold text-gray-900 dark:text-slate-100">Object Constraints</h3>
                <div className="grid gap-4 md:grid-cols-2">
                  <label className="space-y-1.5">
                    <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Min properties</span>
                    <input type="number" min="0" value={selectedNode.minProperties} onChange={(event) => setSelectedNode((node) => ({ ...node, minProperties: event.target.value }))} className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                  </label>
                  <label className="space-y-1.5">
                    <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Max properties</span>
                    <input type="number" min="0" value={selectedNode.maxProperties} onChange={(event) => setSelectedNode((node) => ({ ...node, maxProperties: event.target.value }))} className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                  </label>
                </div>
              </div>
            )}

            <div className="rounded-lg border border-gray-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900">
              <h3 className="mb-4 text-sm font-semibold text-gray-900 dark:text-slate-100">Advanced</h3>
              <div className="grid gap-4 md:grid-cols-2">
                {isRootSelected && (
                  <label className="space-y-1.5 md:col-span-2">
                    <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">$id</span>
                    <input value={selectedNode.schemaId} onChange={(event) => setSelectedNode((node) => ({ ...node, schemaId: event.target.value }))} placeholder="https://example.com/schemas/customer" className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                  </label>
                )}
                <label className="space-y-1.5 md:col-span-2">
                  <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">$comment</span>
                  <textarea value={selectedNode.comment} onChange={(event) => setSelectedNode((node) => ({ ...node, comment: event.target.value }))} rows={2} className="w-full resize-none rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                </label>
                <label className="space-y-1.5 md:col-span-2">
                  <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Examples</span>
                  <textarea value={selectedNode.examples} onChange={(event) => setSelectedNode((node) => ({ ...node, examples: event.target.value }))} rows={3} placeholder={'["example"]'} className="w-full resize-none rounded-lg border border-gray-200 bg-white px-3 py-2 font-mono text-xs outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                </label>
                {!isRootSelected && (
                  <>
                    <label className="flex items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 text-sm text-gray-700 dark:border-slate-700 dark:text-slate-200">
                      <input type="checkbox" checked={selectedNode.deprecated} onChange={(event) => setSelectedNode((node) => ({ ...node, deprecated: event.target.checked }))} className="h-4 w-4 rounded accent-primary-600" />
                      <span>Deprecated</span>
                    </label>
                    <label className="flex items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 text-sm text-gray-700 dark:border-slate-700 dark:text-slate-200">
                      <input type="checkbox" checked={selectedNode.readOnly} onChange={(event) => setSelectedNode((node) => ({ ...node, readOnly: event.target.checked }))} className="h-4 w-4 rounded accent-primary-600" />
                      <span>Read only</span>
                    </label>
                    <label className="flex items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 text-sm text-gray-700 dark:border-slate-700 dark:text-slate-200">
                      <input type="checkbox" checked={selectedNode.writeOnly} onChange={(event) => setSelectedNode((node) => ({ ...node, writeOnly: event.target.checked }))} className="h-4 w-4 rounded accent-primary-600" />
                      <span>Write only</span>
                    </label>
                  </>
                )}
                <label className="space-y-1.5 md:col-span-2">
                  <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Extra keywords</span>
                  <textarea value={selectedNode.extraKeywords} onChange={(event) => setSelectedNode((node) => ({ ...node, extraKeywords: event.target.value }))} rows={4} placeholder={'{"$ref":"#/defs/customer","allOf":[]}'} className="w-full resize-none rounded-lg border border-gray-200 bg-white px-3 py-2 font-mono text-xs outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" />
                </label>
              </div>
            </div>
          </div>
        </main>

        <aside className="flex min-h-0 flex-col border-t border-gray-200 bg-white lg:border-l lg:border-t-0 dark:border-slate-800 dark:bg-slate-900">
          <div className="flex shrink-0 items-center justify-between border-b border-gray-200 px-4 py-3 dark:border-slate-800">
            <div>
              <p className="text-sm font-semibold text-gray-900 dark:text-slate-100">JSON</p>
              <p className="text-xs text-gray-400 dark:text-slate-500">Generated schema and raw import</p>
            </div>
            <div className="flex items-center gap-1">
              <button type="button" onClick={() => void handleCopyRaw()} title="Copy JSON" className="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-gray-200 text-gray-600 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-300 dark:hover:bg-slate-800">
                <Copy className="h-4 w-4" />
              </button>
              <button type="button" onClick={handleDownloadRaw} title="Download JSON Schema" className="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-gray-200 text-gray-600 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-300 dark:hover:bg-slate-800">
                <Download className="h-4 w-4" />
              </button>
            </div>
          </div>
          <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
            <div className={clsx(
              'rounded-lg border px-3 py-3 text-sm',
              schemaIssues.length
                ? 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-900/50 dark:bg-amber-950/20 dark:text-amber-200'
                : 'border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900/50 dark:bg-emerald-950/20 dark:text-emerald-200',
            )}>
              <div className="flex items-center gap-2 font-semibold">
                {schemaIssues.length ? <AlertCircle className="h-4 w-4" /> : <Braces className="h-4 w-4" />}
                {schemaIssueSummary(schemaIssues.length)}
              </div>
              {schemaIssues.length > 0 && (
                <ul className="mt-2 space-y-1 text-xs">
                  {schemaIssues.slice(0, 4).map((issue) => (
                    <li key={issue}>{issue}</li>
                  ))}
                </ul>
              )}
            </div>
            <label className="block space-y-2">
              <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Preview</span>
              <textarea value={generatedJSON} readOnly spellCheck={false} className="h-72 w-full resize-none rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 font-mono text-xs leading-5 text-gray-700 outline-none dark:border-slate-700 dark:bg-slate-950 dark:text-slate-200" />
            </label>
            <label className="block space-y-2">
              <span className="text-xs font-semibold uppercase text-gray-500 dark:text-slate-400">Import Raw JSON</span>
              <textarea value={rawImport} onChange={(event) => setRawImport(event.target.value)} spellCheck={false} placeholder={generatedJSON} className="h-52 w-full resize-none rounded-lg border border-gray-200 bg-white px-3 py-2 font-mono text-xs leading-5 text-gray-700 outline-none transition-colors focus:border-primary-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-200" />
            </label>
            <button type="button" onClick={handleApplyRawImport} className="inline-flex w-full items-center justify-center gap-2 rounded-lg border border-gray-200 px-4 py-2 text-sm font-semibold text-gray-700 transition-colors hover:bg-gray-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800">
              <Upload className="h-4 w-4" />
              Apply Raw JSON
            </button>
          </div>
        </aside>
      </div>
    </div>
  )
}
