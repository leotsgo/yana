// Tag pages. A note carries every #tag written in its body; the index
// page lists each tag with a count, and a tag's page lists every note
// carrying it. Nothing here is a folder: tags cut across the tree.

import { useEffect, useState } from 'preact/hooks'

import { api, ApiError } from './api'
import type { Note, TagCount } from './api'
import { Icon } from './icons'

export function TagsIndex({ onTag }: { onTag: (tag: string) => void }) {
  const [tags, setTags] = useState<TagCount[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    api
      .tags()
      .then(({ tags }) => setTags(tags))
      .catch((err: unknown) => setError(err instanceof ApiError ? err.message : 'Could not load the tags.'))
  }, [])

  return (
    <div class="page-scroll">
      <div class="report tags-report">
        <div class="report-head">
          <h1 class="report-title">Tags</h1>
          <p class="report-sub">{tags ? `${tags.length} tag${tags.length === 1 ? '' : 's'} across your notes` : ''}</p>
        </div>
        {error ? (
          <p class="error">{error}</p>
        ) : tags === null ? (
          <p class="muted">Loading…</p>
        ) : tags.length === 0 ? (
          <div class="empty-state">
            <Icon name="tag" size={22} />
            <p>No tags yet. Write #anything in a note and it shows up here, with every note that carries it.</p>
          </div>
        ) : (
          <ul class="tag-cloud">
            {tags.map((t) => (
              <li key={t.tag}>
                <a class="tag-chip" href={`/tags/${encodeURIComponent(t.tag)}`} onClick={(ev) => { ev.preventDefault(); onTag(t.tag) }}>
                  <span class="tag-chip-name">#{t.tag}</span>
                  <span class="tag-chip-count">{t.count}</span>
                </a>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

interface TagPageProps {
  tag: string
  onOpen: (id: string) => void
  onAll: () => void
}

export function TagPage({ tag, onOpen, onAll }: TagPageProps) {
  const [notes, setNotes] = useState<Note[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setNotes(null)
    setError(null)
    api
      .tagNotes(tag)
      .then(({ notes }) => setNotes(notes))
      .catch((err: unknown) => setError(err instanceof ApiError ? err.message : 'Could not load the notes.'))
  }, [tag])

  return (
    <div class="page-scroll">
      <div class="report tags-report">
        <div class="report-head">
          <h1 class="report-title">
            <span class="tag-hash">#</span>
            {tag}
          </h1>
          <p class="report-sub">{notes ? `${notes.length} note${notes.length === 1 ? '' : 's'}` : ''}</p>
          <a class="btn" href="/tags" onClick={(ev) => { ev.preventDefault(); onAll() }}>
            <Icon name="tag" />
            All tags
          </a>
        </div>
        {error ? (
          <p class="error">{error}</p>
        ) : notes === null ? (
          <p class="muted">Loading…</p>
        ) : notes.length === 0 ? (
          <div class="empty-state">
            <Icon name="tag" size={22} />
            <p>No note carries #{tag} right now.</p>
          </div>
        ) : (
          <ul class="tag-notes">
            {notes.map((n) => (
              <li key={n.id}>
                <a class="tag-note" href={`/n/${n.id}`} onClick={(ev) => { ev.preventDefault(); onOpen(n.id) }}>
                  <span class="tag-note-title">{n.title || n.path}</span>
                  <span class="tag-note-path">{n.path}</span>
                  {n.preview && <span class="tag-note-preview">{n.preview}</span>}
                </a>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}
