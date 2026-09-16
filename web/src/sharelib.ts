// Shared by the share page and the outbox: turning what another app
// handed over (a title, some text, a URL) into a markdown line, and
// appending it to a note's document. Appending opens a short realtime
// session for the note so the line lands through the CRDT and reaches
// every open client; offline it lands in the note's local document and
// merges when the server returns.

import { SyncClient } from './sync'

/** The local date as YYYY-MM-DD, the way the daily note endpoint wants it. */
export function today(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

function escapeLabel(s: string): string {
  return s.replace(/\\/g, '\\\\').replace(/\[/g, '\\[').replace(/\]/g, '\\]')
}

/** The markdown for one shared item: a link when a URL came along, the
 * text quoted under it when the text said more than its own name. */
export function composeShareBlock(rawTitle: string, rawText: string, rawUrl: string): string {
  const title = rawTitle.trim().replace(/\s+/g, ' ')
  const url = rawUrl.trim().replace(/[)\s]/g, (c) => (c === ')' ? '%29' : ''))
  const text = rawText.trim()
  const lines: string[] = []
  if (url) {
    const textLines = text ? text.split('\n') : []
    const label = escapeLabel(title || textLines[0] || url)
    lines.push(`- [${label}](${url})`)
    const rest = title
      ? text && text !== url && text !== title
        ? textLines
        : []
      : textLines.slice(1)
    for (const l of rest) lines.push(`  ${l}`)
  } else {
    const head = title && text && text !== title ? `${title} — ${text}` : text || title
    for (const l of head.split('\n')) lines.push(/^[*-+] /.test(l) ? `  ${l}` : `- ${l}`)
  }
  return lines.join('\n')
}

export interface AppendResult {
  /** True when the line was written without the server seeing it. */
  offline: boolean
}

/** Appends a block to the end of a note's document. Resolves once the
 * line is written (and, when the server was reachable, sent); waits for
 * the session to sync first so the block lands after the note's current
 * text, and falls back to a local append when the network is down and
 * the document was already on this device. */
export function appendToNote(noteID: string, block: string): Promise<AppendResult> {
  return new Promise((resolve, reject) => {
    let settled = false
    let written = false
    let client: SyncClient | null = null
    const finish = (offline: boolean) => {
      if (settled) return
      settled = true
      // Give the batch and the IndexedDB write a beat before teardown.
      window.setTimeout(() => {
        client?.destroy()
        resolve({ offline })
      }, 300)
    }
    const fail = (msg: string) => {
      if (settled) return
      settled = true
      client?.destroy()
      reject(new Error(msg))
    }
    const write = (offline: boolean) => {
      if (written) return
      written = true
      const c = client
      if (!c) return
      c.doc.transact(() => {
        const len = c.text.length
        if (len > 0 && !c.text.toString().endsWith('\n')) c.text.insert(len, '\n')
        c.text.insert(c.text.length, block)
      })
      finish(offline)
    }
    client = new SyncClient(noteID, {
      onStatus(s) {
        if (s === 'synced') write(false)
      },
    })
    window.setTimeout(() => {
      // Still no session: append locally when there is a local document
      // to append to; otherwise leave the line to the caller.
      if (!written && client && client.text.length > 0) write(true)
      else if (!written) fail('The note did not sync.')
    }, 8000)
  })
}
