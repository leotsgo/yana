// The installable-app plumbing on the page side: service worker
// registration, the "reload for the new version" notice, the browser's
// install prompt, and a coarse online/offline signal. The worker itself
// is sw.template.js; this module only talks to it.

declare const __PWA__: boolean

export interface NetState {
  /** The service worker took over from an older version; a reload picks it up. */
  updateReady: boolean
  /** The browser offered an install prompt and the user has not used it. */
  canInstall: boolean
  /** The browser reports no network connection. */
  offline: boolean
}

let state: NetState = { updateReady: false, canInstall: false, offline: !navigator.onLine }
const listeners = new Set<() => void>()

function set(patch: Partial<NetState>): void {
  state = { ...state, ...patch }
  for (const l of listeners) l()
}

export function netState(): NetState {
  return state
}

export function subscribeNet(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

let installEvent: Event & { prompt: () => Promise<void> } | null = null

/** Show the browser's install prompt, when it made one available. */
export async function promptInstall(): Promise<boolean> {
  if (!installEvent) return false
  await installEvent.prompt()
  installEvent = null
  set({ canInstall: false })
  return true
}

/** Reload into the new version the waiting worker has already claimed. */
export function reloadForUpdate(): void {
  location.reload()
}

let started = false

/** Registers the worker and wires the update and install signals. Called
 * once at boot; safe to call again. */
export function initPwa(): void {
  if (started) return
  started = true
  window.addEventListener('online', () => set({ offline: false }))
  window.addEventListener('offline', () => set({ offline: true }))
  if (!__PWA__ || !('serviceWorker' in navigator)) return

  window.addEventListener('beforeinstallprompt', (ev) => {
    ev.preventDefault()
    installEvent = ev as Event & { prompt: () => Promise<void> }
    set({ canInstall: true })
  })
  window.addEventListener('appinstalled', () => set({ canInstall: false }))

  // Whether the page is controlled right now. The first takeover of an
  // uncontrolled page is just the worker arriving; a takeover of a
  // controlled page is an update worth announcing.
  let controlled = !!navigator.serviceWorker.controller
  navigator.serviceWorker.addEventListener('controllerchange', () => {
    if (controlled) set({ updateReady: true })
    controlled = true
  })
  void navigator.serviceWorker
    .register('/sw.js', { updateViaCache: 'none' })
    .then((reg) => {
      // A worker from a previous deploy may already be waiting.
      if (reg.waiting && navigator.serviceWorker.controller) set({ updateReady: true })
      // Check for a new deploy whenever the app comes back to the front.
      document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'visible') void reg.update()
      })
    })
    .catch(() => {
      // No worker, no offline shell; the app still runs online.
    })
}
