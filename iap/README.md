# GCP Identity-Aware Proxy + Cloud Run

This is a simple example of how to use GCP Identity-Aware Proxy (IAP) to protect a Cloud Run service.

This sets up a GCLB and public hostname for the IAP-protected Cloud Run service.

Only certain users can access the Cloud Run service via the IAP-protected URL.

When those users access the service, it receives a signed token that can be used to verify the user's identity.
