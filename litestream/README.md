# Litestream on Cloud Storage + Cloud Run

This demonstrates running a Cloud Run service that uses [Litestream](https://litestream.io) to backup a SQLite database to a Cloud Storage bucket.

It does this by running the Litestream image as a sidecar container in the same pod as the application container, with a shared volume mounted between them.

The application container restores the database from the GCS on startup, and Litestream continuously replicates changes to GCS in the background. Instances can scale to zero, and the database will be persisted across restarts.

This has been load-tested with up to 5000 qps and 60 concurrent Cloud Run instances, and worked mostly as expected. The only errors encountered were due to the database being locked by multiple writers in the same instance ([`SQLITE_BUSY`](https://www.sqlite.org/rescode.html#busy)), and due to Cloud Run not scaling up fast enough. This is not an endorsement of this solution for critical production use cases, your mileage may vary.

## Why?

Cloud Storage is _really cheap_ -- much cheaper than most alternatives:

### [Firestore](https://cloud.google.com/datastore/pricing#regional_location_pricing)

- storage: $0.15 per GB/month (first 1 GB/month free)
- reads: $0.03 per 100,000 entities (50,000 per day)
- writes: $0.09 per 100,000 entities (20,000 per day free)

### [Cloud SQL](https://cloud.google.com/sql/pricing) (MySQL or PostgreSQL)

- storage (SSD): $0.222 per GB/month
- CPU: $32.266 per vCPU/month
- memory: $5.475 per GB/month

### [Cloud Storage](https://cloud.google.com/storage/pricing) + Litestream

- storage: $0.02 per GB/month (first 5 GB/months free)
- reads (class B operations): $0.04 per 100,000 operations (50,000 monthly operations free)\*
- writes (class A operations): $0.50 per 100,000 operations (5,000 monthly operations free)

\* Because Litestream only reads and writes to restore and replicate, these operations don't indicate the number of actual reads/writes to the database itself. Reading from the database is a local operation once the data is restored.

When the service is not receiving any requests, you only pay for GCS storage, at a fraction of the cost of other solutions, making this a very cost-effective solution for infrequently-used applications.

Since it's just SQLite, this also supports standard SQL operations and semantics. It can be trivially tested locally without access to a cloud instance, or moved to other Litestream-compatible backends like S3.

There are also potentially tenancy benefits to using a separate database for each user, which can be easily achieved with multiple SQLite instances.
