const statusEl = document.getElementById('status');
const extIdEl = document.getElementById('extId');

extIdEl.textContent = chrome.runtime.id;

async function refreshStatus() {
  const res = await chrome.runtime.sendMessage({ action: 'getKindleCredentials' });
  const c = res?.credentials || {};
  const parts = [];
  if (c.ubid || c.sid) parts.push('cookies OK');
  else parts.push('cookies missing — sign in on Amazon');
  if (c.deviceToken) parts.push('device token OK');
  else parts.push('device token pending');
  statusEl.textContent = parts.join(' · ');
}

document.getElementById('openKindle').addEventListener('click', () => {
  chrome.tabs.create({ url: 'https://read.amazon.com/kindle-library' });
});

document.getElementById('refreshCookies').addEventListener('click', async () => {
  await refreshStatus();
});

document.getElementById('openDashboard').addEventListener('click', () => {
  chrome.tabs.create({ url: 'http://localhost:8080/' });
});

refreshStatus();
