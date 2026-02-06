# Litestream on Cloud Storage + Cloud Run

This demonstrates running a Cloud Run service that uses the [Litestream VFS](https://litestream.io/guides/vfs/) to read and write a SQLite database directly through a replica in Google Cloud Storage.

## How it works

The application imports the Litestream Go library and registers its VFS with SQLite **in-process** using [`psanford/sqlite3vfs`](https://github.com/psanford/sqlite3vfs). There is no sidecar container, no `.so` extension to load, and no restore step on startup. The VFS:

- **Reads** database pages on-demand from GCS, with a local LRU cache
- **Writes** to a local buffer, then syncs LTX files to GCS on a configurable interval
- Eliminates cold-start delays from restoring the full database

When running locally (not on GCE), the app uses a plain local SQLite file with no VFS.

## Build requirements

This uses [`mattn/go-sqlite3`](https://github.com/mattn/go-sqlite3), a CGO-based SQLite driver, because the VFS registration requires CGO. The `ko` build is configured with `CGO_ENABLED=1` and uses [zig](https://ziglang.org/) as a cross-compiler (`CC=zig cc -target x86_64-linux-gnu`) to build for Linux from macOS.

The `vfs` build tag is required to compile the Litestream VFS code (`GOFLAGS=-tags=vfs`).

## Limitations

**Single writer only.** The Litestream VFS write mode uses optimistic conflict detection, not distributed locking. The Cloud Run service is configured with `max_instance_count = 1`. Running multiple writers will result in conflicts.

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

Since the database is just SQLite, it's very easy to run this locally. Simply `go run -tags vfs ./` and the service will create `db.sqlite` in the current directory (without VFS — the VFS is only used when running on GCE). You can browse to http://localhost:8080/ to see the service running, and interact with the database using standard tools.
