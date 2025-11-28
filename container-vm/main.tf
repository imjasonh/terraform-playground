# Fetch the latest source image based on the input variables (defaulting to COS)
data "google_compute_image" "source_image" {
  family  = var.source_image_family
  project = var.source_image_project
}

# Render the cloud-init YAML template using dynamic module inputs
locals {
  rendered_cloud_init = templatefile("${path.module}/cloud-init.yaml.tftpl", {
    containers     = var.containers,
    enable_logging = var.enable_logging,
    project_id     = var.project_id,
  })
}

# The Instance Template resource itself
resource "google_compute_instance_template" "container_vm_template" {
  project      = var.project_id
  name_prefix  = "systemd-container-tmpl-"
  description  = "Runs a container using Docker, managed by a systemd unit via cloud-init."
  machine_type = var.machine_type
  region       = var.region
  labels       = var.labels
  
  # Disk configuration using the fetched OS image
  disk {
    source_image = data.google_compute_image.source_image.self_link
    auto_delete  = true
    boot         = true
  }

  # Network configuration (private VM - no external IP)
  network_interface {
    network    = var.network
    subnetwork = var.subnetwork
  }

  # Service Account configuration (needed for Google Cloud API access, including image pulls)
  service_account {
    email  = var.service_account_email
    scopes = ["cloud-platform"]
  }

  # Metadata for cloud-init and observability
  metadata = {
    user-data                  = local.rendered_cloud_init
    google-logging-enabled     = var.enable_logging
    google-monitoring-enabled  = var.enable_monitoring
    cos-metrics-enabled        = var.enable_cos_metrics
  }

  scheduling {
    automatic_restart   = true
    on_host_maintenance = "MIGRATE"
  }

  # Enable shielded VM features for security
  shielded_instance_config {
    enable_secure_boot          = true
    enable_vtpm                 = true
    enable_integrity_monitoring = true
  }
}

