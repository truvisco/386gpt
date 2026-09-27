import { useCallback, useEffect, useRef, useState } from 'react'

export const API_BASE = (import.meta.env.VITE_API_URL ?? 'http://localhost:8080').replace(/\/$/, '')
export type Run = { id: string; threadId: string; status: string; output?: string; error?: string; pendingSteer?: unknown; approval?: { request_id: string; command?: string; description?: string; choices: string[] } }
export type Activity = { seq: number; kind: string; runId: string; createdAt: string; data: Record<string, unknown> }
export type Skill = { name: string; description: string }
export type Runtime = { id?: string; hostname?: string; os?: string; home?: string; cwd?: string; provider: string; model: string; healthy?: boolean; error?: string }
export const isActive = (run: Run | null) => !!run && ['submitting', 'queued', 'running', 'waiting_for_approval', 'stopping'].includes(run.status)

export async function agentRequest<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, { ...options, headers: { 'Content-Type': 'application/json', ...options?.headers } })
  const data = await response.json()
  if (!response.ok) throw new Error(data.error ?? `HTTP ${response.status}`)
  return data as T
}

export function useAgent(thread: string | null) {
  const [run, setRun] = useState<Run | null>(null)
  const [events, setEvents] = useState<Activity[]>([])
  const [runtime, setRuntime] = useState<Runtime>({ provider: 'Hermes', model: 'Connecting' })
  const [skills, setSkills] = useState<Skill[]>([])
  const [error, setError] = useState('')
  const refreshRef = useRef<() => void>(() => {})
  useEffect(() => {
    let cancelled = false, busy = false, cursor = 0, initial = true, runtimeChecked = 0
    setRun(null); setEvents([]); setSkills([]); setError('')
    const query = thread ? `?thread_id=${encodeURIComponent(thread)}` : ''
    setRuntime({ provider: 'Hermes', model: 'Connecting' })
    const refreshRuntime = async () => {
      if (Date.now() - runtimeChecked < 10000) return
      runtimeChecked = Date.now()
      try {
        const data = await agentRequest<Runtime>(`/api/runtime${query}`)
        if (!cancelled) setRuntime(data)
      } catch (e) { if (!cancelled) setRuntime(current => ({ ...current, healthy: false, error: e instanceof Error ? e.message : 'Runtime unavailable' })) }
    }
    void refreshRuntime()
    const refresh = async () => {
      if (!thread || busy) return
      busy = true
      try {
        const data = await agentRequest<{ runs: Run[] }>(`/api/threads/${thread}/runs`)
        if (cancelled) return
        setRun(data.runs[0] ?? null)
        let more = true
        while (more && !cancelled) {
          const page = await agentRequest<{ events: Activity[]; hasMore: boolean }>(`/api/threads/${thread}/activity?after=${cursor}${initial ? '&sync=1' : ''}`)
          if (cancelled) return
          initial = false
          if (page.events.length) {
            cursor = page.events[page.events.length - 1].seq
            setEvents(current => [...current, ...page.events.filter(e => !current.some(old => old.seq === e.seq))])
          }
          more = page.hasMore
        }
        setError('')
      } catch (e) { if (!cancelled) setError(e instanceof Error ? e.message : 'Agent connection interrupted') }
      finally { busy = false }
    }
    refreshRef.current = () => { void refresh() }
    void refresh()
    const timer = window.setInterval(() => { void refreshRuntime(); void refresh() }, 2000)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [thread])
  const loadSkills = useCallback(async () => {
    try {
      const data = await agentRequest<{ data: Skill[] }>(`/api/skills${thread ? `?thread_id=${encodeURIComponent(thread)}` : ''}`)
      setSkills(data.data); setError('')
    } catch (e) { setError(e instanceof Error ? e.message : 'Skills unavailable') }
  }, [thread])
  return { run, events, runtime, skills, error, loadSkills, refresh: () => refreshRef.current() }
}
