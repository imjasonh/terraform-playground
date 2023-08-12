terraform {
  required_providers {
    cloudflare = { source = "cloudflare/cloudflare" }
  }
}

variable "name" { type = string }
variable "cloudflare_zone" { type = string }
variable "cloudflare_api_token" { type = string }
variable "cloudflare_account_id" { type = string }

provider "cloudflare" {
  api_token = var.cloudflare_api_token
}

// TODO: need to manually enable the workers.dev route in the dashboard
// use cloudflare_worker_route instead to set up routing
resource "cloudflare_worker_script" "script" {
  account_id = var.cloudflare_account_id
  name       = var.name
  content    = file("script.js")

  module = true

  kv_namespace_binding {
    name         = "KV"
    namespace_id = cloudflare_workers_kv_namespace.kv_ns.id
  }

  r2_bucket_binding {
    name        = "R2"
    bucket_name = cloudflare_r2_bucket.bucket.name
  }
}

resource "cloudflare_r2_bucket" "bucket" {
  account_id = var.cloudflare_account_id
  name       = "${var.name}-bucket"
  location   = "ENAM"
}

/*
resource "cloudflare_worker_route" "route" {
  zone_id     = data.cloudflare_zone.zone.id
  pattern     = "${var.name}.${var.cloudflare_zone}/*"
  script_name = cloudflare_worker_script.script.name
}

data "cloudflare_zone" "zone" {
  name = var.cloudflare_zone
}
*/

resource "cloudflare_workers_kv_namespace" "kv_ns" {
  account_id = var.cloudflare_account_id
  title      = "${var.name}-namespace"
}

// Set a value.
resource "cloudflare_workers_kv" "kv" {
  account_id   = var.cloudflare_account_id
  namespace_id = cloudflare_workers_kv_namespace.kv_ns.id
  key          = "foo"
  value        = "bar"
}

