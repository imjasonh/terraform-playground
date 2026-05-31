# Kindle Reading Progress

Chrome extension that charts your Kindle reading progress **P(t)** over time, synced automatically from your Amazon session. No JSON paste, no separate web server.

## Quick start

1. Chrome → `chrome://extensions` → **Developer mode** → **Load unpacked** → `kindle/extension`
2. Sign in at [read.amazon.com](https://read.amazon.com) and reload that tab once so the device token is captured
3. Click the extension icon → **Open dashboard**
4. Click **Refresh**. Snapshots accumulate over time; each refresh appends a point when progress changed

### If auth expires

- **Sign out** — clears the stored Amazon session; reading history is kept
- **Reset all data** — clears session and all saved snapshots
- Then **Connect Amazon** → open Cloud Reader → **Refresh**

## Security

- Cookies never leave your browser (`chrome.storage.local` only)
- Don't share exported history files if they contain sensitive metadata
- Unofficial Kindle API; use at your own risk

## Layout

```
kindle/
  README.md
  extension/
    manifest.json     # MV3 config
    dashboard.html    # Main UI
    popup.html/.js    # Toolbar popup
    background.js     # Auth capture, sync orchestration
    content.js        # Runs on read.amazon.com; fetches library/progress
    vendor/           # Bundled Chart.js (MV3 forbids remote scripts)
    js/               # Dashboard modules (app, chart, parse, storage, bridge)
    css/              # Dashboard styles
```

## Known limitations

`startReading` only returns Whispersync data for books the **web reader** has touched. Books read only on physical Kindle devices come back with `position: -1` and are filtered out of the chart. Finding a bulk-progress endpoint that exposes all devices is a TODO — see the comments in `content.js`.
