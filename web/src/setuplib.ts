// Small helpers for the fresh-server setup screen.

/** The host a backup address names: `user@host:path`, `ssh://user@host[:port]/path`,
 * or the address itself when it is neither. */
export function hostOf(url: string): string {
  const u = url.trim()
  const m = /^ssh:\/\/(?:[^@/]*@)?(\[[^\]]+\]|[^:/]+)/.exec(u) ?? /^(?:[^@/:]+@)?([^:/]+):/.exec(u)
  return m?.[1] ?? u
}

/** An error's explained first line, and git's own lines under it. */
export function splitError(err: string): { head: string; detail: string } {
  const [head = '', ...rest] = err.trim().split('\n')
  return { head, detail: rest.join('\n').trim() }
}
