# apko + kind + helm

This demonstrates gluing together a few TF providers to:

1. build an `nginx` image using [`apko`](https://apko.dev), similar to how `cgr.dev/chainguard/nginx` is built
2. create a local [KinD](https://kind.sigs.k8s.io/) cluster
3. install a Helm chart for `nginx`, using our newly-built image
  - the installation waits for the deployment to become `Ready` before proceeding

This demonstrates how we can build and immediately smoke-test the image, before potentially tagging the image using [`oci_tag`](https://registry.terraform.io/providers/chainguard-dev/oci/latest/docs/resources/tag)
