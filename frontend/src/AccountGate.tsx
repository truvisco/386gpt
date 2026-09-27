import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'

export type Account = { id: string; email: string; service: boolean }
const API_BASE = (import.meta.env.VITE_API_URL ?? window.location.origin).replace(/\/$/, '')

export function AccountGate({ children }: { children: (account: Account) => ReactNode }) {
  const [account, setAccount] = useState<Account | null>(null)
  const [message, setMessage] = useState('Loading your account…')
  const [loginRequired, setLoginRequired] = useState(false)
  useEffect(() => {
    let cancelled = false
    let timer: ReturnType<typeof setTimeout>
    const controller = new AbortController()
    async function check() {
      try {
        const response = await fetch(`${API_BASE}/api/account`, { signal: controller.signal })
        if (response.status === 401 || response.redirected) {
          if (!cancelled) { setLoginRequired(true); setMessage('Sign in with Google to continue.') }
          return
        }
        if (!response.ok) throw new Error('Account unavailable')
        const data = await response.json() as { account: Account; environment: string }
        if (cancelled) return
        if (data.environment === 'ready') { setAccount(data.account); return }
        setMessage(data.environment === 'retrying' ? 'Your workspace is temporarily unavailable. Retrying…' : 'Preparing your private workspace…')
      } catch {
        if (cancelled) return
        setMessage('Unable to reach your workspace. Retrying…')
      }
      if (!cancelled) timer = setTimeout(check, 2000)
    }
    void check()
    return () => { cancelled = true; clearTimeout(timer); controller.abort() }
  }, [])
  if (account) return children(account)
  return <main className="account-gate"><h1>386GPT</h1><p role="status">{message}</p>{loginRequired ? <a className="new-chat" href="/api/auth/google">Sign in with Google</a> : <button type="button" onClick={async () => { await fetch(`${API_BASE}/api/auth/logout`, { method: 'POST' }); window.location.assign('/') }}>Sign out</button>}</main>
}
