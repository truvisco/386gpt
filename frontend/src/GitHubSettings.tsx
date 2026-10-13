import { useEffect, useRef, useState } from 'react'
import { agentRequest } from './useAgent'

type GitHubState = { state: string; runtime?: string; login?: string; user_code?: string; verification_uri?: string; error?: string }

export function GitHubSettings({ thread, onClose }: { thread: string | null; onClose: () => void }) {
  const dialog = useRef<HTMLElement>(null)
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null
    dialog.current?.focus()
    return () => previous?.focus()
  }, [])
  const [status, setStatus] = useState<GitHubState>({ state: 'loading' })
  const [token, setToken] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const query = thread ? `?thread_id=${encodeURIComponent(thread)}` : ''
  useEffect(() => {
    let stopped = false, loading = false
    const refresh = async () => {
      if (loading) return
      loading = true
      try {
        const next = await agentRequest<GitHubState>(`/api/settings/github${query}`)
        if (!stopped) setStatus(next)
      } catch (e) { if (!stopped) setError(e instanceof Error ? e.message : 'Could not load GitHub settings.') }
      finally { loading = false }
    }
    void refresh()
    const timer = setInterval(() => { void refresh() }, 5000)
    return () => { stopped = true; clearInterval(timer) }
  }, [query])
  const action = async (name: string) => {
    setBusy(true); setError('')
    const body = name === 'token' ? { token } : {}
    setToken('')
    try { setStatus(await agentRequest<GitHubState>(`/api/settings/github/${name}${query}`, { method: 'POST', body: JSON.stringify(body) })) }
    catch (e) { setError(e instanceof Error ? e.message : 'GitHub sign-in failed.') }
    finally { setBusy(false) }
  }
  return <div className="settings-overlay">
    <section ref={dialog} tabIndex={-1} onKeyDown={e => {
      if (e.key === 'Escape') { e.preventDefault(); onClose() }
      if (e.key === 'Tab') {
        const items = [...(dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled), a[href], input:not(:disabled)') ?? [])]
        const first = items[0], last = items.at(-1)
        if (e.shiftKey && (document.activeElement === first || document.activeElement === dialog.current)) { e.preventDefault(); last?.focus() }
        else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first?.focus() }
      }
    }} className="github-settings" role="dialog" aria-modal="true" aria-labelledby="github-settings-title">
      <header><h2 id="github-settings-title">Settings · GitHub</h2><button type="button" onClick={onClose} aria-label="Close settings">Close</button></header>
      <p>Connect GitHub for {thread ? 'this conversation’s workspace' : 'your default workspace'}{status.runtime ? ` (${status.runtime})` : ''}. Credentials stay in your account’s container and persist across logins.</p>
      <p>Git and GitHub CLI use HTTPS through the workspace proxy.</p>
      {status.state === 'connected' ? <p>Connected as <strong>{status.login}</strong>.</p> : <p>Status: {status.state}</p>}
      {(error || status.error) && <p role="alert">{error || status.error}</p>}
      {status.state === 'pending' && <div role="status">
        {status.user_code ? <><p>Enter this code on GitHub: <strong className="device-code">{status.user_code}</strong></p><a href="https://github.com/login/device" target="_blank" rel="noopener noreferrer">Open GitHub device authorization</a><p>Waiting for authorization… You can close settings and return later.</p></> : <p>Requesting a device code…</p>}
      </div>}
      <button type="button" disabled={busy || status.state === 'pending'} onClick={() => void action('device')}>Sign in with GitHub</button>
      <form onSubmit={e => { e.preventDefault(); void action('token') }}>
        <label htmlFor="github-token">Or use a personal access token</label>
        <input id="github-token" type="password" autoComplete="off" spellCheck={false} value={token} onChange={e => setToken(e.target.value)} placeholder="GitHub token" />
        <p>Grant access only to the repositories and operations you need. Organization policies may require additional approval.</p>
        <button type="submit" disabled={busy || !token.trim()}>Save GitHub token</button>
      </form>
      {(status.state === 'connected' || status.state === 'pending' || status.state === 'error') && <button type="button" disabled={busy} onClick={() => void action('disconnect')}>{status.state === 'pending' ? 'Cancel sign-in' : 'Disconnect GitHub'}</button>}
      <p>Disconnect removes credentials from this workspace. Revoke the authorization on GitHub to invalidate it everywhere.</p>
    </section>
  </div>
}
