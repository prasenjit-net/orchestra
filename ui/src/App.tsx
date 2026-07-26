import { lazy, Suspense, type ReactNode } from 'react'
import { Navigate, Route, Routes } from 'react-router-dom'
import Layout from './components/Layout'
import DashboardPage from './pages/DashboardPage'
import OperationsPage from './pages/OperationsPage'
import QueuesPage from './pages/QueuesPage'
import RunDetailsPage from './pages/RunDetailsPage'
import RunsPage from './pages/RunsPage'
import AgentsPage from './pages/AgentsPage'
import ConnectorsPage from './pages/ConnectorsPage'
import ScriptsPage from './pages/ScriptsPage'
import JsonSchemasPage from './pages/JsonSchemasPage'
import SignalsPage from './pages/SignalsPage'
import ClusterPage from './pages/ClusterPage'
import SettingsPage from './pages/SettingsPage'
import WorkflowListPage from './pages/WorkflowListPage'
import WorkflowVersionsPage from './pages/WorkflowVersionsPage'
import LoginPage from './pages/LoginPage'
import ChangePasswordPage from './pages/ChangePasswordPage'
import SecurityPage from './pages/SecurityPage'
import ProtectedRoute from './auth/ProtectedRoute'
import { WorkflowLiveProvider } from './live/WorkflowLiveProvider'
import { useAuth } from './auth/AuthProvider'

const WorkflowDesignerPage = lazy(() => import('./pages/WorkflowDesignerPage'))
const ScriptEditorPage = lazy(() => import('./pages/ScriptEditorPage'))
const JsonSchemaEditorPage = lazy(() => import('./pages/JsonSchemaEditorPage'))
const AgentEditorPage = lazy(() => import('./pages/AgentEditorPage'))
const ConnectorEditorPage = lazy(() => import('./pages/ConnectorEditorPage'))

function PageLoader() {
  return <div className="flex h-64 items-center justify-center text-sm text-gray-500 dark:text-slate-400">Loading…</div>
}

function LiveLayout() {
  return <WorkflowLiveProvider><Layout /></WorkflowLiveProvider>
}

function PermissionRoute({ permission, children }: { permission: string; children: ReactNode }) {
  const { hasPermission } = useAuth()
  return hasPermission(permission) ? children : <Navigate to="/dashboard" replace />
}

function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route element={<ProtectedRoute />}>
        <Route path="/change-password" element={<ChangePasswordPage />} />
        <Route path="/" element={<LiveLayout />}>
          <Route index element={<Navigate to="/dashboard" replace />} />
          <Route path="dashboard" element={<DashboardPage />} />
          <Route path="workflows" element={<WorkflowListPage />} />
          <Route path="workflows/new" element={<PermissionRoute permission="workflow.definition.write"><Suspense fallback={<PageLoader />}><WorkflowDesignerPage /></Suspense></PermissionRoute>} />
          <Route path="workflows/:definitionId/designer" element={<PermissionRoute permission="workflow.definition.write"><Suspense fallback={<PageLoader />}><WorkflowDesignerPage /></Suspense></PermissionRoute>} />
          <Route path="workflows/:definitionId/versions" element={<WorkflowVersionsPage />} />
          <Route path="workflows/designer" element={<PermissionRoute permission="workflow.definition.write"><Suspense fallback={<PageLoader />}><WorkflowDesignerPage /></Suspense></PermissionRoute>} />
          <Route path="workflows/designer/:definitionId" element={<PermissionRoute permission="workflow.definition.write"><Suspense fallback={<PageLoader />}><WorkflowDesignerPage /></Suspense></PermissionRoute>} />
          <Route path="workflows/operations" element={<Navigate to="/operations" replace />} />
          <Route path="scripts" element={<ScriptsPage />} />
          <Route path="scripts/new" element={<PermissionRoute permission="resource.write"><Suspense fallback={<PageLoader />}><ScriptEditorPage /></Suspense></PermissionRoute>} />
          <Route path="scripts/:scriptId/editor" element={<PermissionRoute permission="resource.write"><Suspense fallback={<PageLoader />}><ScriptEditorPage /></Suspense></PermissionRoute>} />
          <Route path="json-schemas" element={<JsonSchemasPage />} />
          <Route path="json-schemas/new" element={<PermissionRoute permission="resource.write"><Suspense fallback={<PageLoader />}><JsonSchemaEditorPage /></Suspense></PermissionRoute>} />
          <Route path="json-schemas/:schemaId/editor" element={<PermissionRoute permission="resource.write"><Suspense fallback={<PageLoader />}><JsonSchemaEditorPage /></Suspense></PermissionRoute>} />
          <Route path="agents" element={<AgentsPage />} />
          <Route path="agents/new" element={<PermissionRoute permission="resource.write"><Suspense fallback={<PageLoader />}><AgentEditorPage /></Suspense></PermissionRoute>} />
          <Route path="agents/:agentId/editor" element={<PermissionRoute permission="resource.write"><Suspense fallback={<PageLoader />}><AgentEditorPage /></Suspense></PermissionRoute>} />
          <Route path="connectors" element={<ConnectorsPage />} />
          <Route path="connectors/new" element={<PermissionRoute permission="resource.write"><Suspense fallback={<PageLoader />}><ConnectorEditorPage /></Suspense></PermissionRoute>} />
          <Route path="connectors/:connectorId/editor" element={<PermissionRoute permission="resource.write"><Suspense fallback={<PageLoader />}><ConnectorEditorPage /></Suspense></PermissionRoute>} />
          <Route path="runs" element={<RunsPage />} />
          <Route path="runs/:workflowId" element={<RunDetailsPage />} />
          <Route path="signals" element={<SignalsPage />} />
          <Route path="queues" element={<QueuesPage />} />
          <Route path="operations" element={<OperationsPage />} />
          <Route path="cluster" element={<ClusterPage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="security" element={<SecurityPage />} />
        </Route>
      </Route>
    </Routes>
  )
}

export default App
