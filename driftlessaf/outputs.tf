output "github_webhook_urls" {
  description = "The URL to configure as the GitHub App webhook endpoint."
  value       = module.github-events.public-urls
}

output "secret_command" {
  description = "Command to populate the GitHub App private key secret."
  value       = "gcloud secrets versions add ${google_secret_manager_secret.github_app_key.secret_id} --project=${var.project_id} --data-file=path/to/private-key.pem"
}

output "webhook_secret_command" {
  description = "Command to populate the GitHub webhook secret."
  value       = "echo -n '<secret>' | gcloud secrets versions add driftlessaf-webhook-secret --project=${var.project_id} --data-file=-"
}
