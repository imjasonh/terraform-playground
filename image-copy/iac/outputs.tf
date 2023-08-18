output "url" {
  value = google_cloud_run_service.image-copy.status[0].url
}

output "dst_repo" {
  value = "${var.region}-docker.pkg.dev/${var.project}/${var.dst_repo}"
}
