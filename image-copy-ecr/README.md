# `image-copy-ecr`

This sets up a Lambda function to listen for `registry.push` events to a private Chainguard Registry group, and mirrors those new images to a repository in Elastic Container Registry.

The Terraform does everything:

- builds the mirroring app into an image using `ko_build`
- deploys the app to a Lambda function
- sets up a Chainguard Identity with permissions to pull from the private cgr.dev repo
- allows the Lambda function to assume the puller identity
- sets up a subscription to notify the Lambda function when pushes happen to cgr.dev
