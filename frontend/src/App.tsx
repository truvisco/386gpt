import { useCallback, useEffect, useRef, useState } from 'react'
import type { FormEvent, KeyboardEvent } from 'react'
import './App.css'

type Thread = {
  id: string
  title: string
  createdAt: string
  updatedAt: string
  lastMessage: string
  messageCount: number
}

type Message = {
  id: string
  threadId: string
  role: 'user' | 'assistant'
  content: string
  createdAt: string
  streaming?: boolean
}

type SocketEvent = {
  type: 'ready' | 'message' | 'thread_updated' | 'error'
  message?: Message
  thread?: Thread
  error?: string
}

type Runtime = { provider: string; model: string }

const API_BASE = (import.meta.env.VITE_API_URL ?? 'http://localhost:8080').replace(/\/$/, '')
const quickPrompts = [
  ['EXPLAIN', 'Explain how WebSockets differ from HTTP polling'],
  ['BUILD', 'Design a small REST API for a notes app'],
  ['DEBUG', 'Help me trace a race condition in Go'],
  ['IMAGINE', 'Write a cyberpunk story in six sentences'],
]

function upsertMessage(messages: Message[], incoming: Message) {
  const index = messages.findIndex((message) => message.id === incoming.id)
  if (index === -1) return [...messages, incoming]
  const next = [...messages]
  next[index] = incoming
  return next
}

function mergeThreadMessages(persisted: Message[], live: Message[], threadId: string) {
  const liveForThread = live.filter((message) => message.threadId === threadId)
  const persistedIDs = new Set(persisted.map((message) => message.id))
  const liveByID = new Map(liveForThread.map((message) => [message.id, message]))
  return [
    ...persisted.map((message) => liveByID.get(message.id) ?? message),
    ...liveForThread.filter((message) => !persistedIDs.has(message.id)),
  ]
}

function Icon({ name }: { name: 'menu' | 'plus' | 'trash' | 'send' | 'copy' | 'spark' | 'close' }) {
  const paths = {
    menu: <path d="M4 7h16M4 12h16M4 17h16" />,
    plus: <path d="M12 5v14M5 12h14" />,
    trash: <><path d="M4 7h16M9 7V4h6v3M8 10v8M12 10v8M16 10v8" /><path d="M6 7l1 14h10l1-14" /></>,
    send: <><path d="m4 12 16-8-6 16-3-7-7-1Z" /><path d="m11 13 4-4" /></>,
    copy: <><rect x="8" y="8" width="11" height="11" /><path d="M16 8V5H5v11h3" /></>,
    spark: <><path d="m12 3 1.7 5.3L19 10l-5.3 1.7L12 17l-1.7-5.3L5 10l5.3-1.7L12 3Z" /><path d="m5 17 .7 2.3L8 20l-2.3.7L5 23l-.7-2.3L2 20l2.3-.7L5 17Z" /></>,
    close: <path d="m6 6 12 12M18 6 6 18" />,
  }
  return <svg viewBox="0 0 24 24" aria-hidden="true">{paths[name]}</svg>
}

