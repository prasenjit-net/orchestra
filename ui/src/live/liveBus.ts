import { buildWebSocketUrl } from '../services/api'
import type { WorkflowLiveEvent, WorkflowLiveStatus } from '../types'

type EventListener = (event: WorkflowLiveEvent) => void
type StatusListener = (status: WorkflowLiveStatus) => void

const WATCHDOG_INTERVAL_MS = 10_000
const WATCHDOG_TIMEOUT_MS = 45_000

class LiveBus {
  private socket: WebSocket | null = null
  private reconnectTimer: number | null = null
  private watchdogTimer: number | null = null
  private lastMessageAt = 0
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
    this.clearWatchdog()
    if (this.reconnectTimer) {
      window.clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    this.socket?.close()
    this.socket = null
    this.setStatus('disconnected')
  }

  private startWatchdog() {
    this.clearWatchdog()
    this.lastMessageAt = Date.now()
    this.watchdogTimer = window.setInterval(() => {
      if (this.status === 'connected' && Date.now() - this.lastMessageAt > WATCHDOG_TIMEOUT_MS) {
        // Silent connection death — force reconnect
        this.socket?.close()
      }
    }, WATCHDOG_INTERVAL_MS)
  }

  private clearWatchdog() {
    if (this.watchdogTimer !== null) {
      window.clearInterval(this.watchdogTimer)
      this.watchdogTimer = null
    }
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
      this.startWatchdog()
    }

    this.socket.onmessage = (message) => {
      this.lastMessageAt = Date.now()
      try {
        const event = JSON.parse(message.data) as WorkflowLiveEvent
        // heartbeat events keep the connection alive; no need to broadcast them
        if (event.type === 'heartbeat') return
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
      this.clearWatchdog()
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
