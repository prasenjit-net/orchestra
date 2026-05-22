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

function invalidateWorkflowQueries(queryClient: ReturnType<typeof useQueryClient>, workflowId?: string) {
  void queryClient.invalidateQueries({ queryKey: ['workflows'] })
  void queryClient.invalidateQueries({ queryKey: ['workflow-tasks'] })
  void queryClient.invalidateQueries({ queryKey: ['workflow-operations'] })
  if (workflowId) {
    void queryClient.invalidateQueries({ queryKey: ['workflow', workflowId] })
    void queryClient.invalidateQueries({ queryKey: ['workflow-history', workflowId] })
  }
}

function handleWorkflowLiveEvent(queryClient: ReturnType<typeof useQueryClient>, event: WorkflowLiveEvent) {
  switch (event.entity) {
    case 'definition':
      void queryClient.invalidateQueries({ queryKey: ['workflow-definitions'] })
      if (event.entityId) {
        void queryClient.invalidateQueries({ queryKey: ['workflow-definition', event.entityId] })
      }
      break
    case 'workflow': {
      const workflowId =
        event.entityId ||
        (event.payload && typeof event.payload === 'object' && 'workflowId' in event.payload ? String(event.payload.workflowId) : undefined)
      invalidateWorkflowQueries(queryClient, workflowId)
      break
    }
    case 'task': {
      const workflowId =
        event.payload && typeof event.payload === 'object' && 'workflowId' in event.payload ? String(event.payload.workflowId) : undefined
      invalidateWorkflowQueries(queryClient, workflowId)
      break
    }
    case 'operation':
      invalidateWorkflowQueries(
        queryClient,
        event.payload && typeof event.payload === 'object' && 'workflowId' in event.payload ? String(event.payload.workflowId) : undefined,
      )
      break
    case 'queue':
      invalidateWorkflowQueries(queryClient)
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

  useEffect(() => {
    liveBus.connect()

    const unsubscribeStatus = liveBus.subscribeStatus(setStatus)
    const unsubscribeEvents = liveBus.subscribe((event: WorkflowLiveEvent) => {
      if (event.type === 'connection.ready') {
        return
      }
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
