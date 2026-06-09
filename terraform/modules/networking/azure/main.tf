# Azure Networking Module
# Provides VNet, subnets, NSGs, and Private DNS zones for Container Apps and PostgreSQL

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
  }
}

# ==============================================
# Virtual Network
# ==============================================

resource "azurerm_virtual_network" "main" {
  name                = "${var.app_name}-vnet"
  location            = var.location
  resource_group_name = var.resource_group_name
  address_space       = [var.vnet_cidr]

  tags = merge(var.tags, {
    environment = var.environment
    managed_by  = "terraform"
  })
}

# ==============================================
# Subnets
# ==============================================

# Subnet for Container App Environment infrastructure
resource "azurerm_subnet" "container_apps" {
  name                 = "${var.app_name}-container-apps-subnet"
  resource_group_name  = var.resource_group_name
  virtual_network_name = azurerm_virtual_network.main.name
  address_prefixes     = [var.container_apps_subnet_cidr]

  # Delegate to Container App Environment
  delegation {
    name = "container-apps-delegation"

    service_delegation {
      name    = "Microsoft.App/environments"
      actions = ["Microsoft.Network/virtualNetworks/subnets/join/action"]
    }
  }
}

# Subnet for PostgreSQL Flexible Server
resource "azurerm_subnet" "database" {
  name                 = "${var.app_name}-database-subnet"
  resource_group_name  = var.resource_group_name
  virtual_network_name = azurerm_virtual_network.main.name
  address_prefixes     = [var.database_subnet_cidr]

  # Delegate to PostgreSQL Flexible Server
  delegation {
    name = "postgres-delegation"

    service_delegation {
      name    = "Microsoft.DBforPostgreSQL/flexibleServers"
      actions = ["Microsoft.Network/virtualNetworks/subnets/join/action"]
    }
  }

  # Service endpoints for enhanced security
  service_endpoints = ["Microsoft.Storage"]
}

# Private subnet for other services (if needed)
resource "azurerm_subnet" "private" {
  count = var.create_private_subnet ? 1 : 0

  name                 = "${var.app_name}-private-subnet"
  resource_group_name  = var.resource_group_name
  virtual_network_name = azurerm_virtual_network.main.name
  address_prefixes     = [var.private_subnet_cidr]

  service_endpoints = [
    "Microsoft.Storage",
    "Microsoft.KeyVault",
    "Microsoft.ContainerRegistry"
  ]
}

# ==============================================
# Network Security Groups
# ==============================================

# NSG for Container Apps subnet
resource "azurerm_network_security_group" "container_apps" {
  name                = "${var.app_name}-container-apps-nsg"
  location            = var.location
  resource_group_name = var.resource_group_name

  tags = var.tags
}

# Allow HTTPS inbound to Container Apps
resource "azurerm_network_security_rule" "container_apps_https" {
  name                        = "AllowHTTPSInbound"
  priority                    = 100
  direction                   = "Inbound"
  access                      = "Allow"
  protocol                    = "Tcp"
  source_port_range           = "*"
  destination_port_range      = "443"
  source_address_prefix       = var.allow_inbound_from_internet ? "*" : "VirtualNetwork"
  destination_address_prefix  = "*"
  resource_group_name         = var.resource_group_name
  network_security_group_name = azurerm_network_security_group.container_apps.name
}

# Allow HTTP inbound to Container Apps (for internal health checks)
resource "azurerm_network_security_rule" "container_apps_http" {
  name                        = "AllowHTTPInbound"
  priority                    = 110
  direction                   = "Inbound"
  access                      = "Allow"
  protocol                    = "Tcp"
  source_port_range           = "*"
  destination_port_range      = "80"
  source_address_prefix       = "VirtualNetwork"
  destination_address_prefix  = "*"
  resource_group_name         = var.resource_group_name
  network_security_group_name = azurerm_network_security_group.container_apps.name
}

# Associate NSG with Container Apps subnet
resource "azurerm_subnet_network_security_group_association" "container_apps" {
  subnet_id                 = azurerm_subnet.container_apps.id
  network_security_group_id = azurerm_network_security_group.container_apps.id
}

