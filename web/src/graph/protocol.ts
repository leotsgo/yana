// Messages between the graph view and its physics worker. No imports: the
// main bundle reads DEFAULT_FORCES / FORCE_RANGES from here.

export interface Forces { center: number; repel: number; link: number; distance: number }

export const DEFAULT_FORCES: Forces = { center: 0.05, repel: 60, link: 1, distance: 40 }

/** Slider bounds per force. */
export const FORCE_RANGES: Record<keyof Forces, [min: number, max: number, step: number]> =
  { center: [0, 0.2, 0.01], repel: [0, 300, 5], link: [0, 2, 0.05], distance: [10, 200, 5] }

/** `links` is flat node-index pairs; `pos` is x,y per node, NaN where unplaced. */
export type ToWorker =
  | { t: 'init'; n: number; links: Int32Array; pos: Float32Array; forces: Forces; animate: boolean }
  | { t: 'forces'; forces: Forces }
  | { t: 'drag'; i: number; x: number; y: number }
  | { t: 'release'; i: number }
  | { t: 'pause' }
  | { t: 'resume' }

export type FromWorker = { t: 'tick'; pos: Float32Array; settled: boolean }
