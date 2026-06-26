#!/usr/bin/env node
// Tiny zero-dependency static file server for local development.
//   node server.js            -> serves on http://localhost:8080
//   PORT=3000 node server.js  -> custom port
// Only Node built-ins are used, so there is nothing to install.

import http from "node:http";
import { readFile, stat } from "node:fs/promises";
import { join, normalize, extname } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = fileURLToPath(new URL(".", import.meta.url));
const START_PORT = Number(process.env.PORT) || 8080;

const MIME = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".mjs": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".svg": "image/svg+xml",
  ".ico": "image/x-icon",
  ".png": "image/png",
  ".map": "application/json; charset=utf-8",
};

const server = http.createServer(async (req, res) => {
  try {
    const url = new URL(req.url, "http://localhost");
    let pathname = decodeURIComponent(url.pathname);
    if (pathname === "/") pathname = "/index.html";

    // Resolve within ROOT and reject path traversal.
    const filePath = normalize(join(ROOT, pathname));
    if (!filePath.startsWith(ROOT)) {
      res.writeHead(403).end("Forbidden");
      return;
    }

    const info = await stat(filePath).catch(() => null);
    if (!info || !info.isFile()) {
      res.writeHead(404, { "Content-Type": "text/plain" }).end("Not found");
      return;
    }

    const body = await readFile(filePath);
    res.writeHead(200, {
      "Content-Type": MIME[extname(filePath)] || "application/octet-stream",
      "Cache-Control": "no-cache",
    });
    res.end(body);
  } catch (err) {
    res.writeHead(500, { "Content-Type": "text/plain" }).end("Server error: " + err.message);
  }
});

function listen(port, attemptsLeft = 10) {
  server.once("error", (err) => {
    if (err.code === "EADDRINUSE" && attemptsLeft > 0) {
      listen(port + 1, attemptsLeft - 1);
    } else {
      console.error(err);
      process.exit(1);
    }
  });
  server.listen(port, () => {
    console.log(`\n  ☸  kubescheduler — the game`);
    console.log(`  →  http://localhost:${port}\n`);
  });
}

listen(START_PORT);
