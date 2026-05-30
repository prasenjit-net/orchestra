import { useEffect, useRef, useState } from 'react'
import { Bot, Send, Sparkles, User, X } from 'lucide-react'
import { scriptAiApi } from '../services/api'

interface Message {
  role: 'user' | 'assistant'
  content: string
}

interface Props {
  currentScript: string
  onApply: (script: string) => void
  onClose: () => void
}

// Splits a message into alternating prose and code-block segments.
function parseContent(content: string): { type: 'prose' | 'code'; text: string }[] {
  const parts: { type: 'prose' | 'code'; text: string }[] = []
  const codeBlockRE = /```(?:python|starlark|py)?\n?([\s\S]*?)```/g
  let lastIndex = 0
  let match: RegExpExecArray | null

  while ((match = codeBlockRE.exec(content)) !== null) {
    const prose = content.slice(lastIndex, match.index).trim()
    if (prose) parts.push({ type: 'prose', text: prose })
    const code = match[1].trim()
    if (code) parts.push({ type: 'code', text: code })
    lastIndex = match.index + match[0].length
  }

  const trailing = content.slice(lastIndex).trim()
  if (trailing) parts.push({ type: 'prose', text: trailing })
  return parts
}

function UserBubble({ text }: { text: string }) {
  return (
    <div className="flex items-start justify-end gap-2">
      <div className="max-w-[80%] rounded-2xl rounded-tr-sm bg-primary-600 px-4 py-2.5 text-sm text-white">
        {text}
      </div>
      <span className="mt-1 flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-primary-100 dark:bg-primary-900/40">
        <User className="h-3.5 w-3.5 text-primary-600 dark:text-primary-400" />
      </span>
    </div>
  )
}

function AssistantBubble({ content, activeScript, onApply }: { content: string; activeScript: string; onApply: (s: string) => void }) {
  const segments = parseContent(content)
  return (
    <div className="flex items-start gap-2">
      <span className="mt-1 flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-violet-100 dark:bg-violet-900/40">
        <Bot className="h-3.5 w-3.5 text-violet-600 dark:text-violet-400" />
      </span>
      <div className="max-w-[85%] space-y-2">
        {segments.map((seg, i) =>
          seg.type === 'prose' ? (
            <p key={i} className="text-sm leading-relaxed text-gray-800 dark:text-slate-200">
              {seg.text}
            </p>
          ) : (
            <div key={i} className="overflow-hidden rounded-xl border border-gray-200 dark:border-slate-700">
              <pre className="overflow-x-auto bg-slate-950 px-4 py-3 text-xs leading-relaxed text-slate-200">
                {seg.text}
              </pre>
              <div className="flex items-center gap-2 border-t border-gray-200 bg-gray-50 px-3 py-2 dark:border-slate-700 dark:bg-slate-800">
                <button
                  type="button"
                  onClick={() => onApply(seg.text)}
                  className="inline-flex items-center gap-1.5 rounded-lg bg-primary-600 px-3 py-1.5 text-xs font-semibold text-white transition-colors hover:bg-primary-700"
                >
                  <Sparkles className="h-3 w-3" />
                  Use this script
                </button>
                {activeScript === seg.text && (
                  <span className="text-[11px] font-medium text-emerald-600 dark:text-emerald-400">✓ Applied</span>
                )}
              </div>
            </div>
          ),
        )}
      </div>
    </div>
  )
}

const GREETING = `Hi! I can help you write a Starlark script for your workflow step.

Tell me what the script should do — for example, what data it should read from the workflow context and what it should output — and I'll write it for you.

You can also paste a partial script and ask me to improve or extend it.`

