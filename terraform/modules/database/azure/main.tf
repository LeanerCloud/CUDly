# Azure PostgreSQL Flexible Server Module
# Managed PostgreSQL database with auto-scaling and high availability

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.0"
    }
  }
}

# ==============================================
# Database Password Generation
# ==============================================

resource "random_password" "database" {
  count = var.administrator_password == null ? 1 : 0

  length  = 32
  special = true
  override_special = "!#$%&*()-_=+[]{}<>:?"
}

# ==============================================
# PostgreSQL Flexible Server
# ==============================================

resource "azurerm_postgresql_flexible_server" "main" {
  name                = "${var.app_name}-postgres"
  resource_group_name = var.resource_group_name
  location            = var.location

  # Version
  version = var.postgres_version

  # Administrator credentials
  administrator_login    = var.administrator_login
  administrator_password = var.administrator_password != null ? var.administrator_password : random_password.database[0].result

  # SKU (size)
  sku_name   = var.sku_name
  storage_mb = var.storage_mb

  # Backup
  backup_retention_days        = var.backup_retention_days
  geo_redundant_backup_enabled = var.geo_redundant_backup_enabled

  # High Availability
  dynamic "high_availability" {
    for_each = var.high_availability_mode != "Disabled" ? [1] : []
    content {
      mode                      = var.high_availability_mode
      standby_availability_zone = var.standby_availability_zone
    }
  }

  # Maintenance window
  dynamic "maintenance_window" {
    for_each = var.maintenance_window != null ? [var.maintenance_window] : []
    content {
      day_of_week  = maintenance_window.value.day_of_week
      start_hour   = maintenance_window.value.start_hour
      start_minute = maintenance_window.value.start_minute
    }
  }

  # Networking - delegated subnet for private access
  delegated_subnet_id = var.delegated_subnet_id
  private_dns_zone_id = var.private_dns_zone_id

  # Public access
  public_network_access_enabled = var.public_network_access_enabled

  # Zone
  zone = var.availability_zone

  tags = merge(var.tags, {
    environment = var.environment
    managed_by  = "terraform"
  })
}

# ==============================================
# Database
# ==============================================

resource "azurerm_postgresql_flexible_server_database" "main" {
  name      = var.database_name
  server_id = azurerm_postgresql_flexible_server.main.id
  collation = "en_US.utf8"
  charset   = "UTF8"
}

# ==============================================
# Firewall Rules (if public access enabled)
# ==============================================

resource "azurerm_postgresql_flexible_server_firewall_rule" "allowed_ips" {
  for_each = var.public_network_access_enabled ? var.allowed_ip_ranges : {}

  name             = each.key
  server_id        = azurerm_postgresql_flexible_server.main.id
  start_ip_address = each.value.start_ip
  end_ip_address   = each.value.end_ip
}

# ==============================================
# Configuration Parameters
# ==============================================

resource "azurerm_postgresql_flexible_server_configuration" "config" {
  for_each = var.server_parameters

  name      = each.key
  server_id = azurerm_postgresql_flexible_server.main.id
  value     = each.value
}

# ==============================================
# Key Vault Secret for Password
# ==============================================

resource "azurerm_key_vault_secret" "db_password" {
  name         = "${var.app_name}-db-password"
  value        = var.administrator_password != null ? var.administrator_password : random_password.database[0].result
  key_vault_id = var.key_vault_id

  tags = merge(var.tags, {
    environment = var.environment
    managed_by  = "terraform"
  })
}

# ==============================================
# Diagnostic Settings (Optional)
# ==============================================

resource "azurerm_monitor_diagnostic_setting" "postgres" {
  count = var.log_analytics_workspace_id != null ? 1 : 0

  name                       = "${var.app_name}-postgres-diag"
  target_resource_id         = azurerm_postgresql_flexible_server.main.id
  log_analytics_workspace_id = var.log_analytics_workspace_id

  enabled_log {
    category = "PostgreSQLLogs"
  }

  metric {
    category = "AllMetrics"
    enabled  = true
  }
}
