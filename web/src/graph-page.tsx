// The graph page: every readable note as a dot, the links among them as
// lines, laid out by a force simulation (the lazily loaded graph bundle).
// With a note it is that note's local graph, its neighbourhood to a depth.
// A floating panel holds the filters and forces, remembered per browser.

import { useEffect, useMemo, useRef, useState } from 'preact/hooks'

import { graphData, onGraphData } from './graph-data'
import { GRAPH_WORKER, loadGraph } from './graph-load'
import { buildView } from './graph/model'
import type { ColorBy, GraphPayload } from './graph/model'
import { FORCE_RANGES } from './graph/protocol'
import type { Forces } from './graph/protocol'
import type { GraphHandle } from './graph/view'
import { Icon } from './icons'
import type { OpenNote } from './panels'
import * as prefs from './prefs'
import type { GraphPrefs } from './prefs'

const SEARCH_DEBOUNCE_MS = 250
const LOAD_ERROR = 'Could not load the graph.'
const NO_WEBGL = "This browser can't draw the graph (WebGL2 is off)."

const FORCE_LABELS: [keyof Forces, string][] = [
  ['center', 'Centre'],
  ['repel', 'Repel'],
  ['link', 'Link strength'],
  ['distance', 'Link distance'],
]

export interface GraphPageProps {
  /** '' for the whole graph; a note id for its local graph. */
  note: string
  depth: number
  phone: boolean
  onOpen: OpenNote
  onTag: (tag: string) => void
}

export function GraphPage({ note, depth: initialDepth, phone, onOpen, onTag }: GraphPageProps) {
  const [p, setP] = useState(prefs.graphPrefs)
  const [query, setQuery] = useState(p.search)
  const [depth, setDepth] = useState(initialDepth)
  const [panel, setPanel] = useState(p.panel ?? !phone)
  const [data, setData] = useState<GraphPayload | null>(null)
  const [error, setError] = useState<string | null>(null)
  const host = useRef<HTMLDivElement>(null)
  const graph = useRef<GraphHandle | null>(null)
  const shown = useRef<unknown>(null) // the view the graph last got
  const open = useRef({ onOpen, onTag })
  open.current = { onOpen, onTag }

  const change = (q: Partial<GraphPrefs>) => {
    setP((prev) => ({ ...prev, ...q }))
    prefs.setGraphPrefs(q)
  }

  useEffect(() => {
    let live = true
    graphData().then(
      (g) => live && setData(g),
      () => live && setError(LOAD_ERROR),
    )
    const off = onGraphData(setData)
    return () => {
      live = false
      off()
    }
  }, [])

  useEffect(() => {
    if (query === p.search) return
    const t = window.setTimeout(() => change({ search: query }), SEARCH_DEBOUNCE_MS)
    return () => window.clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query])

  const view = useMemo(
    () =>
      data &&
      buildView(data, {
        search: p.search,
        orphans: p.orphans,
        ghosts: p.ghosts,
        tags: p.tags,
        colorBy: p.colorBy,
        ...(note ? { center: note, depth } : {}),
      }),
    [data, p.search, p.orphans, p.ghosts, p.tags, p.colorBy, note, depth],
  )
  const latest = useRef(view)
  latest.current = view

  // Mount the graph once there is something to draw.
  const ready = view !== null
  useEffect(() => {
    if (!ready) return
    let gone = false
    loadGraph().then(
      (m) => {
        const el = host.current
        const v = latest.current
        if (gone || !el || !v) return
        try {
          graph.current = m.mountGraph(el, {
            workerURL: GRAPH_WORKER,
            view: v,
            forces: prefs.graphPrefs().forces,
            center: note || undefined,
            reducedMotion: window.matchMedia('(prefers-reduced-motion: reduce)').matches,
            onOpen: (n, newTab) => {
              if (n.kind === 'tag') open.current.onTag(n.id)
              else open.current.onOpen(n.id, newTab ? 'background' : undefined)
            },
            onError: () => setError(LOAD_ERROR),
          })
          shown.current = v
        } catch {
          setError(NO_WEBGL)
        }
      },
      () => !gone && setError(LOAD_ERROR),
    )
    return () => {
      gone = true
      graph.current?.destroy()
      graph.current = null
    }
  }, [ready, note])

  useEffect(() => {
    if (!view || !graph.current || shown.current === view) return
    shown.current = view
    graph.current.update(view)
  }, [view])

  const setForce = (k: keyof Forces, x: number) => {
    const forces = { ...p.forces, [k]: x }
    change({ forces })
    graph.current?.setForces(forces)
  }

  const togglePanel = () => {
    setPanel(!panel)
    change({ panel: !panel })
  }

  const title = note && view ? view.nodes.find((n) => n.key === note)?.label : undefined
  const state = error ?? (view === null ? 'Loading the graph…' : view.nodes.length === 0 ? 'No notes match.' : null)

  return (
    <div class="graph-page">
      <div class="graph-canvas" ref={host} />
      {state && <p class={'graph-state' + (error ? ' error' : '')}>{state}</p>}
      {note && (
        <div class="graph-head">
          <h1 class="graph-title">Local graph — {title ?? 'note'}</h1>
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
                change({ depth: d })
              }}
            />
          </label>
        </div>
      )}
      <div class="graph-tools">
        <button type="button" class={'btn' + (panel ? ' on' : '')} aria-expanded={panel} onClick={togglePanel}>
          <Icon name="settings" />
          Filters
        </button>
        {panel && (
          <div class="graph-panel">
            <input
              class="input"
              type="search"
              placeholder="Search notes"
              aria-label="Search notes"
              value={query}
              onInput={(ev) => setQuery((ev.target as HTMLInputElement).value)}
            />
            <label class="check">
              <input type="checkbox" checked={p.orphans} onChange={(ev) => change({ orphans: (ev.target as HTMLInputElement).checked })} />
              Orphans
            </label>
            <label class="check">
              <input type="checkbox" checked={p.ghosts} onChange={(ev) => change({ ghosts: (ev.target as HTMLInputElement).checked })} />
              Unresolved links
            </label>
            <label class="check">
              <input type="checkbox" checked={p.tags} onChange={(ev) => change({ tags: (ev.target as HTMLInputElement).checked })} />
              Tags
            </label>
            <label class="field">
              <span class="field-label">Colour by</span>
              <select class="select" value={p.colorBy} onChange={(ev) => change({ colorBy: (ev.target as HTMLSelectElement).value as ColorBy })}>
                <option value="none">None</option>
                <option value="space">Space</option>
                <option value="folder">Top folder</option>
              </select>
            </label>
            {FORCE_LABELS.map(([k, label]) => {
              const [min, max, step] = FORCE_RANGES[k]
              return (
                <label class="graph-range" key={k}>
                  <span>{label}</span>
                  <input
                    type="range"
                    min={min}
                    max={max}
                    step={step}
                    value={p.forces[k]}
                    onInput={(ev) => setForce(k, Number((ev.target as HTMLInputElement).value))}
                  />
                </label>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}