export default function ScriptAssistModal({ currentScript, onApply, onClose }: Props) {
  const [messages, setMessages] = useState<Message[]>([
    { role: 'assistant', content: GREETING },
  ])
  const [activeScript, setActiveScript] = useState(currentScript)
  const [input, setInput] = useState('')
  const [isPending, setIsPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const scrollRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  // Auto-scroll on new messages.
  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [messages, isPending])

  // Auto-resize textarea.
  const handleInputChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    setInput(e.target.value)
    e.target.style.height = 'auto'
    e.target.style.height = Math.min(e.target.scrollHeight, 120) + 'px'
  }

  const send = async () => {
    const text = input.trim()
    if (!text || isPending) return

    const userMsg: Message = { role: 'user', content: text }
    const next = [...messages, userMsg]
    setMessages(next)
    setInput('')
    setError(null)
    setIsPending(true)

    // Reset textarea height.
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto'
    }

    try {
      // Send all non-greeting messages as the conversation history.
      const history = next
        .filter((m) => !(m.role === 'assistant' && m.content === GREETING))
        .map((m) => ({ role: m.role, content: m.content }))
      const { content } = await scriptAiApi.assist(history, activeScript)
      setMessages((prev) => [...prev, { role: 'assistant', content }])
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setIsPending(false)
    }
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      void send()
    }
  }

  const handleApply = (script: string) => {
    setActiveScript(script)
    onApply(script)
    // Stay open so the user can continue refining in the same conversation.
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 backdrop-blur-sm">
      <div className="mx-4 flex w-full max-w-2xl flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-2xl dark:border-slate-700 dark:bg-slate-900"
        style={{ height: '80vh' }}>
        {/* Header */}
        <div className="flex shrink-0 items-center justify-between border-b border-gray-200 px-5 py-4 dark:border-slate-700">
          <div className="flex items-center gap-2">
            <span className="flex h-7 w-7 items-center justify-center rounded-full bg-violet-100 dark:bg-violet-900/40">
              <Bot className="h-4 w-4 text-violet-600 dark:text-violet-400" />
            </span>
            <div>
              <h2 className="text-sm font-semibold text-gray-900 dark:text-slate-100">Script Assistant</h2>
              <p className="text-[11px] text-gray-400 dark:text-slate-500">Conversation resets when closed</p>
            </div>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg p-1.5 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 dark:text-slate-500 dark:hover:bg-slate-800 dark:hover:text-slate-300"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Messages */}
        <div ref={scrollRef} className="flex-1 space-y-4 overflow-y-auto px-5 py-4">
          {messages.map((msg, i) =>
            msg.role === 'user' ? (
              <UserBubble key={i} text={msg.content} />
            ) : (
              <AssistantBubble key={i} content={msg.content} activeScript={activeScript} onApply={handleApply} />
            ),
          )}
          {isPending && (
            <div className="flex items-start gap-2">
              <span className="mt-1 flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-violet-100 dark:bg-violet-900/40">
                <Bot className="h-3.5 w-3.5 text-violet-600 dark:text-violet-400" />
              </span>
              <div className="flex items-center gap-1.5 rounded-2xl rounded-tl-sm bg-gray-100 px-4 py-3 dark:bg-slate-800">
                <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-gray-400 dark:bg-slate-400" style={{ animationDelay: '0ms' }} />
                <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-gray-400 dark:bg-slate-400" style={{ animationDelay: '150ms' }} />
                <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-gray-400 dark:bg-slate-400" style={{ animationDelay: '300ms' }} />
              </div>
            </div>
          )}
          {error && (
            <p className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-600 dark:border-red-900/40 dark:bg-red-950/20 dark:text-red-400">
              {error}
            </p>
          )}
        </div>

        {/* Input bar */}
        <div className="shrink-0 border-t border-gray-200 bg-gray-50 px-4 py-3 dark:border-slate-700 dark:bg-slate-950">
          {activeScript && (
            <p className="mb-2 text-[11px] text-gray-400 dark:text-slate-500">
              {activeScript === currentScript
                ? 'Current script passed as context — ask the assistant to improve or extend it.'
                : 'Applied script is sent as context for further refinement.'}
            </p>
          )}
          <div className="flex items-end gap-2">
            <textarea
              ref={textareaRef}
              rows={1}
              value={input}
              onChange={handleInputChange}
              onKeyDown={handleKeyDown}
              placeholder="Describe what the script should do… (Enter to send, Shift+Enter for newline)"
              disabled={isPending}
              className="flex-1 resize-none rounded-xl border border-gray-200 bg-white px-3 py-2.5 text-sm text-gray-900 outline-none transition-colors focus:border-primary-500 disabled:opacity-60 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100 dark:placeholder:text-slate-500"
              style={{ minHeight: '40px' }}
            />
            <button
              type="button"
              onClick={() => void send()}
              disabled={!input.trim() || isPending}
              className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-primary-600 text-white transition-colors hover:bg-primary-700 disabled:cursor-not-allowed disabled:opacity-50"
            >
              <Send className="h-4 w-4" />
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