# NSG for Database subnet
resource "azurerm_network_security_group" "database" {
  name                = "${var.app_name}-database-nsg"
  location            = var.location
  resource_group_name = var.resource_group_name

  tags = var.tags
}

# Allow PostgreSQL from Container Apps subnet
resource "azurerm_network_security_rule" "database_postgres" {
  name                        = "AllowPostgreSQLFromContainerApps"
  priority                    = 100
  direction                   = "Inbound"
  access                      = "Allow"
  protocol                    = "Tcp"
  source_port_range           = "*"
  destination_port_range      = "5432"
  source_address_prefix       = var.container_apps_subnet_cidr
  destination_address_prefix  = "*"
  resource_group_name         = var.resource_group_name
  network_security_group_name = azurerm_network_security_group.database.name
}

# Allow PostgreSQL from private subnet (if exists)
resource "azurerm_network_security_rule" "database_postgres_private" {
  count = var.create_private_subnet ? 1 : 0

  name                        = "AllowPostgreSQLFromPrivate"
  priority                    = 110
  direction                   = "Inbound"
  access                      = "Allow"
  protocol                    = "Tcp"
  source_port_range           = "*"
  destination_port_range      = "5432"
  source_address_prefix       = var.private_subnet_cidr
  destination_address_prefix  = "*"
  resource_group_name         = var.resource_group_name
  network_security_group_name = azurerm_network_security_group.database.name
}

# Associate NSG with Database subnet
resource "azurerm_subnet_network_security_group_association" "database" {
  subnet_id                 = azurerm_subnet.database.id
  network_security_group_id = azurerm_network_security_group.database.id
}

# NSG for Private subnet (if created)
resource "azurerm_network_security_group" "private" {
  count = var.create_private_subnet ? 1 : 0

  name                = "${var.app_name}-private-nsg"
  location            = var.location
  resource_group_name = var.resource_group_name

  tags = var.tags
}

resource "azurerm_subnet_network_security_group_association" "private" {
  count = var.create_private_subnet ? 1 : 0

  subnet_id                 = azurerm_subnet.private[0].id
  network_security_group_id = azurerm_network_security_group.private[0].id
}

# ==============================================
# Private DNS Zone for PostgreSQL
# ==============================================

resource "azurerm_private_dns_zone" "postgres" {
  name                = "privatelink.postgres.database.azure.com"
  resource_group_name = var.resource_group_name

  tags = var.tags
}

resource "azurerm_private_dns_zone_virtual_network_link" "postgres" {
  name                  = "${var.app_name}-postgres-vnet-link"
  resource_group_name   = var.resource_group_name
  private_dns_zone_name = azurerm_private_dns_zone.postgres.name
  virtual_network_id    = azurerm_virtual_network.main.id
  registration_enabled  = false

  tags = var.tags
}

# ==============================================
# Route Tables (if custom routing needed)
# ==============================================

resource "azurerm_route_table" "main" {
  count = var.create_route_table ? 1 : 0

  name                          = "${var.app_name}-route-table"
  location                      = var.location
  resource_group_name           = var.resource_group_name
  bgp_route_propagation_enabled = true

  tags = var.tags
}

# Associate route table with private subnet
resource "azurerm_subnet_route_table_association" "private" {
  count = var.create_route_table && var.create_private_subnet ? 1 : 0

  subnet_id      = azurerm_subnet.private[0].id
  route_table_id = azurerm_route_table.main[0].id
}

# ==============================================
# Log Analytics Workspace (for diagnostics)
# ==============================================

resource "azurerm_log_analytics_workspace" "main" {
  count = var.create_log_analytics ? 1 : 0

  name                = "${var.app_name}-logs"
  location            = var.location
  resource_group_name = var.resource_group_name
  sku                 = "PerGB2018"
  retention_in_days   = var.log_retention_days

  tags = merge(var.tags, {
    environment = var.environment
    managed_by  = "terraform"
  })
}

# ==============================================
# Network Watcher (for network diagnostics)
# ==============================================

resource "azurerm_network_watcher" "main" {
  count = var.create_network_watcher ? 1 : 0

  name                = "${var.app_name}-network-watcher"
  location            = var.location
  resource_group_name = var.resource_group_name

  tags = var.tags
}
