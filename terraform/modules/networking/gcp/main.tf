# GCP VPC Module with Serverless VPC Access
# Creates VPC and connector for Cloud Run to access Cloud SQL

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

# ==============================================
# VPC Network
# ==============================================

resource "google_compute_network" "main" {
  name                    = "${var.service_name}-vpc"
  auto_create_subnetworks = false
  project                 = var.project_id
}

# ==============================================
# Subnets
# ==============================================

resource "google_compute_subnetwork" "private" {
  name          = "${var.service_name}-private-subnet"
  ip_cidr_range = var.subnet_cidr
  region        = var.region
  network       = google_compute_network.main.id
  project       = var.project_id

  # Private Google Access (for Cloud SQL, Secret Manager, etc.)
  private_ip_google_access = true

  # Secondary ranges for GKE (if needed in future)
  dynamic "secondary_ip_range" {
    for_each = var.secondary_ranges
    content {
      range_name    = secondary_ip_range.value.name
      ip_cidr_range = secondary_ip_range.value.cidr
    }
  }
}

# ==============================================
# Cloud Router and NAT (for internet access)
# ==============================================

resource "google_compute_router" "main" {
  name    = "${var.service_name}-router"
  region  = var.region
  network = google_compute_network.main.id
  project = var.project_id
}

resource "google_compute_router_nat" "main" {
  name                               = "${var.service_name}-nat"
  router                             = google_compute_router.main.name
  region                             = var.region
  nat_ip_allocate_option             = "AUTO_ONLY"
  source_subnetwork_ip_ranges_to_nat = "ALL_SUBNETWORKS_ALL_IP_RANGES"
  project                            = var.project_id

  log_config {
    enable = var.enable_nat_logging
    filter = "ERRORS_ONLY"
  }
}

# ==============================================
# Serverless VPC Access Connector
# ==============================================

resource "google_vpc_access_connector" "main" {
  name    = "${var.service_name}-connector"
  region  = var.region
  project = var.project_id

  # Subnet for connector
  subnet {
    name       = google_compute_subnetwork.connector.name
    project_id = var.project_id
  }

  # Machine type and scaling
  machine_type  = var.connector_machine_type
  min_instances = var.connector_min_instances
  max_instances = var.connector_max_instances
}

# Dedicated subnet for VPC Access Connector
resource "google_compute_subnetwork" "connector" {
  name          = "${var.service_name}-connector-subnet"
  ip_cidr_range = var.connector_subnet_cidr
  region        = var.region
  network       = google_compute_network.main.id
  project       = var.project_id
}

# ==============================================
# Private Service Connection (for Cloud SQL)
# ==============================================

resource "google_compute_global_address" "private_ip_address" {
  name          = "${var.service_name}-private-ip"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  network       = google_compute_network.main.id
  project       = var.project_id
}

resource "google_service_networking_connection" "private_vpc_connection" {
  network                 = google_compute_network.main.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private_ip_address.name]
}

# ==============================================
# Firewall Rules
# ==============================================

# Allow internal traffic
resource "google_compute_firewall" "allow_internal" {
  name    = "${var.service_name}-allow-internal"
  network = google_compute_network.main.name
  project = var.project_id

  allow {
    protocol = "tcp"
    ports    = ["0-65535"]
  }

  allow {
    protocol = "udp"
    ports    = ["0-65535"]
  }

  allow {
    protocol = "icmp"
  }

  source_ranges = [var.subnet_cidr, var.connector_subnet_cidr]
}

# Allow health checks from Google
resource "google_compute_firewall" "allow_health_checks" {
  name    = "${var.service_name}-allow-health-checks"
  network = google_compute_network.main.name
  project = var.project_id

  allow {
    protocol = "tcp"
  }

  # Google's health check IP ranges
  source_ranges = [
    "35.191.0.0/16",
    "130.211.0.0/22"
  ]
}

# Allow SSH from IAP (for debugging instances if needed)
resource "google_compute_firewall" "allow_iap_ssh" {
  count = var.enable_iap_ssh ? 1 : 0

  name    = "${var.service_name}-allow-iap-ssh"
  network = google_compute_network.main.name
  project = var.project_id

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  # IAP's IP range
  source_ranges = ["35.235.240.0/20"]
}
