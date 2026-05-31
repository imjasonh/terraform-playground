/**
 * Thin wrapper around chrome.runtime.sendMessage for the in-extension
 * dashboard. The dashboard always runs as a chrome-extension:// page so we
 * only need internal messaging — no external connection / extension-id flow.
 */

export function isExtensionAvailable() {
  return typeof chrome !== "undefined" && Boolean(chrome?.runtime?.sendMessage);
}

export function extensionRequest(action, payload = {}) {
  return new Promise((resolve, reject) => {
    if (!isExtensionAvailable()) {
      reject(new Error("Chrome extension APIs are not available."));
      return;
    }
    chrome.runtime.sendMessage({ action, ...payload }, (response) => {
      if (chrome.runtime.lastError) {
        reject(new Error(chrome.runtime.lastError.message));
        return;
      }
      resolve(response);
    });
  });
}
