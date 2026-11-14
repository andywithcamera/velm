// Velm web push: subscribes the browser via VAPID when the user opts in.
// The toggle lives in the notification menu (data-push-toggle).
(function () {
  'use strict';
  // A capability gate must not be silent. iOS exposes no PushManager outside a
  // Home Screen web app, so returning early here left the menu with no control
  // and no explanation at all (#340). The toggle is shown instead, disabled,
  // with the reason.
  const supported = () => 'serviceWorker' in navigator && 'PushManager' in window;
  const UNSUPPORTED = 'Push needs this site added to your Home Screen';

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
    btn.disabled = false;
  }

  // Errors were swallowed with console.warn, which is invisible in an iOS
  // home-screen app — the opt-in could fail at permission, at service worker
  // registration, at pushManager.subscribe, or at the POST, and the button
  // just stayed on "Enable" (#344). Say which step failed on the button.
  function setError(btn, message) {
    if (!btn) return false;
    btn.hidden = false;
    btn.disabled = false;
    btn.textContent = message;
    return false;
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
      if (!supported()) {
        btn.textContent = UNSUPPORTED;
        btn.disabled = true;
        return;
      }
      refreshState();
    } catch (_) {
      // Leave it hidden: no config endpoint means no push.
    }
  }

  // Authoritative state comes from the server: the rows the sender will read.
  // localStorage only proved this browser once asked; a subscription pruned as
  // stale, or a POST that never landed, still looked subscribed locally.
  async function refreshState() {
    try {
      const status = await fetch('/api/push/status', { credentials: 'same-origin' })
        .then((r) => (r.ok ? r.json() : null));
      if (status && typeof status.subscribed === 'boolean') {
        setLabel(status.subscribed);
        return;
      }
    } catch (_) {
      // Fall through to the local check below.
    }
    try {
      const reg = await navigator.serviceWorker.getRegistration();
      if (!reg) return setLabel(false);
      const sub = await reg.pushManager.getSubscription();
      setLabel(!!(sub || endpointStored()));
    } catch (_) {
      setLabel(false);
    }
  }

  // Returns true only when the server now holds a subscription. Every failure
  // path reports on the button, because that is the only surface a user sees.
  async function enable() {
    const btn = currentBtn();
    if (!supported()) {
      if (btn) { btn.hidden = false; btn.textContent = UNSUPPORTED; btn.disabled = true; }
      return false;
    }
    const config = await fetch('/api/push/config', { credentials: 'same-origin' }).then((r) => r.json());
    if (!config.enabled) {
      setError(btn, 'Push not available on this server');
      if (btn) btn.disabled = true;
      return false;
    }
    let permission;
    try {
      permission = await Notification.requestPermission();
    } catch (err) {
      return setError(btn, 'Notification permission failed');
    }
    if (permission !== 'granted') {
      return setError(btn, permission === 'denied'
        ? 'Notifications blocked for this site'
        : 'Notification permission not granted');
    }
    let reg;
    try {
      reg = await navigator.serviceWorker.register('/sw.js');
    } catch (err) {
      return setError(btn, 'Service worker registration failed');
    }
    let sub;
    try {
      sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlB64ToUint8Array(config.publicKey)
      });
    } catch (err) {
      return setError(btn, 'Push subscribe failed');
    }
    const json = sub.toJSON();
    try {
      await postJSON('/api/push/subscribe', json);
    } catch (err) {
      return setError(btn, 'Saving subscription failed');
    }
    try { localStorage.setItem(LS_KEY, sub.endpoint); } catch (_) {}
    setLabel(true);
    return true;
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
    let ok = true;
    try {
      if (btn.textContent.startsWith('Disable')) await disable();
      else ok = await enable();
    } catch (err) {
      ok = false;
      setError(btn, 'Push toggle failed: ' + (err && err.message ? err.message : 'unknown'));
    } finally {
      btn.disabled = false;
      // Refreshing after an error would immediately overwrite the message
      // with the plain label, hiding exactly what the user needs to read.
      if (ok) refreshState();
    }
  });

  // htmx swaps the panel in on bell click; re-reveal after every settle so the
  // toggle appears the first time the menu opens, not only on a full reload.
  document.addEventListener('htmx:afterSettle', revealIfAvailable);
  document.addEventListener('DOMContentLoaded', revealIfAvailable);

})();
