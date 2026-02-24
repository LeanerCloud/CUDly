# Azure Frontend Module - Azure CDN + Blob Storage
# Serves static files from Blob Storage and proxies /api requests to Container Apps

terraform {
  required_version = ">= 1.0"
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
  }
}

# Storage account for frontend files
resource "azurerm_storage_account" "frontend" {
  name                     = var.storage_account_name
  resource_group_name      = var.resource_group_name
  location                 = var.location
  account_tier             = "Standard"
  account_replication_type = "GRS"
  account_kind             = "StorageV2"

  # Enable static website hosting
  static_website {
    index_document     = "index.html"
    error_404_document = "index.html" # SPA routing
  }

  # Security settings
  allow_nested_items_to_be_public = false
  https_traffic_only_enabled      = true
  min_tls_version                 = "TLS1_2"

  # Enable blob encryption
  blob_properties {
    versioning_enabled = true

    delete_retention_policy {
      days = 30
    }

    container_delete_retention_policy {
      days = 30
    }
  }

  tags = merge(var.tags, {
    Name        = "${var.project_name}-frontend"
    Environment = var.environment
  })
}

# CDN profile
resource "azurerm_cdn_profile" "frontend" {
  name                = "${var.project_name}-cdn-profile"
  location            = var.location
  resource_group_name = var.resource_group_name
  sku                 = var.cdn_sku

  tags = merge(var.tags, {
    Name        = "${var.project_name}-cdn-profile"
    Environment = var.environment
  })
}

# CDN endpoint for frontend
resource "azurerm_cdn_endpoint" "frontend" {
  name                = "${var.project_name}-cdn-endpoint"
  profile_name        = azurerm_cdn_profile.frontend.name
  location            = azurerm_cdn_profile.frontend.location
  resource_group_name = var.resource_group_name

  origin_host_header     = azurerm_storage_account.frontend.primary_web_host
  is_compression_enabled = true
  is_http_allowed        = false
  is_https_allowed       = true

  content_types_to_compress = [
    "application/javascript",
    "application/json",
    "application/x-javascript",
    "application/xml",
    "text/css",
    "text/html",
    "text/javascript",
    "text/plain",
  ]

  # Origin for static files (Blob Storage)
  origin {
    name      = "storage-origin"
    host_name = azurerm_storage_account.frontend.primary_web_host
  }

  # Delivery rules for routing
  delivery_rule {
    name  = "api-proxy"
    order = 1

    url_path_condition {
      operator     = "BeginsWith"
      match_values = ["/api/"]
    }

    url_redirect_action {
      redirect_type = "Found"
      protocol      = "Https"
      hostname      = var.api_hostname
      path          = "/"
      query_string  = ""
    }
  }

  # SPA routing - redirect 404s to index.html
  delivery_rule {
    name  = "spa-routing"
    order = 2

    request_uri_condition {
      operator         = "Equal"
      negate_condition = false
      match_values     = ["*.html", "*.css", "*.js", "*.json", "*.png", "*.jpg", "*.svg", "*.ico"]
      transforms       = []
    }

    url_rewrite_action {
      source_pattern          = "/"
      destination             = "/index.html"
      preserve_unmatched_path = false
    }
  }

  # Cache rules for static assets
  global_delivery_rule {
    cache_expiration_action {
      behavior = "Override"
      duration = "1.00:00:00" # 1 day
    }
  }

  tags = merge(var.tags, {
    Name        = "${var.project_name}-cdn-endpoint"
    Environment = var.environment
  })
}

# Custom domain (if provided)
resource "azurerm_cdn_endpoint_custom_domain" "frontend" {
  count = var.custom_domain != "" ? 1 : 0

  name            = replace(var.custom_domain, ".", "-")
  cdn_endpoint_id = azurerm_cdn_endpoint.frontend.id
  host_name       = var.custom_domain

  cdn_managed_https {
    certificate_type = "Dedicated"
    protocol_type    = "ServerNameIndication"
    tls_version      = "TLS12"
  }
}

# Front Door profile (Premium CDN alternative)
resource "azurerm_cdn_frontdoor_profile" "frontend" {
  count = var.use_front_door ? 1 : 0

  name                = "${var.project_name}-frontdoor"
  resource_group_name = var.resource_group_name
  sku_name            = "Premium_AzureFrontDoor"

  tags = merge(var.tags, {
    Name        = "${var.project_name}-frontdoor"
    Environment = var.environment
  })
}

# Front Door endpoint
resource "azurerm_cdn_frontdoor_endpoint" "frontend" {
  count = var.use_front_door ? 1 : 0

  name                     = "${var.project_name}-fd-endpoint"
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.frontend[0].id

  tags = merge(var.tags, {
    Name        = "${var.project_name}-fd-endpoint"
    Environment = var.environment
  })
}

# Front Door origin group for static files
resource "azurerm_cdn_frontdoor_origin_group" "storage" {
  count = var.use_front_door ? 1 : 0

  name                     = "storage-origin-group"
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.frontend[0].id

  load_balancing {
    sample_size                 = 4
    successful_samples_required = 3
  }

  health_probe {
    path                = "/"
    request_type        = "HEAD"
    protocol            = "Https"
    interval_in_seconds = 100
  }
}

# Front Door origin for storage
resource "azurerm_cdn_frontdoor_origin" "storage" {
  count = var.use_front_door ? 1 : 0

  name                          = "storage-origin"
  cdn_frontdoor_origin_group_id = azurerm_cdn_frontdoor_origin_group.storage[0].id
  enabled                       = true

  certificate_name_check_enabled = true
  host_name                      = azurerm_storage_account.frontend.primary_web_host
  http_port                      = 80
  https_port                     = 443
  origin_host_header             = azurerm_storage_account.frontend.primary_web_host
  priority                       = 1
  weight                         = 1000
}

# Front Door route
resource "azurerm_cdn_frontdoor_route" "frontend" {
  count = var.use_front_door ? 1 : 0

  name                          = "frontend-route"
  cdn_frontdoor_endpoint_id     = azurerm_cdn_frontdoor_endpoint.frontend[0].id
  cdn_frontdoor_origin_group_id = azurerm_cdn_frontdoor_origin_group.storage[0].id
  cdn_frontdoor_origin_ids      = [azurerm_cdn_frontdoor_origin.storage[0].id]

  supported_protocols    = ["Http", "Https"]
  patterns_to_match      = ["/*"]
  forwarding_protocol    = "HttpsOnly"
  link_to_default_domain = true
  https_redirect_enabled = true
}

# Monitor for CDN health
resource "azurerm_monitor_metric_alert" "cdn_errors" {
  count               = var.action_group_id != "" ? 1 : 0
  name                = "${var.project_name}-cdn-errors"
  resource_group_name = var.resource_group_name
  scopes              = [azurerm_cdn_endpoint.frontend.id]
  description         = "Alert when CDN error rate is high"
  severity            = 2
  frequency           = "PT5M"
  window_size         = "PT15M"

  criteria {
    metric_namespace = "Microsoft.Cdn/profiles/endpoints"
    metric_name      = "PercentageOf4XX"
    aggregation      = "Average"
    operator         = "GreaterThan"
    threshold        = 5
  }

  action {
    action_group_id = var.action_group_id
  }

  tags = merge(var.tags, {
    Name        = "${var.project_name}-cdn-alert"
    Environment = var.environment
  })
}
