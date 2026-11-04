// Linked mentions at the foot of the rendered note, Obsidian style: the
// notes linking here, one collapsible group each, every link's line in a
// card with the link itself marked. Nothing shows until something links
// here.

import { useEffect, useState } from 'preact/hooks'
import { api } from './api'
import type { Backlink, Note } from './api'
import { Icon } from './icons'
import type { OpenNote } from './panels'
import { openProps } from './workspace'

type Group = { note: Note; links: Backlink[] }

export function PageBacklinks({ noteId, onOpen }: { noteId: string; onOpen: OpenNote }) {
  const [groups, setGroups] = useState<Group[]>([])
  useEffect(() => {
    let live = true
    setGroups([])
    api
      .backlinks(noteId)
      .then(({ backlinks }) => {
        if (live) setGroups(groupByNote(backlinks))
      })
      .catch(() => {})
    return () => {
      live = false
    }
  }, [noteId])

  if (groups.length === 0) return null
  const total = groups.reduce((n, g) => n + g.links.length, 0)
  return (
    <section class="page-backlinks" aria-label="linked mentions">
      <h2 class="page-backlinks-title">
        Linked mentions <span class="count">{total}</span>
      </h2>
      {groups.map(({ note, links }) => {
        const open = openProps((how) => onOpen(note.id, how))
        return (
          <details key={note.id} class="mention-group" open>
            <summary>
              <Icon name="chevron-right" size={14} class="mention-caret" />
              <Icon name="file-text" size={15} />
              <a href={`/n/${note.id}`} title={note.path} {...open}>
                {note.title || note.path}
              </a>
              <span class="count">{links.length}</span>
            </summary>
            {links.map((b) => (
              <a key={b.raw_target} class="mention" href={`/n/${note.id}`} {...open}>
                {marked(b)}
              </a>
            ))}
          </details>
        )
      })}
    </section>
  )
}

// The server sends one row per distinct link spelling, already ordered by
// path; a note linking here two ways becomes one group of two.
function groupByNote(back: Backlink[]): Group[] {
  const by = new Map<string, Group>()
  for (const b of back) {
    const g = by.get(b.note.id) ?? { note: b.note, links: [] }
    g.links.push(b)
    by.set(b.note.id, g)
  }
  return [...by.values()]
}

// The context line with its [[link]] (or ![[embed]]) marked.
function marked(b: Backlink) {
  const line = b.context || `[[${b.raw_target}]]`
  const at = line.indexOf('[[' + b.raw_target)
  const end = at < 0 ? -1 : line.indexOf(']]', at)
  if (end < 0) return line
  const from = line[at - 1] === '!' ? at - 1 : at
  return (
    <>
      {line.slice(0, from)}
      <mark>{line.slice(from, end + 2)}</mark>
      {line.slice(end + 2)}
    </>
  )
}
