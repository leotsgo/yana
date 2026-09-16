// The service worker source. build.mjs copies it to dist/sw.js with the
// precache list and a version stamp filled in. The shell (index.html plus
// the hashed bundles) is precached so the app opens with no network;
// hashed assets are immutable and served cache-first; every navigation is
// network-first so a new deploy is picked up on the next open. Nothing
// under /api or /ws is ever cached.
const VERSION = '__BUILD_VERSION__'
const PRECACHE = __PRECACHE_MANIFEST__
const CACHE = 'yana-shell-' + VERSION

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      .then((cache) => cache.addAll(PRECACHE))
      .then(() => self.skipWaiting()),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((names) => Promise.all(names.filter((n) => n !== CACHE).map((n) => caches.delete(n))))
      .then(() => self.clients.claim()),
  )
})

self.addEventListener('fetch', (event) => {
  const req = event.request
  if (req.method !== 'GET') return
  const url = new URL(req.url)
  if (url.origin !== self.location.origin) return
  // The API, the relay, and the content origin are never cached.
  if (url.pathname === '/api' || url.pathname.startsWith('/api/') || url.pathname === '/ws') return

  if (req.mode === 'navigate') {
    event.respondWith(shellFirst(req))
    return
  }
  if (url.pathname.startsWith('/assets/')) {
    event.respondWith(cacheFirst(req))
    return
  }
  // Icons and the manifest are precached; anything else same-origin goes
  // to the network and through untouched on failure.
  event.respondWith(cacheFirstIfPresent(req))
})

// Navigations (and only navigations) need the shell: try the network so a
// new deploy is served on the next open, refresh the cached copy in the
// background (only when the answer really is the shell), and fall back to
// the cached shell offline.
async function shellFirst(req) {
  try {
    const res = await fetch(req)
    if (res.ok && (res.headers.get('content-type') || '').includes('text/html')) {
      const cache = await caches.open(CACHE)
      cache.put('/index.html', res.clone())
    }
    return res
  } catch {
    const cached = await caches.match('/index.html')
    if (cached) return cached
    return new Response('Offline and the shell is not cached yet.', {
      status: 503,
      headers: { 'Content-Type': 'text/plain; charset=utf-8' },
    })
  }
}

// Immutable hashed assets: serve from cache, fetch and fill on miss.
async function cacheFirst(req) {
  const cached = await caches.match(req)
  if (cached) return cached
  const res = await fetch(req)
  if (res.ok) {
    const cache = await caches.open(CACHE)
    cache.put(req, res.clone())
  }
  return res
}

// Precached static files: cache when present, network otherwise, and a
// clean miss offline rather than a rejected promise.
async function cacheFirstIfPresent(req) {
  const cached = await caches.match(req)
  if (cached) return cached
  try {
    return await fetch(req)
  } catch {
    return new Response('', { status: 504 })
  }
}
