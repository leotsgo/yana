// Thin typed wrapper over the JSON API. Every call carries the account's
// access token and refreshes it once when it has expired.

import { authFetch } from './auth'

export interface TreeNode {
  type: 'dir' | 'note'
  name: string
  path: string
  id?: string
  title?: string
  kind?: 'md' | 'html'
  order?: number
  children?: TreeNode[]
}

export interface SpaceTree {
  name: string
  notes: number
  children: TreeNode[]
}

export interface LinkInfo {
  raw_target: string
  to_id?: string
  resolved: boolean
}

export interface Backlink {
  note: Note
  raw_target: string
  context: string
}

export interface UnresolvedLink {
  note: Note
  raw_target: string
}

export interface Note {
  id: string
  space: string
  path: string
  title: string
  preview: string
  kind: 'md' | 'html'
  size: number
  mtime: string
  created: string
  tags: string[]
  base: string
  links: LinkInfo[]
  html?: string
  markdown?: string
  source?: string
}

export interface SearchHit {
  note: Note
  snippet: string
}

export interface RegexHit {
  path: string
  line: number
  text: string
  id?: string
  title?: string
}

export interface Status {
  version: string
  ready: boolean
  notes: number
  assets: number
  last_scan: string
  regex_search: boolean
  git?: { available: boolean; commits: number; last_commit: string; errors: number }
}

export interface HistoryEntry {
  hash: string
  name: string
  email: string
  date: string
  subject: string
  path: string
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

async function get<T>(path: string): Promise<T> {
  const res = await authFetch(path, { headers: { Accept: 'application/json' } })
  const body = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new ApiError(res.status, (body as { error?: string }).error ?? `request failed (${res.status})`)
  }
  return body as T
}

async function post<T>(path: string, payload: unknown, method = 'POST'): Promise<T> {
  const res = await authFetch(path, {
    method,
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify(payload),
  })
  const body = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new ApiError(res.status, (body as { error?: string }).error ?? `request failed (${res.status})`)
  }
  return body as T
}

export const api = {
  tree: (space?: string) =>
    get<{ spaces: SpaceTree[] }>('/api/tree' + (space ? `?space=${encodeURIComponent(space)}` : '')),
  note: (id: string) => get<Note>(`/api/notes/${encodeURIComponent(id)}`),
  search: (q: string, space?: string) =>
    get<{ mode: 'fts'; hits: SearchHit[] }>(
      `/api/search?q=${encodeURIComponent(q)}` + (space ? `&space=${encodeURIComponent(space)}` : ''),
    ),
  regex: (raw: string, space?: string) =>
    get<{ mode: 'regex'; hits: RegexHit[] }>(
      `/api/search?raw=${encodeURIComponent(raw)}` + (space ? `&space=${encodeURIComponent(space)}` : ''),
    ),
  status: () => get<Status>('/api/status'),
  backlinks: (id: string) =>
    get<{ backlinks: Backlink[] }>(`/api/notes/${encodeURIComponent(id)}/backlinks`),
  unresolved: (space?: string) =>
    get<{ unresolved: UnresolvedLink[] }>(
      '/api/links/unresolved' + (space ? `?space=${encodeURIComponent(space)}` : ''),
    ),
  createNote: (path: string, content?: string) =>
    post<{ id: string; path: string }>('/api/notes', { path, content }),
  moveNote: (id: string, path: string) =>
    post<{ note: Note; rewritten: number; broken: number }>(`/api/notes/${encodeURIComponent(id)}/move`, { path }),
  history: (id: string) =>
    get<{ entries: HistoryEntry[] }>(`/api/notes/${encodeURIComponent(id)}/history`),
  historyDiff: (id: string, from: string, to: string) =>
    get<{ diff: string }>(
      `/api/notes/${encodeURIComponent(id)}/history/diff?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
    ),
  restoreNote: (id: string, revision: string, path: string) =>
    post<{ ok: boolean }>(`/api/notes/${encodeURIComponent(id)}/history/restore`, { revision, path }),
  gitSnapshot: () => post<{ ok: boolean; commits: number }>('/api/git/snapshot', {}),
  spaces: () => get<{ spaces: { name: string; label: string; notes: number }[] }>('/api/spaces'),
  createSpace: (name: string) => post<{ name: string }>('/api/spaces', { name }),
}
