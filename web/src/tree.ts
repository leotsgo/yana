import type { SpaceTree, TreeNode } from './api'
import { h, clear } from './dom'

// Directory open/closed state survives re-renders within a session.
const collapsed = new Set<string>()

export interface TreeView {
  render(spaces: SpaceTree[]): void
  select(id: string | null): void
}

export function createTree(container: HTMLElement, onOpen: (id: string) => void): TreeView {
  let selected: string | null = null

  function noteRow(n: TreeNode): HTMLElement {
    const row = h(
      'a',
      {
        class: 'tree-note' + (n.id === selected ? ' selected' : ''),
        href: `/n/${n.id}`,
        'data-id': n.id,
        title: n.path,
        onClick: (ev) => {
          ev.preventDefault()
          if (n.id) onOpen(n.id)
        },
      },
      h('span', { class: 'tree-title' }, n.title || n.name),
      n.kind === 'html' ? h('span', { class: 'tree-kind' }, 'html') : null,
    )
    return row
  }

  function dirRow(n: TreeNode, depth: number): HTMLElement {
    const isOpen = !collapsed.has(n.path)
    const kids = h('div', { class: 'tree-children', hidden: !isOpen })
    for (const c of n.children ?? []) kids.append(render(c, depth + 1))
    const toggle = h(
      'button',
      {
        class: 'tree-dir' + (isOpen ? ' open' : ''),
        type: 'button',
        onClick: () => {
          if (collapsed.has(n.path)) collapsed.delete(n.path)
          else collapsed.add(n.path)
          const nowOpen = !collapsed.has(n.path)
          kids.hidden = !nowOpen
          toggle.classList.toggle('open', nowOpen)
        },
      },
      h('span', { class: 'tree-caret' }),
      h('span', { class: 'tree-title' }, n.name + '/'),
    )
    return h('div', { class: 'tree-dirwrap', style: `--depth:${depth}` }, toggle, kids)
  }

  function render(n: TreeNode, depth: number): HTMLElement {
    return n.type === 'dir' ? dirRow(n, depth) : noteRow(n)
  }

  return {
    render(spaces) {
      clear(container)
      if (spaces.length === 0) {
        container.append(
          h(
            'p',
            { class: 'empty' },
            'No notes yet. Drop a markdown file into the notes directory; it will show up on the next scan.',
          ),
        )
        return
      }
      for (const s of spaces) {
        const name = s.name === '' ? '/' : s.name + '/'
        const section = h(
          'section',
          { class: 'space' },
          h('h2', { class: 'space-name', title: `${s.notes} notes` }, name, h('span', { class: 'space-count' }, String(s.notes))),
        )
        for (const c of s.children) section.append(render(c, 0))
        container.append(section)
      }
    },
    select(id) {
      selected = id
      for (const el of container.querySelectorAll<HTMLElement>('.tree-note')) {
        el.classList.toggle('selected', el.dataset['id'] === id)
      }
      const cur = container.querySelector<HTMLElement>('.tree-note.selected')
      cur?.scrollIntoView({ block: 'nearest' })
    },
  }
}
