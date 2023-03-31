# `image-copy`

This sets up a Cloud Run app to listen for `registry.push` events to a private Chainguard Registry group, and mirrors those new images to a repository in Google Artifact Registry.

The Terraform does everything:

- builds the mirroring app into an image using `ko_build`
- deploys the app to a Cloud Run service
- sets up a Chainguard Identity with permissions to pull from the private cgr.dev repo
- allows the Cloud Run service's SA to assume the puller identity
- sets up a subscription to notify the Cloud Run service when pushes happen to cgr.dev
