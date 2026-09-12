// Thin typed wrapper over the JSON API.

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
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

async function get<T>(path: string): Promise<T> {
  const res = await fetch(path, { headers: { Accept: 'application/json' } })
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
}
