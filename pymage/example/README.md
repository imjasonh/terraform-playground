# pymage example

A small [uv](https://docs.astral.sh/uv/) project with a realistic FastAPI dependency tree (~28 wheels). Use it to try pymage end-to-end.

## Build

From the `pymage` module root (network required on first build to download wheels):

```bash
cd pymage
go run . build ./example \
  --push=false \
  --print-digest
```

Push to a registry. The base image and target platforms come from the
`[tool.pymage]` table in [`pyproject.toml`](./pyproject.toml); supply the
destination repo with `--repo` (or uncomment `repo` in that table) and the tag
with `-t`:

```bash
go run . build ./example --repo registry.example.com/me/example -t latest
```

Defaults apply automatically:

- `uv.lock` in the source directory (the positional arg)
- `[tool.pymage]` base (`cgr.dev/chainguard/python:latest`, Python 3.14) and
  platforms (`linux/amd64`, `linux/arm64`)
- `[project.scripts]` entrypoint (`example`)
- `PYTHONPATH=/app/src` for the src layout

## Run locally with uv

```bash
cd example
uv sync
uv run example
curl localhost:8080/healthz
```
