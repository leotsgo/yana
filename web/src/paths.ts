// Paths typed by a person, resolved against where a note already sits.
// A title of `projects/kiln` moves the note into projects/ and calls it
// kiln; `/work/plan` starts from the root; `../notes` steps up; a
// trailing slash moves the note without renaming it.

import { dirOf } from './api'

/** A file name for a title: the characters a path cannot hold are dropped. */
export function fileName(title: string): string {
  return title
    .replace(/[\\/:*?"<>|\x00-\x1f]/g, '')
    .replace(/\s+/g, ' ')
    .trim()
    .replace(/^\.+/, '')
    .slice(0, 120) || 'untitled'
}

/** A folder name typed by a person; empty when nothing usable is left. */
export function dirName(seg: string): string {
  return seg
    .replace(/[\\/:*?"<>|\x00-\x1f]/g, '')
    .replace(/\s+/g, ' ')
    .trim()
    .replace(/^\.+$/, '')
    .slice(0, 120)
}

/** A folder path typed against a base folder: `a/b` sits inside base,
 * `/a/b` starts at the root, `..` steps up. Segments that clean to
 * nothing are dropped. */
export function resolveDir(base: string, typed: string): string {
  const t = typed.trim()
  const parts = t.startsWith('/') ? [] : base === '' ? [] : base.split('/')
  for (const raw of t.split('/')) {
    const seg = raw.trim()
    if (seg === '' || seg === '.') continue
    if (seg === '..') {
      parts.pop()
      continue
    }
    const name = dirName(seg)
    if (name) parts.push(name)
  }
  return parts.join('/')
}

export interface TitlePath {
  /** The heading the note gets; the current one when the typed text ended in a slash. */
  title: string
  /** The folder the note lands in. */
  dir: string
  /** The full path of the file after the change. */
  path: string
  /** True when the file changes folder. */
  moves: boolean
}

/** What a title typed into the header means for the note: the last
 * segment names it, the segments before it place it. */
export function resolveTitle(notePath: string, currentTitle: string, raw: string): TitlePath {
  const base = notePath.slice(notePath.lastIndexOf('/') + 1)
  const dot = base.lastIndexOf('.')
  const ext = dot > 0 ? base.slice(dot) : ''
  const cur = dirOf(notePath)
  const text = raw.replace(/\s+/g, ' ').trim()
  const cut = text.lastIndexOf('/')
  let dir = cur
  let title = text
  if (cut >= 0) {
    dir = resolveDir(cur, text.slice(0, cut + 1))
    title = text.slice(cut + 1).trim()
    if (title === '') title = currentTitle
  }
  const name = fileName(title) + ext
  const path = dir ? `${dir}/${name}` : name
  return { title, dir, path, moves: dir !== cur }
}

/** Lines taken by a frontmatter block at the top of the text, 0 when
 * there is none. Mirrors the server's rule: the first line is exactly
 * `---`, the block ends at `---` or `...`, and an unterminated block is
 * body. */
export function headLines(text: string): number {
  if (!/^---\r?\n/.test(text)) return 0
  const lines = text.split('\n')
  for (let i = 1; i < lines.length; i++) {
    const l = (lines[i] ?? '').replace(/\r$/, '')
    if (l === '---' || l === '...') return i + 1
  }
  return 0
}

/** Where the body starts in a note's whole text: the first character
 * past the frontmatter block. */
export function bodyStart(text: string): number {
  let pos = 0
  for (let i = 0; i < headLines(text); i++) pos = text.indexOf('\n', pos) + 1
  return pos
}

/** A rune count (how the server counts) as an offset in JS string terms
 * (how strings and the editor count): the length, in UTF-16 units, of
 * the first `runes` runes of body. */
export function utf16At(runes: number, body: string): number {
  let units = 0
  let seen = 0
  let i = 0
  while (i < body.length && seen < runes) {
    const cp = body.codePointAt(i) ?? 0
    const width = cp > 0xffff ? 2 : 1
    i += width
    units += width
    seen++
  }
  return units
}
