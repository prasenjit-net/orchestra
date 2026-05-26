import { lazy, Suspense } from 'react'
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
import SignalsPage from './pages/SignalsPage'
import ClusterPage from './pages/ClusterPage'
import SettingsPage from './pages/SettingsPage'
import WorkflowListPage from './pages/WorkflowListPage'

const WorkflowDesignerPage = lazy(() => import('./pages/WorkflowDesignerPage'))
const ScriptEditorPage = lazy(() => import('./pages/ScriptEditorPage'))
const AgentEditorPage = lazy(() => import('./pages/AgentEditorPage'))
const ConnectorEditorPage = lazy(() => import('./pages/ConnectorEditorPage'))

function PageLoader() {
  return <div className="flex h-64 items-center justify-center text-sm text-gray-500 dark:text-slate-400">Loading…</div>
}

function App() {
  return (
    <Routes>
      <Route path="/" element={<Layout />}>
        <Route index element={<Navigate to="/dashboard" replace />} />
        <Route path="dashboard" element={<DashboardPage />} />
        <Route path="workflows" element={<WorkflowListPage />} />
        <Route path="workflows/new" element={<Suspense fallback={<PageLoader />}><WorkflowDesignerPage /></Suspense>} />
        <Route path="workflows/:definitionId/designer" element={<Suspense fallback={<PageLoader />}><WorkflowDesignerPage /></Suspense>} />
        <Route path="workflows/designer" element={<Suspense fallback={<PageLoader />}><WorkflowDesignerPage /></Suspense>} />
        <Route path="workflows/designer/:definitionId" element={<Suspense fallback={<PageLoader />}><WorkflowDesignerPage /></Suspense>} />
        <Route path="workflows/operations" element={<Navigate to="/operations" replace />} />
        <Route path="scripts" element={<ScriptsPage />} />
        <Route path="scripts/new" element={<Suspense fallback={<PageLoader />}><ScriptEditorPage /></Suspense>} />
        <Route path="scripts/:scriptId/editor" element={<Suspense fallback={<PageLoader />}><ScriptEditorPage /></Suspense>} />
        <Route path="agents" element={<AgentsPage />} />
        <Route path="agents/new" element={<Suspense fallback={<PageLoader />}><AgentEditorPage /></Suspense>} />
        <Route path="agents/:agentId/editor" element={<Suspense fallback={<PageLoader />}><AgentEditorPage /></Suspense>} />
        <Route path="connectors" element={<ConnectorsPage />} />
        <Route path="connectors/new" element={<Suspense fallback={<PageLoader />}><ConnectorEditorPage /></Suspense>} />
        <Route path="connectors/:connectorId/editor" element={<Suspense fallback={<PageLoader />}><ConnectorEditorPage /></Suspense>} />
        <Route path="runs" element={<RunsPage />} />
        <Route path="runs/:workflowId" element={<RunDetailsPage />} />
        <Route path="signals" element={<SignalsPage />} />
        <Route path="queues" element={<QueuesPage />} />
        <Route path="operations" element={<OperationsPage />} />
        <Route path="cluster" element={<ClusterPage />} />
        <Route path="settings" element={<SettingsPage />} />
      </Route>
    </Routes>
  )
}

export default App