function App() {
  const [threads, setThreads] = useState<Thread[]>([])
  const [activeId, setActiveId] = useState<string | null>(null)
  const [messages, setMessages] = useState<Message[]>([])
  const [draft, setDraft] = useState('')
  const [loading, setLoading] = useState(true)
  const [sending, setSending] = useState(false)
  const [connected, setConnected] = useState(false)
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [runtime, setRuntime] = useState<Runtime>({ provider: 'HERMES', model: 'AUTO' })
  const socketRef = useRef<WebSocket | null>(null)
  const socketThreadRef = useRef<string | null>(null)
  const messagesEndRef = useRef<HTMLDivElement | null>(null)
  const textareaRef = useRef<HTMLTextAreaElement | null>(null)
  const assistantTargetsRef = useRef<Map<string, Message>>(new Map())
  const renderedLengthsRef = useRef<Map<string, number>>(new Map())
  const typingTimersRef = useRef<Map<string, number>>(new Map())

  const request = useCallback(async <T,>(path: string, options?: RequestInit): Promise<T> => {
    const response = await fetch(`${API_BASE}${path}`, {
      ...options,
      headers: { 'Content-Type': 'application/json', ...options?.headers },
    })
    if (!response.ok) {
      const body = await response.json().catch(() => ({ error: response.statusText }))
      throw new Error(body.error ?? 'Request failed')
    }
    return response.json() as Promise<T>
  }, [])

  const clearTypingAnimations = useCallback(() => {
    typingTimersRef.current.forEach((timer) => window.clearInterval(timer))
    typingTimersRef.current.clear()
    assistantTargetsRef.current.clear()
    renderedLengthsRef.current.clear()
  }, [])

  const animateAssistant = useCallback((incoming: Message) => {
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      setMessages((current) => upsertMessage(current, incoming))
      if (!incoming.streaming) setSending(false)
      return
    }

    assistantTargetsRef.current.set(incoming.id, incoming)
    if (!renderedLengthsRef.current.has(incoming.id)) {
      renderedLengthsRef.current.set(incoming.id, 0)
      setMessages((current) => [...current, { ...incoming, content: '', streaming: true }])
    }
    if (typingTimersRef.current.has(incoming.id)) return

    const speedFactor = window._386?.speedFactor || 1
    const interval = Math.max(8, 18 / speedFactor)
    const timer = window.setInterval(() => {
      const target = assistantTargetsRef.current.get(incoming.id)
      if (!target) return
      const currentLength = renderedLengthsRef.current.get(incoming.id) ?? 0
      const targetCharacters = Array.from(target.content)
      const nextLength = Math.min(currentLength + 2, targetCharacters.length)
      const caughtUp = nextLength >= targetCharacters.length
      const complete = caughtUp && !target.streaming

      renderedLengthsRef.current.set(incoming.id, nextLength)
      setMessages((current) => {
        const index = current.findIndex((message) => message.id === incoming.id)
        const animated = {
          ...target,
          content: targetCharacters.slice(0, nextLength).join(''),
          streaming: !complete,
        }
        if (index === -1) return [...current, animated]
        const next = [...current]
        next[index] = animated
        return next
      })

      if (complete) {
        window.clearInterval(timer)
        typingTimersRef.current.delete(incoming.id)
        assistantTargetsRef.current.delete(incoming.id)
        renderedLengthsRef.current.delete(incoming.id)
        setSending(false)
      }
    }, interval)
    typingTimersRef.current.set(incoming.id, timer)
  }, [])

  const applySocketEvent = useCallback((event: SocketEvent) => {
    if (event.type === 'message' && event.message) {
      const incoming = event.message
      if (incoming.role === 'assistant') {
        animateAssistant(incoming)
      } else {
        setMessages((current) => upsertMessage(current, incoming))
      }
    }
    if (event.type === 'thread_updated' && event.thread) {
      const updated = event.thread
      setThreads((current) => [updated, ...current.filter((thread) => thread.id !== updated.id)])
    }
    if (event.type === 'error') {
      clearTypingAnimations()
      setMessages((current) => current
        .filter((message) => !(message.streaming && message.content === ''))
        .map((message) => message.streaming ? { ...message, streaming: false } : message))
      setNotice(event.error ?? 'The uplink reported an error.')
      setSending(false)
    }
  }, [animateAssistant, clearTypingAnimations])

  const connectSocket = useCallback((threadId: string) => {
    const existing = socketRef.current
    if (socketThreadRef.current === threadId && existing?.readyState === WebSocket.OPEN) {
      return Promise.resolve(existing)
    }
    existing?.close()
    setConnected(false)
    const wsBase = API_BASE.replace(/^http/, 'ws')
    const socket = new WebSocket(`${wsBase}/ws?thread_id=${encodeURIComponent(threadId)}`)
    socketRef.current = socket
    socketThreadRef.current = threadId
    socket.onmessage = (raw) => applySocketEvent(JSON.parse(raw.data) as SocketEvent)
    socket.onclose = () => {
      if (socketRef.current === socket) setConnected(false)
    }
    socket.onerror = () => setNotice('Realtime uplink unavailable. Is the Go backend running?')
    return new Promise<WebSocket>((resolve, reject) => {
      socket.onopen = () => {
        setConnected(true)
        setNotice(null)
        resolve(socket)
      }
      socket.addEventListener('error', () => reject(new Error('WebSocket connection failed')), { once: true })
    })
  }, [applySocketEvent])

  const loadThreads = useCallback(async () => {
    try {
      const [data, runtimeData] = await Promise.all([
	        request<{ threads: Thread[] }>('/api/threads'),
	        request<Runtime>('/api/runtime'),
	      ])
      setThreads(data.threads)
	    setRuntime(runtimeData)
    } catch (error) {
      setNotice(error instanceof Error ? error.message : 'Could not load conversations.')
    } finally {
      setLoading(false)
    }
  }, [request])

  useEffect(() => {
    void loadThreads()
    return () => {
      socketRef.current?.close()
      clearTypingAnimations()
    }
  }, [clearTypingAnimations, loadThreads])

  useEffect(() => {
    if (!activeId) {
      setMessages([])
      socketRef.current?.close()
      socketThreadRef.current = null
      setConnected(false)
      return
    }
    let cancelled = false
    setLoading(true)
    request<{ messages: Message[] }>(`/api/threads/${activeId}/messages`)
      .then((data) => {
        if (!cancelled) {
          setMessages((current) => mergeThreadMessages(data.messages, current, activeId))
        }
      })
      .catch((error) => { if (!cancelled) setNotice(error.message) })
      .finally(() => { if (!cancelled) setLoading(false) })
    void connectSocket(activeId).catch(() => undefined)
    return () => { cancelled = true }
  }, [activeId, connectSocket, request])

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  const newChat = () => {
    clearTypingAnimations()
    setActiveId(null)
    setDraft('')
    setSidebarOpen(false)
    setNotice(null)
    setTimeout(() => textareaRef.current?.focus(), 0)
  }

  const selectThread = (id: string) => {
    clearTypingAnimations()
    setActiveId(id)
    setSidebarOpen(false)
    setNotice(null)
  }

  const deleteThread = async (id: string) => {
    try {
      await request<{ ok: boolean }>(`/api/threads/${id}`, { method: 'DELETE' })
      setThreads((current) => current.filter((thread) => thread.id !== id))
      if (activeId === id) newChat()
    } catch (error) {
      setNotice(error instanceof Error ? error.message : 'Could not delete the thread.')
    }
  }

  const sendMessage = async (content = draft) => {
    const trimmed = content.trim()
    if (!trimmed || sending) return
    setSending(true)
    setDraft('')
    setNotice(null)
    try {
      let threadId = activeId
      if (!threadId) {
        const data = await request<{ thread: Thread }>('/api/threads', {
          method: 'POST',
          body: JSON.stringify({ title: 'New conversation' }),
        })
        threadId = data.thread.id
        setThreads((current) => [data.thread, ...current])
        setMessages([])
      }
      const socket = await connectSocket(threadId)
      socket.send(JSON.stringify({ type: 'user_message', content: trimmed }))
      setActiveId(threadId)
    } catch (error) {
      setDraft(trimmed)
      setSending(false)
      setNotice(error instanceof Error ? error.message : 'Message failed to send.')
    }
  }

  const submit = (event: FormEvent) => {
    event.preventDefault()
    void sendMessage()
  }

  const handleComposerKey = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault()
      void sendMessage()
    }
  }

  const activeThread = threads.find((thread) => thread.id === activeId)

  return (
    <div className="app-shell">
      <div className={`sidebar-scrim ${sidebarOpen ? 'visible' : ''}`} onClick={() => setSidebarOpen(false)} />
      <aside className={`sidebar ${sidebarOpen ? 'open' : ''}`} aria-label="Conversation history">
        <div className="brand-row">
          <div className="brand-mark">386</div>
          <div>
            <div className="brand-name">386GPT</div>
            <div className="brand-version">TERMINAL AI v0.1</div>
          </div>
          <button className="icon-button sidebar-close" onClick={() => setSidebarOpen(false)} aria-label="Close sidebar"><Icon name="close" /></button>
        </div>

        <button className="new-chat" onClick={newChat}><Icon name="plus" /> NEW CHAT</button>

        <div className="thread-section-label">// CONVERSATIONS</div>
        <nav className="thread-list">
          {threads.map((thread) => (
            <button key={thread.id} className={`thread-item ${thread.id === activeId ? 'active' : ''}`} onClick={() => selectThread(thread.id)}>
              <span className="thread-cursor">{thread.id === activeId ? '>' : ' '}</span>
              <span className="thread-copy">
                <span className="thread-title">{thread.title}</span>
                <span className="thread-meta">{thread.messageCount} MSG · {new Date(thread.updatedAt).toLocaleDateString([], { month: 'short', day: 'numeric' }).toUpperCase()}</span>
              </span>
              <span className="delete-thread" role="button" tabIndex={0} aria-label={`Delete ${thread.title}`} onClick={(event) => { event.stopPropagation(); void deleteThread(thread.id) }} onKeyDown={(event) => { if (event.key === 'Enter') { event.stopPropagation(); void deleteThread(thread.id) } }}><Icon name="trash" /></span>
            </button>
          ))}
          {!threads.length && !loading && <div className="empty-history">NO SAVED THREADS<br />CREATE ONE TO BEGIN_</div>}
        </nav>

        <div className="sidebar-footer">
          <div><span className={`status-dot ${connected ? 'online' : ''}`} /> {connected ? 'UPLINK ACTIVE' : 'STANDBY'}</div>
          <div>SQLITE // LOCAL</div>
        </div>
      </aside>

      <main className="chat-panel">
        <header className="topbar">
          <button className="icon-button menu-button" onClick={() => setSidebarOpen(true)} aria-label="Open sidebar"><Icon name="menu" /></button>
          <div className="title-block">
            <span className="prompt-mark">C:\GPT&gt;</span>
            <span>{activeThread?.title ?? 'NEW_SESSION'}</span>
            <span className="terminal-cursor" />
          </div>
          <div className="model-pill" title={`${runtime.provider} / ${runtime.model}`}><span className="status-dot online" /> {runtime.model.toUpperCase()} · {runtime.provider.toUpperCase()}</div>
        </header>

        <section className={`conversation ${messages.length ? '' : 'welcome-mode'}`}>
          {notice && <div className="notice"><strong>ERROR:</strong> {notice}</div>}
          {!messages.length && !loading ? (
            <div className="welcome">
              <div className="ascii-logo" aria-label="386 GPT">{` ██████╗  █████╗  ██████╗\n ╚════██╗██╔══██╗██╔════╝\n  █████╔╝╚█████╔╝███████╗\n  ╚═══██╗██╔══██╗██╔═══██╗\n ██████╔╝╚█████╔╝╚██████╔╝\n ╚═════╝  ╚════╝  ╚═════╝`}</div>
              <p className="welcome-kicker">[ GENERATIVE PRE-TRAINED TERMINAL ]</p>
              <h1>WHAT CAN I HELP YOU BUILD?</h1>
              <p className="welcome-copy">Your local conversation archive is ready. Ask a question, explore an idea, or ship some code.</p>
              <div className="quick-grid">
                {quickPrompts.map(([label, prompt]) => (
                  <button key={label} onClick={() => void sendMessage(prompt)}>
                    <span>{label}</span>
                    <p>{prompt}</p>
                    <b>↗</b>
                  </button>
                ))}
              </div>
            </div>
          ) : (
            <div className="message-list">
              {messages.map((message) => (
                <article key={message.id} className={`message ${message.role}`}>
                  <div className="message-label">{message.role === 'user' ? 'YOU' : '386GPT'} <span>{new Date(message.createdAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span></div>
                  <div className="message-body">
                    {message.role === 'assistant' && <div className="assistant-glyph"><Icon name="spark" /></div>}
                    <p>{message.content}{message.streaming && <span className="stream-cursor">█</span>}</p>
                  </div>
                  {!message.streaming && <button className="copy-button" onClick={() => void navigator.clipboard.writeText(message.content)} aria-label="Copy message"><Icon name="copy" /> COPY</button>}
                </article>
              ))}
              <div ref={messagesEndRef} />
            </div>
          )}
        </section>

        <footer className="composer-wrap">
          <form className="composer" onSubmit={submit}>
            <span className="composer-prompt">&gt;</span>
            <textarea ref={textareaRef} value={draft} onChange={(event) => setDraft(event.target.value)} onKeyDown={handleComposerKey} placeholder="ENTER MESSAGE..." rows={1} aria-label="Message" />
            <button type="submit" className="send-button" disabled={!draft.trim() || sending} aria-label="Send message">{sending ? '...' : <Icon name="send" />}</button>
          </form>
          <div className="composer-meta"><span>ENTER TO SEND · SHIFT+ENTER FOR NEW LINE</span><span>MESSAGES SAVED LOCALLY</span></div>
        </footer>
      </main>
    </div>
  )
}

export default App
