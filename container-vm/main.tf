# Fetch the latest source image based on the input variables (defaulting to COS)
data "google_compute_image" "source_image" {
  family  = var.source_image_family
  project = var.source_image_project
}

# Render the cloud-init YAML template using dynamic module inputs
locals {
  rendered_cloud_init = templatefile("${path.module}/cloud-init.yaml.tftpl", {
    containers = var.containers,
  })
}

# The Instance Template resource itself
resource "google_compute_instance_template" "container_vm_template" {
  project      = var.project_id
  name_prefix  = "systemd-container-tmpl-"
  description  = "Runs a container using Docker, managed by a systemd unit via cloud-init."
  machine_type = var.machine_type
  region       = var.region
  
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

  # Critical step: inject the cloud-init script via the 'user-data' metadata key.
  metadata = {
    user-data = local.rendered_cloud_init
  }

  scheduling {
    automatic_restart   = true
    on_host_maintenance = "MIGRATE"
  }
}

