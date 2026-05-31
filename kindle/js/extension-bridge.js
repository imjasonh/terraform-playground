export function isExtensionAvailable() {
  return typeof chrome !== 'undefined' && chrome?.runtime?.sendMessage;
}

/**
 * Validates stored extension ID via ping.
 * @returns {Promise<string | null>}
 */
export async function detectExtensionId() {
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
  const extensionId =
    localStorage.getItem('kindle-extension-id') || (await detectExtensionId());
  if (!extensionId) {
    throw new Error(
      'Kindle Chart Sync extension not found. Load the extension and set its ID, or click “Detect extension”.'
    );
  }
  return sendToExtension(extensionId, { action, ...payload });
}
