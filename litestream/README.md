# Litestream on Cloud Storage + Cloud Run

This demonstrates running Cloud Run services that use the [Litestream VFS](https://litestream.io/guides/vfs/) to read and write a SQLite database directly through a replica in Google Cloud Storage.

Production traffic uses two services plus an HTTP load balancer:

- **`litestream-reader`** — read-only VFS (`LITESTREAM_WRITE_ENABLED=false`), serves `GET /`, scales out (up to 10 instances).
- **`litestream-writer`** — write-enabled VFS, serves `POST /click` only, single instance.
- **HTTP load balancer** — routes `/click` to the writer and all other paths to the reader so htmx can use relative URLs on one hostname (`lb_url` output).

## How it works

The application imports the Litestream Go library and registers its VFS with SQLite **in-process** using [`psanford/sqlite3vfs`](https://github.com/psanford/sqlite3vfs). There is no sidecar container, no `.so` extension to load, and no restore step on startup. The VFS:

- **Reads** database pages on-demand from GCS, with a local LRU cache
- **Writes** to a local buffer, then syncs LTX files to GCS on a configurable interval
- Eliminates cold-start delays from restoring the full database

When running locally (not on GCE), the app uses a plain local SQLite file with no VFS.

## Build requirements

This uses [`mattn/go-sqlite3`](https://github.com/mattn/go-sqlite3), a CGO-based SQLite driver, because the VFS registration requires CGO. The `ko` build is configured with `CGO_ENABLED=1` and uses [zig](https://ziglang.org/) as a cross-compiler (`CC=zig cc -target x86_64-linux-gnu`) to build for Linux from macOS.

The `vfs` build tag is required to compile the Litestream VFS code (`GOFLAGS=-tags=vfs`).

## Environment variables

| Variable | Reader | Writer |
|----------|--------|--------|
| `LITESTREAM_REPLICA_URL` | required | required |
| `LITESTREAM_WRITE_ENABLED` | `false` | `true` |
| `LITESTREAM_BUFFER_PATH` | — | path for write buffer (in-memory volume) |

After `terraform apply`, open the **`lb_url`** output for the UI. Direct `reader_url` / `writer_url` remain available for debugging.

Deploy the **writer** at least once before readers on a fresh bucket so the schema exists in the replica.

## Limitations

**Single writer only.** The Litestream VFS write mode uses optimistic conflict detection, not distributed locking. Only `litestream-writer` may have `LITESTREAM_WRITE_ENABLED=true`. Running multiple writers against the same replica will result in conflicts.

Reader counts may lag writes by about the VFS poll interval plus the writer sync interval (both default to 1 second).

The VFS write buffer is stored on an in-memory `empty_dir` volume, so it is lost when the instance scales to zero. The VFS will rebuild from the GCS replica on the next cold start, fetching pages on-demand rather than requiring a full restore.

## Why?

Cloud Storage is _really cheap_ -- much cheaper than most alternatives, especially since it scales to zero.

### [Firestore](https://cloud.google.com/datastore/pricing#regional_location_pricing)

- storage: $0.15 per GB/month (first 1 GB/month free)
- reads: $0.03 per 100,000 entities (50,000 per day)
- writes: $0.09 per 100,000 entities (20,000 per day free)

### [Cloud SQL](https://cloud.google.com/sql/pricing) (MySQL or PostgreSQL)

- storage (SSD): $0.222 per GB/month
- CPU: $32.266 per vCPU/month
- memory: $5.475 per GB/month

### [Cloud Storage](https://cloud.google.com/storage/pricing)

- storage: $0.02 per GB/month (first 5 GB/months free)
- reads (class B operations): $0.04 per 100,000 operations (50,000 monthly operations free)\*
- writes (class A operations): $0.50 per 100,000 operations (5,000 monthly operations free)

\* Because Litestream replicates at the page level, GCS operation counts don't directly correspond to application-level reads/writes.

When the service is not receiving any requests, you only pay for GCS storage, at a fraction of the cost of other solutions, making this a very cost-effective solution for infrequently-used applications.

Query costs are charged at [Cloud Run rates](https://cloud.google.com/run/pricing#tables) (since it's doing the querying), which comes out to:

- CPU: $46.656 per vCPU/month ($62.208 per vCPU/month when only allocated during requests)
- memory: $5.184 per GB/month ($6.48 per GB/month when only allocated during requests)

These costs are shown in monthly units, but are billed per 100 milliseconds of actual usage. Cloud Run costs include the SQLite queries and your application logic.

Since it's just SQLite, this also supports standard SQL operations and semantics. It can be trivially tested locally without access to a cloud instance, or moved to other Litestream-compatible backends like S3.

There are also potentially tenancy benefits to using a separate database for each user, which can be easily achieved with multiple SQLite instances.

### Running locally

Since the database is just SQLite, it's very easy to run this locally. `go run -tags vfs ./` starts a single process with both `GET /` and `POST /click` (no VFS on laptop — VFS is only used on GCE). Browse to http://localhost:8080/.
