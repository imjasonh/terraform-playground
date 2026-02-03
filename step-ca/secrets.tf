# Secret Manager secrets for step-ca
# These will be populated by the bootstrap script

resource "google_secret_manager_secret" "secret" {
  for_each = toset([
    "step-root-ca",
    "step-intermediate-crt",
    "step-intermediate-key",
    "step-ssh-host-ca-key",
    "step-ssh-user-ca-key",
    "step-ca-config",
    "step-intermediate-password",
  ])
  secret_id = each.key

  replication {
    auto {}
  }
}

# Grant Cloud Run service account access to secrets
resource "google_secret_manager_secret_iam_member" "secret_access" {
  for_each  = google_secret_manager_secret.secret
  secret_id = each.value.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.step_ca.email}"
}

resource "google_secret_manager_secret_version" "secret_version" {
  for_each    = google_secret_manager_secret.secret
  secret      = each.value.id
  secret_data = file("${path.module}/secrets/${each.key}")
}
