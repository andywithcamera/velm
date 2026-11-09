// Velm service worker: app-shell caching.
// Static assets: cache-first (immutable, versioned URLs).
// Pages and API: network-first with offline fallback to the cached shell.
const VERSION = 'velm-sw-v1';
const SHELL = ['/', '/static/css/ui.css', '/static/icons/icon-192.png', '/offline.html'];

self.addEventListener('install', (event) => {
  event.waitUntil(caches.open(VERSION).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting()));
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== VERSION).map((k) => caches.delete(k)))
    ).then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url);
  if (event.request.method !== 'GET' || url.origin !== self.location.origin) return;

  // Static assets: cache-first.
  if (url.pathname.startsWith('/static/')) {
    event.respondWith(
      caches.match(event.request).then((hit) =>
        hit || fetch(event.request).then((res) => {
          const copy = res.clone();
          caches.open(VERSION).then((c) => c.put(event.request, copy));
          return res;
        })
      )
    );
    return;
  }

  // Pages/API: network-first, fall back to cache, then offline page.
  event.respondWith(
    fetch(event.request).then((res) => {
      const copy = res.clone();
      caches.open(VERSION).then((c) => c.put(event.request, copy));
      return res;
    }).catch(() =>
      caches.match(event.request).then((hit) => hit || caches.match('/offline.html'))
    )
  );
});
