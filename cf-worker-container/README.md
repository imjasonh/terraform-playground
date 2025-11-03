# Cloudflare Workers Containers

This is an experiment using Cloudflare Workers Containers to host a Go application on Workers.

Findings:

- You can't just write Go, you also need to write a simple [`index.js`](./index.js) that handles the Worker request and forwards it to the Container durable object
  - On the plus side, that also means it can forward to _multiple_ containers, which could be interesting
- You can't just give it a pre-built image; Cloudflare wants to either build it from a `Dockerfile` and your source, or a _publicly pullable_ image
  - Because Cloudflare wants to do the build, you can't even just do `FROM <ko-built-image>` if that image is private :sob:
- R2 integration is very DIY; regular Workers have a nice clean API for R2 access, but Containers need to be passed regular API creds via env
- Monitoring and observability of Containers seem pretty limited
  - The Worker wrapper logs are available, and the Container logs are available via DO, but Container startup logs seem to go into a black hole, which is frustrating when you're debugging startup issues
- I couldn't see any Terraform support, only declarative `wrangler deploy`

Due to these limitations, I don't see a lot of upside to using CF Containers over something like Cloud Run, even if it might cost a bit more. :shrug:
