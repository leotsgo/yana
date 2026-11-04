// The local graph in a note's details: the note and its neighbourhood to a
// depth, notes only. Collapsed by default; while it is closed nothing loads,
// neither the graph bundle nor /api/graph.

import { useMemo, useRef, useState } from 'preact/hooks'

import type { Note } from './api'
import { useGraphData, useGraphMount } from './graph-page'
import { buildView } from './graph/model'
import type { OpenNote } from './panels'
import * as prefs from './prefs'

export interface LocalGraphProps {
  note: Note
  onOpen: OpenNote
  /** Open the local graph as a page tab. */
  onOpenTab: (id: string, depth: number) => void
}

export function LocalGraph(props: LocalGraphProps) {
  const [open, setOpen] = useState(() => prefs.graphPrefs().localOpen)
  return (
    <details
      class="panel local-graph"
      open={open}
      onToggle={(ev) => {
        const on = (ev.currentTarget as HTMLDetailsElement).open
        if (on === open) return
        setOpen(on)
        prefs.setGraphPrefs({ localOpen: on })
      }}
    >
      <summary class="panel-title">Graph</summary>
      {open && <LocalGraphBody {...props} />}
    </details>
  )
}

function LocalGraphBody({ note, onOpen, onOpenTab }: LocalGraphProps) {
  const [depth, setDepth] = useState(() => prefs.graphPrefs().depth)
  const [error, setError] = useState<string | null>(null)
  const data = useGraphData(setError)
  const host = useRef<HTMLDivElement>(null)
  const view = useMemo(
    () =>
      data &&
      buildView(data, { search: '', orphans: true, ghosts: false, tags: false, colorBy: 'none', center: note.id, depth }),
    [data, note.id, depth],
  )
  useGraphMount(host, view, note.id, (n, newTab) => onOpen(n.id, newTab ? 'background' : undefined), setError)

  const state = error ?? (view === null ? 'Loading the graph…' : view.nodes.length === 0 ? "This note isn't in the graph yet." : null)

  return (
    <>
      <div class="local-graph-view">
        <div class="graph-canvas" ref={host} />
        {state && <p class={'graph-state' + (error ? ' error' : '')}>{state}</p>}
      </div>
      <div class="local-graph-tools">
        <label class="graph-range">
          <span>Depth {depth}</span>
          <input
            type="range"
            min={1}
            max={3}
            step={1}
            value={depth}
            onInput={(ev) => {
              const d = Number((ev.target as HTMLInputElement).value)
              setDepth(d)
              prefs.setGraphPrefs({ depth: d })
            }}
          />
        </label>
        <button type="button" class="btn small" onClick={() => onOpenTab(note.id, depth)}>
          Open as tab
        </button>
      </div>
    </>
  )
}
