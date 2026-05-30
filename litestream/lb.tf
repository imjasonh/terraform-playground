// HTTP load balancer: GET /* -> reader, POST /click -> writer (same hostname for htmx).

data "google_project" "project" {
  project_id = local.project_id
}

resource "google_compute_global_address" "litestream" {
  name = "litestream-lb"
}

resource "google_compute_region_network_endpoint_group" "reader" {
  name                  = "litestream-reader-neg"
  region                = local.region
  network_endpoint_type = "SERVERLESS"

  cloud_run {
    service = google_cloud_run_v2_service.reader.name
  }
}

resource "google_compute_region_network_endpoint_group" "writer" {
  name                  = "litestream-writer-neg"
  region                = local.region
  network_endpoint_type = "SERVERLESS"

  cloud_run {
    service = google_cloud_run_v2_service.writer.name
  }
}

resource "google_compute_backend_service" "reader" {
  name        = "litestream-reader-backend"
  protocol    = "HTTP"
  port_name   = "http"
  timeout_sec = 30

  backend {
    group = google_compute_region_network_endpoint_group.reader.id
  }
}

resource "google_compute_backend_service" "writer" {
  name        = "litestream-writer-backend"
  protocol    = "HTTP"
  port_name   = "http"
  timeout_sec = 30

  backend {
    group = google_compute_region_network_endpoint_group.writer.id
  }
}

resource "google_compute_url_map" "litestream" {
  name            = "litestream-url-map"
  default_service = google_compute_backend_service.reader.id

  host_rule {
    hosts        = ["*"]
    path_matcher = "paths"
  }

  path_matcher {
    name            = "paths"
    default_service = google_compute_backend_service.reader.id

    path_rule {
      paths   = ["/click"]
      service = google_compute_backend_service.writer.id
    }
  }
}

resource "google_compute_target_http_proxy" "litestream" {
  name    = "litestream-http-proxy"
  url_map = google_compute_url_map.litestream.id
}

resource "google_compute_global_forwarding_rule" "litestream" {
  name       = "litestream-http-forwarding"
  target     = google_compute_target_http_proxy.litestream.id
  port_range = "80"
  ip_address = google_compute_global_address.litestream.address
}

// Allow the external HTTP load balancer to invoke Cloud Run.
resource "google_cloud_run_v2_service_iam_member" "reader_lb" {
  name     = google_cloud_run_v2_service.reader.name
  location = google_cloud_run_v2_service.reader.location
  role     = "roles/run.invoker"
  member   = "serviceAccount:${data.google_project.project.number}-compute@developer.gserviceaccount.com"
}

resource "google_cloud_run_v2_service_iam_member" "writer_lb" {
  name     = google_cloud_run_v2_service.writer.name
  location = google_cloud_run_v2_service.writer.location
  role     = "roles/run.invoker"
  member   = "serviceAccount:${data.google_project.project.number}-compute@developer.gserviceaccount.com"
}
