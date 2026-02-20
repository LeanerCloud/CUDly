# ==============================================
# Networking
# ==============================================

module "networking" {
  source = "../../modules/networking/gcp"

  project_id            = var.project_id
  service_name          = local.service_name
  region                = var.region
  subnet_cidr           = var.subnet_cidr
  connector_subnet_cidr = var.connector_subnet_cidr
  enable_nat_logging    = var.enable_nat_logging

  connector_machine_type  = var.connector_machine_type
  connector_min_instances = var.connector_min_instances
  connector_max_instances = var.connector_max_instances
}
