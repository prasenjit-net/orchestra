import { buildWebSocketUrl } from '../services/api'
import type { WorkflowLiveEvent, WorkflowLiveStatus } from '../types'

type EventListener = (event: WorkflowLiveEvent) => void
type StatusListener = (status: WorkflowLiveStatus) => void

class LiveBus {
  private socket: WebSocket | null = null
  private reconnectTimer: number | null = null
  private reconnectAttempts = 0
  private connected = false
  private status: WorkflowLiveStatus = 'disconnected'
  private readonly eventListeners = new Set<EventListener>()
  private readonly statusListeners = new Set<StatusListener>()

  connect() {
    if (this.connected || typeof window === 'undefined') {
      return
    }

    this.connected = true
    this.open()
  }

  disconnect() {
    this.connected = false
    if (this.reconnectTimer) {
      window.clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    this.socket?.close()
    this.socket = null
    this.setStatus('disconnected')
  }

  subscribe(listener: EventListener) {
    this.eventListeners.add(listener)
    return () => {
      this.eventListeners.delete(listener)
    }
  }

  subscribeStatus(listener: StatusListener) {
    this.statusListeners.add(listener)
    listener(this.status)
    return () => {
      this.statusListeners.delete(listener)
    }
  }

  getStatus() {
    return this.status
  }

  private open() {
    if (!this.connected) {
      return
    }

    this.setStatus(this.reconnectAttempts === 0 ? 'connecting' : 'reconnecting')
    this.socket = new WebSocket(buildWebSocketUrl())

    this.socket.onopen = () => {
      this.reconnectAttempts = 0
      this.setStatus('connected')
    }

    this.socket.onmessage = (message) => {
      try {
        const event = JSON.parse(message.data) as WorkflowLiveEvent
        this.eventListeners.forEach((listener) => listener(event))
      } catch {
        // ignore malformed bus events
      }
    }

    this.socket.onerror = () => {
      this.socket?.close()
    }

    this.socket.onclose = () => {
      this.socket = null
      if (!this.connected) {
        this.setStatus('disconnected')
        return
      }

      this.reconnectAttempts += 1
      this.setStatus('reconnecting')
      this.reconnectTimer = window.setTimeout(() => this.open(), Math.min(5000, 500 * 2 ** Math.min(this.reconnectAttempts, 4)))
    }
  }

  private setStatus(status: WorkflowLiveStatus) {
    this.status = status
    this.statusListeners.forEach((listener) => listener(status))
  }
}

export const liveBus = new LiveBus()
