export function isExtensionAvailable() {
  return typeof chrome !== 'undefined' && chrome?.runtime?.sendMessage;
}

/** Running as chrome-extension:// dashboard (no extension ID needed). */
export function isExtensionPage() {
  return (
    isExtensionAvailable() &&
    typeof location !== 'undefined' &&
    location.protocol === 'chrome-extension:'
  );
}

function sendInternal(message) {
  return new Promise((resolve, reject) => {
    chrome.runtime.sendMessage(message, (response) => {
      if (chrome.runtime.lastError) {
        reject(new Error(chrome.runtime.lastError.message));
        return;
      }
      resolve(response);
    });
  });
}

/**
 * @param {string} extensionId
 * @param {{ action: string, [key: string]: unknown }} message
 */
export function sendToExtension(extensionId, message) {
  return new Promise((resolve, reject) => {
    if (!isExtensionAvailable()) {
      reject(new Error('Chrome extension APIs are not available in this page.'));
      return;
    }
    chrome.runtime.sendMessage(extensionId, message, (response) => {
      if (chrome.runtime.lastError) {
        reject(new Error(chrome.runtime.lastError.message));
        return;
      }
      resolve(response);
    });
  });
}

export async function extensionRequest(action, payload = {}) {
  if (isExtensionPage()) {
    return sendInternal({ action, ...payload });
  }

  const extensionId = localStorage.getItem('kindle-extension-id');
  if (!extensionId) {
    throw new Error(
      'Open the dashboard from the Kindle Chart Sync extension popup, or paste your extension ID once under Advanced setup.'
    );
  }
  return sendToExtension(extensionId, { action, ...payload });
}

/**
 * @returns {Promise<string | null>}
 */
export async function detectExtensionId() {
  if (isExtensionPage()) return chrome.runtime.id;
  if (!isExtensionAvailable()) return null;
  const stored = localStorage.getItem('kindle-extension-id');
  if (!stored) return null;
  try {
    const res = await sendToExtension(stored, { action: 'ping' });
    if (res?.ok) return stored;
  } catch {
    return null;
  }
  return null;
}
