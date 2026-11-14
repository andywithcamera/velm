// Velm service worker: app-shell caching.
// Static assets: network-first, cache fallback.
// Pages and API: network-first with offline fallback to the cached shell.
//
// Static assets used to be cache-first, on the assumption that their URLs were
// "immutable, versioned". They are not: the templates request
// /static/js/push-client.js with no ?v=, and VERSION below was never bumped (it
// stayed 'velm-sw-v1' from #246). So the first response a browser cached was
// served forever, overriding the origin's own `Cache-Control: no-cache`, and
// every front-end fix shipped after that was invisible to any device with the
// service worker installed (#348). Network-first keeps the offline shell but
// never lets a stale asset outlive one online navigation.
const VERSION = 'velm-sw-v2';
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

  // Static assets: network-first, cache fallback. A rejected or failed fetch
  // must not leave the page without the asset, hence the cache fallback.
  if (url.pathname.startsWith('/static/')) {
    event.respondWith(
      fetch(event.request).then((res) => {
        if (res && res.ok) {
          const copy = res.clone();
          caches.open(VERSION).then((c) => c.put(event.request, copy));
        }
        return res;
      }).catch(() => caches.match(event.request).then((hit) => hit || Response.error()))
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

// ─── Web Push ─────────────────────────────────────────────────────────
self.addEventListener('push', (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch (_) {
    data = { title: 'Velm', body: event.data ? event.data.text() : '' };
  }
  event.waitUntil(self.registration.showNotification(data.title || 'Velm', {
    body: data.body || '',
    icon: '/static/icons/icon-192.png',
    badge: '/static/icons/icon-192.png',
    tag: 'velm-' + (data.href || ''),
    data: { href: data.href || '/task' }
  }));
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const href = (event.notification.data && event.notification.data.href) || '/task';
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clients) => {
      for (const client of clients) {
        if (client.url.includes(self.location.origin)) {
          client.navigate(href).catch(() => {});
          return client.focus();
        }
      }
      return self.clients.openWindow(href);
    })
  );
});
