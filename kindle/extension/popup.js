const statusEl = document.getElementById('status');

async function send(action, payload = {}) {
  return chrome.runtime.sendMessage({ action, ...payload });
}

function formatAuth(auth) {
  if (!auth?.authenticated) return 'Not signed in — open Cloud Reader';
  if (!auth.ready) return `Almost ready — need: ${auth.missing?.join(', ') || 'device token'}`;
  return `Connected (${auth.amazonDomain})`;
}

async function refreshStatus() {
  try {
    const res = await send('getAuthStatus');
    statusEl.textContent = formatAuth(res?.auth);
  } catch (e) {
    statusEl.textContent = e.message;
  }
}

document.getElementById('openDashboard').addEventListener('click', () => {
  chrome.tabs.create({ url: chrome.runtime.getURL('dashboard.html') });
});

document.getElementById('openKindle').addEventListener('click', () => {
  chrome.tabs.create({ url: 'https://read.amazon.com/kindle-library' });
});

document.getElementById('refreshSync').addEventListener('click', async () => {
  statusEl.textContent = 'Syncing…';
  const res = await send('refreshAndSync');
  if (res?.error) statusEl.textContent = res.error;
  else statusEl.textContent = `Synced ${res?.library?.length ?? 0} book(s).`;
});

document.getElementById('signOut').addEventListener('click', async () => {
  await send('clearAuth', { clearHistory: false });
  statusEl.textContent = 'Signed out.';
});

document.getElementById('resetAll').addEventListener('click', async () => {
  if (!confirm('Clear session and delete all saved reading history?')) return;
  await send('clearAuth', { clearHistory: true });
  statusEl.textContent = 'Reset complete.';
});

refreshStatus();
