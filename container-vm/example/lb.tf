# Health check for the backend service
resource "google_compute_health_check" "default" {
  name                = local.name
  check_interval_sec  = 5
  timeout_sec         = 5
  healthy_threshold   = 2
  unhealthy_threshold = 2

  http_health_check {
    port         = 80
    request_path = "/"
  }
}

# Unmanaged instance group for the single VM
resource "google_compute_instance_group" "default" {
  name = local.name
  zone = local.zone

  named_port {
    name = "http"
    port = 80
  }

  lifecycle {
    ignore_changes = [instances]
  }
}

# Manage instance group membership separately
resource "google_compute_instance_group_membership" "default" {
  instance_group = google_compute_instance_group.default.id
  instance       = google_compute_instance_from_template.instance.self_link
  zone           = local.zone
}

# Backend service
resource "google_compute_backend_service" "default" {
  name                  = local.name
  protocol              = "HTTP"
  port_name             = "http"
  timeout_sec           = 30
  load_balancing_scheme = "EXTERNAL_MANAGED"

  backend {
    group           = google_compute_instance_group.default.self_link
    balancing_mode  = "UTILIZATION"
    capacity_scaler = 1.0
  }

  health_checks = [google_compute_health_check.default.id]
}

# URL map
resource "google_compute_url_map" "default" {
  name            = local.name
  default_service = google_compute_backend_service.default.id
}

# HTTP proxy
resource "google_compute_target_http_proxy" "default" {
  name    = local.name
  url_map = google_compute_url_map.default.id
}

# Forwarding rule (creates external IP)
resource "google_compute_global_forwarding_rule" "default" {
  name                  = local.name
  target                = google_compute_target_http_proxy.default.id
  port_range            = "80"
  load_balancing_scheme = "EXTERNAL_MANAGED"
}

# Firewall rule to allow health checks from Google Cloud health checkers
resource "google_compute_firewall" "allow_health_checks" {
  name    = "allow-health-checks"
  network = "default"

  allow {
    protocol = "tcp"
    ports    = ["80"]
  }

  # Google Cloud health check IP ranges
  source_ranges = [
    "35.191.0.0/16",
    "130.211.0.0/22",
  ]

  target_tags = ["allow-health-check"]
}

# Output the load balancer IP
output "load_balancer_ip" {
  value       = google_compute_global_forwarding_rule.default.ip_address
  description = "External IP address of the load balancer"
}
