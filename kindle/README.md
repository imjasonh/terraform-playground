# Kindle Reading Progress

Client-side step chart of Kindle reading progress **P(t)**, synced automatically from your Amazon session via a Chrome extension. No JSON paste.

## Quick start

1. Chrome → `chrome://extensions` → **Developer mode** → **Load unpacked** → `kindle/extension`
2. Sign in at [read.amazon.com](https://read.amazon.com) (refresh once so device token is captured)
3. Click the extension icon → **Open dashboard**
4. Charts load automatically; click **Refresh** after reading sessions to append snapshots

### If auth expires

- **Sign out** — clears stored Amazon session in the extension; reading history is kept
- **Reset all data** — clears session and all saved progress snapshots
- Then **Connect Amazon** → open Cloud Reader → **Refresh**

## Localhost (optional)

```bash
cd kindle && python3 -m http.server 8080
```

Use only if you prefer `http://localhost:8080` over the extension dashboard. Expand **Advanced** and save your extension ID once.

## Security

- Cookies never leave your browser (extension `chrome.storage.local` only)
- Do not share exported history files if they contain sensitive metadata
- Unofficial API; use at your own risk

## Layout

```
kindle/
  index.html              # Optional localhost UI
  extension/
    dashboard.html        # Primary UI (recommended)
    background.js         # Auth + sync
    content.js            # Fetches from read.amazon.com tab
    js/ css/              # Symlinks to ../js and ../css
```

History builds over time: each **Refresh** adds a point when progress changed. The step chart shows plateaus between sessions and jumps while reading.
