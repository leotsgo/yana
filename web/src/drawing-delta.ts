// What a drawing sends its room: the elements that changed since the last
// send, packed into frames the relay accepts. No imports; the test runner
// loads it directly.

/** The part of an Excalidraw element the delta reads. */
export interface Stamped {
  id: string
  version: number
}

/**
 * The elements whose version moved past what the room was last given,
 * in scene order; `sent` records them as given.
 */
export function changedSince<T extends Stamped>(elements: readonly T[], sent: Map<string, number>): T[] {
  const out: T[] = []
  for (const e of elements) {
    if ((sent.get(e.id) ?? -1) >= e.version) continue
    sent.set(e.id, e.version)
    out.push(e)
  }
  return out
}

/** Records elements the room already has (they came from it), so they are not sent back. */
export function markSent(elements: readonly Stamped[], sent: Map<string, number>): void {
  for (const e of elements) if ((sent.get(e.id) ?? -1) < e.version) sent.set(e.id, e.version)
}

/** One dr payload: elements (els), image files (files), and on a join the hello asking the room for its scene. */
export interface Delta {
  els?: unknown[]
  files?: unknown[]
  hello?: true
}

const bytes = (s: string): number => new TextEncoder().encode(s).length

/**
 * Packs elements into payloads of at most max bytes each, in order; an
 * element too large for any frame is left out (the save carries it).
 * hello rides on the first payload, which is sent even with no elements.
 */
export function pack(elements: readonly unknown[], max: number, hello = false): string[] {
  const out: string[] = []
  let batch: string[] = []
  let size = 0
  const head = (first: boolean) => (first && hello ? '{"hello":true,"els":[' : '{"els":[')
  const close = () => {
    out.push(head(out.length === 0) + batch.join(',') + ']}')
    batch = []
    size = 0
  }
  for (const e of elements) {
    const json = JSON.stringify(e)
    const n = bytes(json) + 1
    if (n + 32 > max) continue
    if (size + n + 32 > max) close()
    batch.push(json)
    size += n
  }
  if (batch.length > 0 || (hello && out.length === 0)) close()
  return out
}

/** The payload for one image file, or null when it is too large for a frame. */
export function packFile(file: unknown, max: number): string | null {
  const json = JSON.stringify({ files: [file] })
  return bytes(json) <= max ? json : null
}
