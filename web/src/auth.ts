// Accounts on the client: one access token held in memory, refreshed
// through the HttpOnly cookie when it expires, and the setup and
// sign-in screens the app boots through. No token ever touches
// localStorage.

export interface User {
  id: string
  username: string
  is_owner: boolean
}

let accessToken: string | null = null
let currentUser: User | null = null
let refreshing: Promise<boolean> | null = null

export function user(): User | null {
  return currentUser
}

export function token(): string | null {
  return accessToken
}

/** URL for a file under _assets, with the access token when accounts are on. */
export function assetURL(rel: string): string {
  const path = '/api/files/' + rel.split('/').map(encodeURIComponent).join('/')
  return accessToken ? `${path}?token=${encodeURIComponent(accessToken)}` : path
}

export function wsURL(): string {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  const t = accessToken ? `?token=${encodeURIComponent(accessToken)}` : ''
  return `${proto}://${location.host}/ws${t}`
}

export class AuthError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

interface Parsed {
  ok: boolean
  status: number
  body: Record<string, unknown>
}

async function parse(res: Response): Promise<Parsed> {
  const body = await res.json().catch(() => ({}) as Record<string, unknown>)
  return { ok: res.ok, status: res.status, body }
}

function errMsg(body: Record<string, unknown>, fallback: string): string {
  const e = body['error']
  return typeof e === 'string' ? e : fallback
}

/** Try to mint an access token from the refresh cookie. */
export async function tryRefresh(): Promise<boolean> {
  const res = await fetch('/api/auth/refresh', { method: 'POST', headers: { Accept: 'application/json' } })
  const { ok, status, body } = await parse(res)
  if (!ok) {
    if (status === 501) return true // server without accounts: everything is open
    return false
  }
  const tokens = body['tokens'] as { access_token?: string } | undefined
  if (!tokens?.access_token) return false
  accessToken = tokens.access_token
  await loadUser()
  return true
}

async function loadUser(): Promise<void> {
  const res = await fetch('/api/auth/state', { headers: authHeaders() })
  const { body } = await parse(res)
  const u = body['user'] as User | undefined
  currentUser = u ?? null
}

function authHeaders(): Record<string, string> {
  return accessToken ? { Authorization: `Bearer ${accessToken}` } : {}
}

/** fetch with the bearer token; one silent refresh-and-retry on 401. */
export async function authFetch(path: string, init?: RequestInit): Promise<Response> {
  const res = await fetch(path, { ...init, headers: { ...authHeaders(), ...(init?.headers ?? {}) } })
  if (res.status !== 401 || path.startsWith('/api/auth/')) return res
  const ok = await refreshOnce()
  if (!ok) return res
  return fetch(path, { ...init, headers: { ...authHeaders(), ...(init?.headers ?? {}) } })
}

function refreshOnce(): Promise<boolean> {
  refreshing ??= tryRefresh().finally(() => {
    refreshing = null
  })
  return refreshing
}

export async function authState(): Promise<{ setupRequired: boolean; open: boolean }> {
  const res = await fetch('/api/auth/state', { headers: { Accept: 'application/json' } })
  const { status, body } = await parse(res)
  const setup = body['setup_required'] === true
  const open = status === 501
  return { setupRequired: setup, open }
}

export async function setup(username: string, password: string): Promise<void> {
  const res = await fetch('/api/auth/setup', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password, label: 'first run' }),
  })
  const { ok, status, body } = await parse(res)
  if (!ok) throw new AuthError(status, errMsg(body, 'setup failed'))
  const tokens = body['tokens'] as { access_token?: string } | undefined
  accessToken = tokens?.access_token ?? null
  const u = body['user'] as User | undefined
  currentUser = u ?? null
}

export async function login(username: string, password: string): Promise<void> {
  const res = await fetch('/api/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password, label: navigator.userAgent }),
  })
  const { ok, status, body } = await parse(res)
  if (!ok) throw new AuthError(status, errMsg(body, 'sign in failed'))
  const tokens = body['tokens'] as { access_token?: string } | undefined
  accessToken = tokens?.access_token ?? null
  const u = body['user'] as User | undefined
  currentUser = u ?? null
}

export async function logout(): Promise<void> {
  await fetch('/api/auth/logout', { method: 'POST', headers: { Accept: 'application/json' } })
  accessToken = null
  currentUser = null
}

/** True when the last 401 means "sign in again", not "refreshing". */
export function signedOut(): boolean {
  return accessToken === null
}
