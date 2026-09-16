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
  /** The note's inline #tags, folded to lower case. */
  tags?: string[]
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
  trusted: boolean
  content_hash: string
  /** The caller's role in the note's space; a viewer reads only. */
  role: Role
  html?: string
  markdown?: string
  source?: string
}

export type Role = 'owner' | 'editor' | 'viewer'

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
  regex_version?: string
  accounts: boolean
  daily: { pattern: string; template: string }
  git?: { available: boolean; commits: number; last_commit: string; errors: number }
  sync?: { loaded: number; dirty: number; writebacks: number; readins: number; watching: boolean }
  trash?: { retention_days: number }
}

export interface Session {
  id: string
  label: string
  created_at: string
  last_used_at: string
  expires_at: string
  revoked_at?: string | null
  current: boolean
}

export interface Account {
  id: string
  username: string
  is_owner: boolean
  created_at: string
}

export interface SpaceInfo {
  name: string
  label: string
  notes: number
}

export interface SpaceMember {
  /** The reference as written in .space.yml: a username or an id. */
  user: string
  role: Role
  id?: string
  username?: string
}

export interface SpaceDetail {
  name: string
  label: string
  role: Role
  /** Present for space owners only. */
  members?: SpaceMember[]
}

export interface AgentKey {
  id: string
  label: string
  spaces: string[]
  can_write: boolean
  created_at: string
  last_used_at: string
  revoked_at?: string | null
}

export interface TrashEntry {
  id: string
  space: string
  path: string
  title: string
  kind: 'md' | 'html'
  created: string
  deleted_at: string
  trash_path?: string
  has_file: boolean
  has_sidecar: boolean
  untracked?: boolean
}

export interface TagCount {
  tag: string
  count: number
}

export interface DirMoveResult {
  path: string
  moved: number
  total: number
  rewritten: number
  broken: number
}

export interface MoveResult {
  note: Note
  rewritten: number
  broken: number
}

export interface RestoreResult {
  ok: boolean
  path: string
  conflict: boolean
  note?: Note
  deferred: boolean
}

