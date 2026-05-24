/* eslint-disable react-refresh/only-export-components */
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { HealthResponse, WorkflowLiveEvent, WorkflowLiveStatus } from '../types'
import { liveBus } from './liveBus'

interface WorkflowLiveContextValue {
  bus: typeof liveBus
  status: WorkflowLiveStatus
}

const WorkflowLiveContext = createContext<WorkflowLiveContextValue>({
  bus: liveBus,
  status: 'disconnected',
})

type QC = ReturnType<typeof useQueryClient>

function getWorkflowId(event: WorkflowLiveEvent): string | undefined {
  if (event.entityId) return event.entityId
  if (event.payload && typeof event.payload === 'object' && 'workflowId' in event.payload) {
    return String((event.payload as Record<string, unknown>).workflowId)
  }
  return undefined
}

function handleWorkflowLiveEvent(queryClient: QC, event: WorkflowLiveEvent) {
  switch (event.entity) {
    case 'definition':
      void queryClient.invalidateQueries({ queryKey: ['workflow-definitions'] })
      if (event.entityId) {
        void queryClient.invalidateQueries({ queryKey: ['workflow-definition', event.entityId] })
      }
      break

    case 'workflow': {
      const workflowId = getWorkflowId(event)
      void queryClient.invalidateQueries({ queryKey: ['workflows'] })
      void queryClient.invalidateQueries({ queryKey: ['waiting-signal-workflows'] })
      void queryClient.invalidateQueries({ queryKey: ['workflow-operations'] })
      if (workflowId) {
        void queryClient.invalidateQueries({ queryKey: ['workflow', workflowId] })
        void queryClient.invalidateQueries({ queryKey: ['workflow-history', workflowId] })
      }
      break
    }

    case 'task': {
      const workflowId = getWorkflowId(event)
      void queryClient.invalidateQueries({ queryKey: ['workflow-tasks'] })
      void queryClient.invalidateQueries({ queryKey: ['workflow-operations'] })
      if (workflowId) {
        void queryClient.invalidateQueries({ queryKey: ['workflow', workflowId] })
        void queryClient.invalidateQueries({ queryKey: ['workflow-history', workflowId] })
      }
      break
    }

    case 'operation':
      void queryClient.invalidateQueries({ queryKey: ['workflow-operations'] })
      void queryClient.invalidateQueries({ queryKey: ['workflows'] })
      break

    case 'queue':
      void queryClient.invalidateQueries({ queryKey: ['workflow-tasks'] })
      void queryClient.invalidateQueries({ queryKey: ['workflow-operations'] })
      break

    case 'script':
      void queryClient.invalidateQueries({ queryKey: ['scripts'] })
      if (event.entityId) {
        void queryClient.invalidateQueries({ queryKey: ['script', event.entityId] })
      }
      break

    case 'agent':
      void queryClient.invalidateQueries({ queryKey: ['agents'] })
      if (event.entityId) {
        void queryClient.invalidateQueries({ queryKey: ['agent', event.entityId] })
        void queryClient.invalidateQueries({ queryKey: ['agent-connectors', event.entityId] })
      }
      break

    case 'mcp_server':
      void queryClient.invalidateQueries({ queryKey: ['connectors'] })
      if (event.entityId) {
        void queryClient.invalidateQueries({ queryKey: ['connector', event.entityId] })
      }
      break

    case 'health':
      if (event.type === 'health.updated' && event.payload) {
        queryClient.setQueryData(['health'], event.payload as HealthResponse)
      }
      break

    default:
      break
  }
}

export function WorkflowLiveProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  const [status, setStatus] = useState<WorkflowLiveStatus>(liveBus.getStatus())

  // Adaptive staleTime: when WS is live, data only updates via push — no background polling.
  // When WS drops, fall back to staleTime: 0 so queries refetch on mount/focus as normal.
  useEffect(() => {
    const unsubscribeStatus = liveBus.subscribeStatus((s) => {
      queryClient.setDefaultOptions({
        queries: { staleTime: s === 'connected' ? 5 * 60_000 : 0 },
      })
    })
    return unsubscribeStatus
  }, [queryClient])

  useEffect(() => {
    liveBus.connect()

    const unsubscribeStatus = liveBus.subscribeStatus(setStatus)
    const unsubscribeEvents = liveBus.subscribe((event: WorkflowLiveEvent) => {
      if (event.type === 'connection.ready') return
      handleWorkflowLiveEvent(queryClient, event)
    })

    return () => {
      unsubscribeStatus()
      unsubscribeEvents()
      liveBus.disconnect()
    }
  }, [queryClient])

  const value = useMemo(() => ({ bus: liveBus, status }), [status])
  return <WorkflowLiveContext.Provider value={value}>{children}</WorkflowLiveContext.Provider>
}

export function useLiveBus() {
  return useContext(WorkflowLiveContext)
}

export function useWorkflowLive() {
  return useLiveBus()
}
