// Velm web push: subscribes the browser via VAPID when the user opts in.
// The toggle lives in the notification menu (data-push-toggle).
(function () {
  'use strict';
  if (!('serviceWorker' in navigator) || !('PushManager' in window)) return;

  // The toggle lives in the notification menu, which htmx fetches when the
  // bell is clicked (#337). It is NOT in the DOM at script load, so it must be
  // resolved on demand: binding here and returning when it is absent meant
  // revealIfAvailable() never ran at all.
  const currentBtn = () => document.getElementById('pushToggle');

  const csrf = document.querySelector('meta[name="csrf-token"]')?.content || '';
  const LS_KEY = 'velm-push-endpoint';

  function endpointStored() {
    try { return localStorage.getItem(LS_KEY) || ''; } catch (_) { return ''; }
  }

  function urlB64ToUint8Array(base64String) {
    const padding = '='.repeat((4 - (base64String.length % 4)) % 4);
    const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/');
    const raw = atob(base64);
    const out = new Uint8Array(raw.length);
    for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
    return out;
  }

  async function postJSON(url, body) {
    const res = await fetch(url, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      body: JSON.stringify(body)
    });
    if (!res.ok) throw new Error('push request failed (' + res.status + ')');
    return res.json();
  }

  function setLabel(on) {
    const btn = currentBtn();
    if (!btn) return;
    btn.textContent = on ? 'Disable push notifications' : 'Enable push notifications';
    btn.setAttribute('aria-pressed', on ? 'true' : 'false');
    btn.hidden = false;
  }

  // The toggle ships hidden in the template so an instance without VAPID keys
  // never shows a control that cannot work. Nothing used to reveal it, so even
  // a configured instance had no way to subscribe: the button existed, was
  // hidden, and stayed hidden (#332).
  async function revealIfAvailable() {
    if (!currentBtn()) return;
    try {
      const config = await fetch('/api/push/config', { credentials: 'same-origin' }).then((r) => r.json());
      if (!config || !config.enabled) return;
      const btn = currentBtn();
      if (!btn) return;
      btn.hidden = false;
      refreshState();
    } catch (_) {
      // Leave it hidden: no config endpoint means no push.
    }
  }

  async function refreshState() {
    try {
      const reg = await navigator.serviceWorker.getRegistration();
      if (!reg) return setLabel(false);
      const sub = await reg.pushManager.getSubscription();
      setLabel(!!(sub || endpointStored()));
    } catch (_) {
      setLabel(false);
    }
  }

  async function enable() {
    const btn = currentBtn();
    const config = await fetch('/api/push/config', { credentials: 'same-origin' }).then((r) => r.json());
    if (!config.enabled) {
      if (btn) btn.textContent = 'Push not available';
      if (btn) btn.disabled = true;
      return;
    }
    const permission = await Notification.requestPermission();
    if (permission !== 'granted') return;
    const reg = await navigator.serviceWorker.register('/sw.js');
    const sub = await reg.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: urlB64ToUint8Array(config.publicKey)
    });
    const json = sub.toJSON();
    await postJSON('/api/push/subscribe', json);
    try { localStorage.setItem(LS_KEY, sub.endpoint); } catch (_) {}
    setLabel(true);
  }

  async function disable() {
    const reg = await navigator.serviceWorker.getRegistration();
    let endpoint = endpointStored();
    try {
      const sub = reg && await reg.pushManager.getSubscription();
      if (sub) {
        endpoint = sub.endpoint;
        await sub.unsubscribe();
      }
    } catch (_) {}
    if (endpoint) {
      try { await postJSON('/api/push/unsubscribe', { endpoint }); } catch (_) {}
    }
    try { localStorage.removeItem(LS_KEY); } catch (_) {}
    setLabel(false);
  }

  // Delegated: the panel is swapped in after this script runs, so a direct
  // listener on the button would never be attached.
  document.addEventListener('click', async (e) => {
    const btn = e.target.closest && e.target.closest('#pushToggle');
    if (!btn) return;
    btn.disabled = true;
    try {
      if (btn.textContent.startsWith('Disable')) await disable();
      else await enable();
    } catch (err) {
      console.warn('push toggle failed:', err);
    } finally {
      btn.disabled = false;
      refreshState();
    }
  });

  // htmx swaps the panel in on bell click; re-reveal after every settle so the
  // toggle appears the first time the menu opens, not only on a full reload.
  document.addEventListener('htmx:afterSettle', revealIfAvailable);
  document.addEventListener('DOMContentLoaded', revealIfAvailable);

})();