export interface Upload {
  path: string
  name: string
  size: number
  url: string
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
  deleteNote: (id: string) =>
    post<{ ok: boolean; trash_path: string }>(`/api/notes/${encodeURIComponent(id)}`, {}, 'DELETE'),
  tags: () => get<{ tags: TagCount[] }>('/api/tags'),
  tagNotes: (tag: string) => get<{ tag: string; notes: Note[] }>(`/api/tags/${encodeURIComponent(tag)}`),
  createDir: (path: string) => post<{ path: string }>('/api/dirs', { path }),
  moveDir: (path: string, to: string) => post<DirMoveResult>('/api/dirs/move', { path, to }),
  deleteDir: (path: string) =>
    post<{ ok: boolean; deleted: number; removed: boolean }>(`/api/dirs?path=${encodeURIComponent(path)}`, {}, 'DELETE'),
  trash: () => get<{ entries: TrashEntry[] }>('/api/trash'),
  restoreTrash: (id: string) =>
    post<RestoreResult>(`/api/trash/${encodeURIComponent(id)}/restore`, {}),
  destroyTrash: (id: string) => post<{ ok: boolean }>(`/api/trash/${encodeURIComponent(id)}`, {}, 'DELETE'),
  emptyTrash: () => post<{ ok: boolean; destroyed: number }>('/api/trash/empty', {}),
  history: (id: string) =>
    get<{ entries: HistoryEntry[] }>(`/api/notes/${encodeURIComponent(id)}/history`),
  historyDiff: (id: string, from: string, to: string) =>
    get<{ diff: string }>(
      `/api/notes/${encodeURIComponent(id)}/history/diff?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
    ),
  restoreNote: (id: string, revision: string, path: string) =>
    post<{ ok: boolean }>(`/api/notes/${encodeURIComponent(id)}/history/restore`, { revision, path }),
  gitSnapshot: () => post<{ ok: boolean; commits: number }>('/api/git/snapshot', {}),
  spaces: () => get<{ spaces: SpaceInfo[] }>('/api/spaces'),
  space: (name: string) => get<SpaceDetail>(`/api/spaces/${encodeURIComponent(name)}`),
  createSpace: (name: string) => post<{ name: string }>('/api/spaces', { name }),
  updateSpace: (space: string, name: string, members: Array<{ user: string; role: Role }>) =>
    post<{ ok: boolean }>(`/api/spaces/${encodeURIComponent(space)}`, { name, members }, 'PATCH'),
  deleteSpace: (space: string) => post<{ ok: boolean }>(`/api/spaces/${encodeURIComponent(space)}`, {}, 'DELETE'),
  sessions: () => get<{ sessions: Session[] }>('/api/auth/sessions'),
  revokeSession: (id: string) => post<{ ok: boolean }>(`/api/auth/sessions/${encodeURIComponent(id)}`, {}, 'DELETE'),
  users: () => get<{ users: Account[] }>('/api/users'),
  createUser: (username: string, password: string) =>
    post<{ id: string; username: string; is_owner: boolean }>('/api/users', { username, password }),
  deleteUser: (id: string) => post<{ ok: boolean }>(`/api/users/${encodeURIComponent(id)}`, {}, 'DELETE'),
  setPassword: (id: string, password: string) =>
    post<{ ok: boolean }>(`/api/users/${encodeURIComponent(id)}/password`, { password }),
  agents: () => get<{ agents: AgentKey[] }>('/api/agents'),
  createAgent: (label: string, spaces: string[], canWrite: boolean) =>
    post<AgentKey & { token: string }>('/api/agents', { label, spaces, can_write: canWrite }),
  revokeAgent: (id: string) => post<{ ok: boolean }>(`/api/agents/${encodeURIComponent(id)}`, {}, 'DELETE'),
  daily: (space: string, date: string) =>
    post<{ id: string; path: string; created: boolean }>('/api/notes/daily', { space, date }),
  render: (markdown: string) => post<{ html: string }>('/api/render', { markdown }),
  noteView: (id: string) =>
    get<{ url: string; expires_at: string }>(`/api/notes/${encodeURIComponent(id)}/view`),
  saveSource: (id: string, source: string, baseHash: string) =>
    post<{ ok: boolean; path: string; hash: string; conflict_copy?: string }>(
      `/api/notes/${encodeURIComponent(id)}/source`,
      { source, base_hash: baseHash },
      'PUT',
    ),
  setTrusted: (id: string, trusted: boolean) =>
    post<{ ok: boolean; trusted: boolean }>(`/api/notes/${encodeURIComponent(id)}/trust`, { trusted }),
  upload: (path: string, file: Blob) => upload(path, file),
  exportNote: (id: string) => download(`/api/notes/${encodeURIComponent(id)}/export.html`),
  exportSite: (space: string, path?: string) => download(`/api/spaces/${encodeURIComponent(space)}/export/site.zip` + scope(path)),
  exportTree: (space: string, path?: string) => download(`/api/spaces/${encodeURIComponent(space)}/export/notes.zip` + scope(path)),
}

function scope(path?: string): string {
  return path ? `?path=${encodeURIComponent(path)}` : ''
}

/** Fetches an export as a blob, carrying the account token a plain navigation cannot. */
async function download(path: string): Promise<{ blob: Blob; name: string }> {
  const res = await authFetch(path, { headers: { Accept: 'application/octet-stream' } })
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    throw new ApiError(res.status, (body as { error?: string }).error ?? `export failed (${res.status})`)
  }
  const name = contentDispositionName(res.headers.get('Content-Disposition')) ?? 'export'
  return { blob: await res.blob(), name }
}

/** Pulls the filename out of a Content-Disposition header. */
function contentDispositionName(header: string | null): string | null {
  if (!header) return null
  const m = /filename="([^"]+)"/.exec(header)
  return m && m[1] ? m[1] : null
}

/** Saves a downloaded blob the way a browser saves a clicked link. */
export function saveBlob(blob: Blob, name: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.append(a)
  a.click()
  a.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 10_000)
}

/** PUT one file under an _assets directory; the server picks a free name. */
async function upload(path: string, file: Blob): Promise<Upload> {
  const res = await authFetch('/api/files/' + path.split('/').map(encodeURIComponent).join('/'), {
    method: 'PUT',
    headers: { 'Content-Type': file.type || 'application/octet-stream', Accept: 'application/json' },
    body: file,
  })
  const body = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new ApiError(res.status, (body as { error?: string }).error ?? `upload failed (${res.status})`)
  }
  return body as Upload
}

/** Path helpers shared by the editor, the tree, and the palette. */
export function join(base: string, rel: string): string {
  const parts = base ? base.split('/') : []
  for (const seg of rel.split('/')) {
    if (seg === '' || seg === '.') continue
    if (seg === '..') parts.pop()
    else parts.push(seg)
  }
  return parts.join('/')
}

export function dirOf(path: string): string {
  const i = path.lastIndexOf('/')
  return i < 0 ? '' : path.slice(0, i)
}

export function baseOf(path: string): string {
  const i = path.lastIndexOf('/')
  return i < 0 ? path : path.slice(i + 1)
}

export function spaceOf(path: string): string {
  const i = path.indexOf('/')
  return i < 0 ? '' : path.slice(0, i)
}
