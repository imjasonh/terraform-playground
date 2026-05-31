# Kindle Reading Progress

Client-side web app that plots Kindle reading progress **P(t)** as a **step chart** (flat plateaus between sessions, vertical jumps while reading). Optional Chrome extension automates session capture on [read.amazon.com](https://read.amazon.com).

## Quick start (chart only)

1. Serve this folder locally (extension messaging requires HTTP, not `file://`):

   ```bash
   cd kindle
   python3 -m http.server 8080
   ```

2. Open [http://localhost:8080](http://localhost:8080).

3. Paste JSON and click **Generate progress graph**.

### Supported JSON shapes

- Array of points: `{ "timestamp": "...", "progress": 45 }` (progress 0–100, or 0–1)
- Wrapper: `{ "progress_to_completion": [ ... ] }`
- Kindle API snapshot: `{ "percentageRead": 13.1, "progress": { "syncDate": "..." } }`
- Librera-style map: `{ "book.epub": { "t": 1565986186029, "p": 0.57 } }`

Use **days from start** on the X axis to match **t** in days from the first sync.

## Chrome extension (automated auth)

Amazon has no public Kindle API. Community tools reuse **browser session cookies** and a **device token** from Cloud Reader. This extension reads them via `chrome.cookies` and network hooks (no passwords stored).

### Install

1. Chrome → `chrome://extensions` → **Developer mode** → **Load unpacked** → select `kindle/extension`.
2. Copy the **extension ID** from the popup or extensions page.
3. Sign in at [read.amazon.com](https://read.amazon.com) (refresh once so `getDeviceToken` runs).
4. Open the dashboard at `http://localhost:8080`, paste the extension ID, click **Detect extension**.
5. **Sync from Kindle** appends current progress into browser history and the chart.

### Security

- Cookies stay in **extension storage** and your **browser localStorage** (history only, not raw cookies in the chart app).
- Never commit cookie values or share exported files that include credentials.
- Unofficial API use may conflict with Amazon’s terms of service; use at your own risk.

## Project layout

```
kindle/
  index.html          # Dashboard
  css/app.css
  js/
    parse.js          # Normalize JSON → points
    chart.js          # Chart.js stepped line
    storage.js        # localStorage history
    extension-bridge.js
    app.js
  extension/
    manifest.json
    background.js     # Cookies + device token + messaging
    content.js        # In-page fetch to Kindle library API
    popup.html
```

## Building reading history over time

Each **Sync** or **Save to browser history** appends a snapshot. Re-sync after reading sessions to grow a step curve. Export via **Export history** for backup.

## TLS / server-side note

Node clients (e.g. [kindle-api](https://github.com/transitive-bullshit/kindle-api)) often need a local TLS proxy because of Amazon fingerprinting. This project avoids that by running fetches **inside** the signed-in Cloud Reader tab via the content script.
