# Litestream on Cloud Storage + Cloud Run

This demonstrates running a Cloud Run service that uses [Litestream](https://litestream.io) to backup a SQLite database to a Cloud Storage bucket.

It does this by running the Litestream image as a sidecar container in the same pod as the application container, with a shared volume mounted between them.

The application container restores the database from the GCS on startup, and Litestream continuously replicates changes to GCS in the background. Instances can scale to zero, and the database will be persisted across restarts.

## Limitations

This hasn't been tested with multiple container instances, and may not work as expected. For now it's limited to one container instance, which can handle 1000 concurrent requests. This may be enough for most apps.

## Why?

Cloud Storage is _really cheap_ -- much cheaper than most alternatives:

### [Firestore](https://cloud.google.com/datastore/pricing#regional_location_pricing)

- storage: $0.15 per GB/month
- reads: $0.03 per 100,000 entities
- writes: $0.09 per 100,000 entities

### [Cloud SQL](https://cloud.google.com/sql/pricing) (MySQL or PostgreSQL)

- storage (SSD): $0.222 per GB/month
- CPU: $32.266 per vCPU/month
- memory: $5.475 per GB/month

### [Cloud Storage](https://cloud.google.com/storage/pricing)

- storage: $0.02 per GB/month
- reads (class B operations): $0.04 per 100,000 operations\*
- writes (class A operations): $0.50 per 100,000 operations

\* Because Litestream only reads and writes to restore and replicate, these operations don't indicate the number of actual reads/writes to the database itself. Reading from the database is a local operation once the data is restored.

Since it's just SQLite, this also supports standard SQL operations and semantics, and can be trivially tested locally without access to a cloud instance.

There are also potentially tenancy benefits to using a separate database for each user, which can be easily achieved with multiple SQLite instances.
