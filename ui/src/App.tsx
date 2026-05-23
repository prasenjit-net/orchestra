import { Navigate, Route, Routes } from 'react-router-dom'
import Layout from './components/Layout'
import DashboardPage from './pages/DashboardPage'
import OperationsPage from './pages/OperationsPage'
import QueuesPage from './pages/QueuesPage'
import RunDetailsPage from './pages/RunDetailsPage'
import RunsPage from './pages/RunsPage'
import ScriptEditorPage from './pages/ScriptEditorPage'
import ScriptsPage from './pages/ScriptsPage'
import SignalsPage from './pages/SignalsPage'
import SettingsPage from './pages/SettingsPage'
import WorkflowDesignerPage from './pages/WorkflowDesignerPage'
import WorkflowListPage from './pages/WorkflowListPage'

function App() {
  return (
    <Routes>
      <Route path="/" element={<Layout />}>
        <Route index element={<Navigate to="/dashboard" replace />} />
        <Route path="dashboard" element={<DashboardPage />} />
        <Route path="workflows" element={<WorkflowListPage />} />
        <Route path="workflows/new" element={<WorkflowDesignerPage />} />
        <Route path="workflows/:definitionId/designer" element={<WorkflowDesignerPage />} />
        <Route path="workflows/designer" element={<WorkflowDesignerPage />} />
        <Route path="workflows/designer/:definitionId" element={<WorkflowDesignerPage />} />
        <Route path="workflows/operations" element={<Navigate to="/operations" replace />} />
        <Route path="scripts" element={<ScriptsPage />} />
        <Route path="scripts/new" element={<ScriptEditorPage />} />
        <Route path="scripts/:scriptId/editor" element={<ScriptEditorPage />} />
        <Route path="runs" element={<RunsPage />} />
        <Route path="runs/:workflowId" element={<RunDetailsPage />} />
        <Route path="signals" element={<SignalsPage />} />
        <Route path="queues" element={<QueuesPage />} />
        <Route path="operations" element={<OperationsPage />} />
        <Route path="settings" element={<SettingsPage />} />
      </Route>
    </Routes>
  )
}

export default App
