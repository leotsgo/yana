import { api } from './api'
import type { UnresolvedLink } from './api'
import { h, clear } from './dom'

// The unresolved-link report: every broken wikilink in the tree, grouped
// by space. Clicking a row opens the note holding the link.
export function renderUnresolvedReport(
  container: HTMLElement,
  onOpen: (id: string) => void,
  reload: () => void,
): void {
  clear(container)
  container.append(
    h('article', { class: 'note' },
      h('header', { class: 'note-header' },
        h('nav', { class: 'crumbs', 'aria-label': 'path' }, h('span', { class: 'crumb crumb-file' }, 'Unresolved links'))),
      h('p', { class: 'muted loading' }, 'Loading…'),
    ),
  )
  api
    .unresolved()
    .then(({ unresolved }) => {
      clear(container)
      const article = h('article', { class: 'note unresolved-report' })
      article.append(
        h('header', { class: 'note-header' },
          h('nav', { class: 'crumbs', 'aria-label': 'path' }, h('span', { class: 'crumb crumb-file' }, 'Unresolved links'))),
      )
      if (unresolved.length === 0) {
        article.append(h('p', { class: 'muted' }, 'Every wikilink resolves.'))
        container.append(article)
        return
      }
      article.append(h('p', { class: 'muted' }, `${unresolved.length} link${unresolved.length === 1 ? '' : 's'} point at notes that do not exist yet.`))
      const groups = new Map<string, UnresolvedLink[]>()
      for (const u of unresolved) {
        const list = groups.get(u.note.space) ?? []
        list.push(u)
        groups.set(u.note.space, list)
      }
      for (const [space, rows] of groups) {
        const section = h('section', { class: 'unresolved-space' },
          h('h2', { class: 'space-name' }, space === '' ? '/' : space + '/'))
        for (const u of rows) {
          section.append(
            h('li', { class: 'unresolved-row' },
              h('a', {
                class: 'unresolved-note',
                href: `/n/${u.note.id}`,
                title: u.note.path,
                onClick: (ev) => {
                  ev.preventDefault()
                  onOpen(u.note.id)
                },
              }, u.note.path),
              h('span', { class: 'unresolved-target' }, `[[${u.raw_target}]]`),
            ),
          )
        }
        article.append(section)
      }
      container.append(article)
    })
    .catch(() => {
      clear(container)
      container.append(
        h('div', { class: 'placeholder' },
          h('p', { class: 'error' }, 'Could not load the unresolved links.'),
          h('button', { class: 'btn', onClick: () => reload() }, 'Try again')))
    })
}
