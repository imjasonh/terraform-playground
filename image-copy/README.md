# `image-copy`

This sets up a Cloud Run app to listen for `registry.push` events to a private Chainguard Registry group, and mirrors those new images to a repository in Google Artifact Registry.

The Terraform does everything:

- builds the mirroring app into an image using `ko_build`
- deploys the app to a Cloud Run service
- sets up a Chainguard Identity with permissions to pull from the private cgr.dev repo
- allows the Cloud Run service's SA to assume the puller identity
- sets up a subscription to notify the Cloud Run service when pushes happen to cgr.dev

# Setup

```sh
gcloud auth application-default login
chainctl auth login
terraform init
terraform apply
```

This will prompt for a group ID and destination repo, and show you the resources it will create.

When the resources are created, any images that are pushed to your group will be mirrored to the `gcr.io/<project-id>/<dst-repo>` repository.

The Cloud Run service account has minimal permissions: it's only allowed to push images to the destination repo.

The Chainguard identity also has minimal permissions: it only has permission to pull from the source repo.

To tear down resources, run `terraform destroy`.
