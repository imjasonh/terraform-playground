resource "google_secret_manager_secret" "github_app_key" {
  project   = var.project_id
  secret_id = "driftlessaf-github-app-private-key"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "github_app_key_initial" {
  secret      = google_secret_manager_secret.github_app_key.id
  secret_data = "populate this secret with the GitHub App private key"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret_iam_member" "reconciler_reads_github_app_key" {
  secret_id = google_secret_manager_secret.github_app_key.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.reconciler.email}"
}
