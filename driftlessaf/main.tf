terraform {
  required_providers {
    ko     = { source = "ko-build/ko" }
    google = { source = "hashicorp/google" }
  }
}

provider "ko" {
  repo = "gcr.io/${var.project_id}/driftlessaf"
}

provider "google" {
  project = var.project_id
}
