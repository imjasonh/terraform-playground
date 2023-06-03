terraform {
  required_providers {
    apko = { source = "chainguard-dev/apko" }
    kind = { source = "tehcyx/kind" }
    helm = { source = "hashicorp/helm" }
  }
}

provider "apko" {
  extra_repositories = ["https://packages.wolfi.dev/os"]
  extra_keyring      = ["https://packages.wolfi.dev/os/wolfi-signing.rsa.pub"]
  default_archs      = ["x86_64", "aarch64"]
}

data "apko_config" "nginx" {
  config_contents = file("nginx.apko.yaml")
}

resource "apko_build" "nginx" {
  repo   = "ttl.sh/nginx"
  config = data.apko_config.nginx.config
}

provider "kind" {}

resource "kind_cluster" "default" {
  name = "tf-cluster"
}

provider "helm" {
  kubernetes {
    config_path = resource.kind_cluster.default.kubeconfig_path
  }
}

resource "helm_release" "nginx" {
  name = "nginx"

  repository = "https://charts.bitnami.com/bitnami"
  chart      = "nginx"

  set {
    name  = "nginx.image"
    value = apko_build.nginx.image_ref
  }
}

output "status" {
  value = resource.helm_release.nginx.status
}
