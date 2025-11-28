# Fetch the latest source image based on the input variables (defaulting to COS)
data "google_compute_image" "source_image" {
  family  = var.source_image_family
  project = var.source_image_project
}

# Render the cloud-init YAML template using dynamic module inputs
locals {
  rendered_cloud_init = templatefile("${path.module}/cloud-init.yaml.tftpl", {
    container_name    = var.container_name,
    container_image   = var.container_image,
    restart_policy    = var.restart_policy,
    container_env     = var.container_env,
    container_command = var.container_command,
    container_args    = var.container_args,
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

  # Network configuration
  network_interface {
    network    = "default" 
    subnetwork = null      
    
    # Assign an external IP address (access_config is required for external IP)
    access_config {}
  }

  # Service Account configuration (needed for Google Cloud API access, including image pulls)
  service_account {
    email  = "default" # Use the project's default compute service account
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

