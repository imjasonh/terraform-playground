# Service account for Cloud Run
resource "google_service_account" "step_ca" {
  account_id   = "step-ca"
  display_name = "step-ca Cloud Run Service Account"
}

# Grant Cloud SQL client permissions
resource "google_project_iam_member" "step_ca_sql_client" {
  project = var.project_id
  role    = "roles/cloudsql.client"
  member  = "serviceAccount:${google_service_account.step_ca.email}"
}

# Cloud Run service
resource "google_cloud_run_v2_service" "step_ca" {
  name     = "step-ca"
  location = var.region

  template {
    service_account = google_service_account.step_ca.email

    vpc_access {
      connector = google_vpc_access_connector.connector.id
      egress    = "PRIVATE_RANGES_ONLY"
    }

    containers {
      image = "smallstep/step-ca:latest"

      ports {
        container_port = 9000
      }


      env {
        name  = "STEPDEBUG"
        value = "1"
      }

      env {
        name  = "STEPPATH"
        value = "/home/step"
      }

      env {
        name  = "STEP_TLS_INSECURE"
        value = "true"
      }

      env {
        name  = "CONFIG_HASH"
        value = filesha256("${path.module}/secrets/step-ca-config")
      }

      # Mount secrets as volumes
      volume_mounts {
        name       = "root-ca"
        mount_path = "/mnt/root-ca"
      }

      volume_mounts {
        name       = "intermediate-crt"
        mount_path = "/mnt/intermediate-crt"
      }

      volume_mounts {
        name       = "intermediate-key"
        mount_path = "/mnt/intermediate-key"
      }

      volume_mounts {
        name       = "ssh-host-key"
        mount_path = "/mnt/ssh-host-key"
      }

      volume_mounts {
        name       = "ssh-user-key"
        mount_path = "/mnt/ssh-user-key"
      }

      volume_mounts {
        name       = "password"
        mount_path = "/mnt/password"
      }

      volume_mounts {
        name       = "ca-config"
        mount_path = "/home/step/config"
      }

      command = ["/bin/sh", "-c"]
      args    = ["mkdir -p /home/step/secrets && cp /mnt/*/* /home/step/secrets/ && tr -d '\\n' < /home/step/secrets/password > /tmp/pass && mv /tmp/pass /home/step/secrets/password && echo 'Files copied, password stripped' && ls -la /home/step/secrets/ && echo 'Password length:' && wc -c /home/step/secrets/password && /usr/local/bin/step-ca /home/step/config/ca.json"]
    }

    volumes {
      name = "root-ca"
      secret {
        secret       = google_secret_manager_secret.secret["step-root-ca"].secret_id
        default_mode = 0444
        items {
          version = "latest"
          path    = "root_ca.crt"
        }
      }
    }

    volumes {
      name = "intermediate-crt"
      secret {
        secret       = google_secret_manager_secret.secret["step-intermediate-crt"].secret_id
        default_mode = 0444
        items {
          version = "latest"
          path    = "intermediate_ca.crt"
        }
      }
    }

    volumes {
      name = "intermediate-key"
      secret {
        secret       = google_secret_manager_secret.secret["step-intermediate-key"].secret_id
        default_mode = 0400
        items {
          version = "latest"
          path    = "intermediate_ca_key"
        }
      }
    }

    volumes {
      name = "ssh-host-key"
      secret {
        secret       = google_secret_manager_secret.secret["step-ssh-host-ca-key"].secret_id
        default_mode = 0400
        items {
          version = "latest"
          path    = "ssh_host_ca_key"
        }
      }
    }

    volumes {
      name = "ssh-user-key"
      secret {
        secret       = google_secret_manager_secret.secret["step-ssh-user-ca-key"].secret_id
        default_mode = 0400
        items {
          version = "latest"
          path    = "ssh_user_ca_key"
        }
      }
    }

    volumes {
      name = "password"
      secret {
        secret       = google_secret_manager_secret.secret["step-intermediate-password"].secret_id
        default_mode = 0400
        items {
          version = "latest"
          path    = "password"
        }
      }
    }

    volumes {
      name = "ca-config"
      secret {
        secret       = google_secret_manager_secret.secret["step-ca-config"].secret_id
        default_mode = 0444
        items {
          version = "latest"
          path    = "ca.json"
        }
      }
    }
  }

  depends_on = [
    google_project_iam_member.step_ca_sql_client,
    google_secret_manager_secret_iam_member.secret_access,
  ]
}

# Make the service publicly accessible
resource "google_cloud_run_v2_service_iam_member" "public_access" {
  name     = google_cloud_run_v2_service.step_ca.name
  location = google_cloud_run_v2_service.step_ca.location
  role     = "roles/run.invoker"
  member   = "allUsers"
}

output "step_ca_url" {
  value       = google_cloud_run_v2_service.step_ca.uri
  description = "The URL of the step-ca Cloud Run service"
}

output "cloud_sql_connection_name" {
  value       = google_sql_database_instance.step_ca.connection_name
  description = "Cloud SQL connection name"
}

output "db_password" {
  value       = random_password.db_password.result
  description = "Database password for step user"
  sensitive   = true
}
