import React from 'react'
import ReactDOM from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import '@xyflow/react/dist/style.css'
import App from './App'
import './index.css'
import { WorkflowLiveProvider } from './live/WorkflowLiveProvider'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5000,
      refetchOnWindowFocus: false,
    },
  },
})

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <WorkflowLiveProvider>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </WorkflowLiveProvider>
    </QueryClientProvider>
  </React.StrictMode>,
)
