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

# CDN profile (classic - only when not using Front Door)
resource "azurerm_cdn_profile" "frontend" {
  count = var.use_front_door ? 0 : 1

  name                = "${var.project_name}-cdn-profile"
  location            = var.location
  resource_group_name = var.resource_group_name
  sku                 = var.cdn_sku

  tags = merge(var.tags, {
    Name        = "${var.project_name}-cdn-profile"
    Environment = var.environment
  })
}

# CDN endpoint for frontend (only when not using Front Door)
resource "azurerm_cdn_endpoint" "frontend" {
  count = var.use_front_door ? 0 : 1

  name                = "${var.project_name}-cdn-endpoint"
  profile_name        = azurerm_cdn_profile.frontend[0].name
  location            = azurerm_cdn_profile.frontend[0].location
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
    name  = "apiproxy"
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
    name  = "sparouting"
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

# Custom domain (if provided, only with classic CDN)
resource "azurerm_cdn_endpoint_custom_domain" "frontend" {
  count = !var.use_front_door && var.custom_domain != "" ? 1 : 0

  name            = replace(var.custom_domain, ".", "-")
  cdn_endpoint_id = azurerm_cdn_endpoint.frontend[0].id
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
  sku_name            = "Standard_AzureFrontDoor"

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

# Front Door origin group for API (Container App)
resource "azurerm_cdn_frontdoor_origin_group" "api" {
  count = var.use_front_door ? 1 : 0

  name                     = "api-origin-group"
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.frontend[0].id

  load_balancing {
    sample_size                 = 4
    successful_samples_required = 3
  }

  health_probe {
    path                = "/health"
    request_type        = "GET"
    protocol            = "Https"
    interval_in_seconds = 100
  }
}

# Front Door origin for API (Container App)
resource "azurerm_cdn_frontdoor_origin" "api" {
  count = var.use_front_door ? 1 : 0

  name                          = "api-origin"
  cdn_frontdoor_origin_group_id = azurerm_cdn_frontdoor_origin_group.api[0].id
  enabled                       = true

  certificate_name_check_enabled = true
  host_name                      = var.api_hostname
  http_port                      = 80
  https_port                     = 443
  origin_host_header             = var.api_hostname
  priority                       = 1
  weight                         = 1000
}

# Front Door route for API requests (/api/*)
resource "azurerm_cdn_frontdoor_route" "api" {
  count = var.use_front_door ? 1 : 0

  name                          = "api-route"
  cdn_frontdoor_endpoint_id     = azurerm_cdn_frontdoor_endpoint.frontend[0].id
  cdn_frontdoor_origin_group_id = azurerm_cdn_frontdoor_origin_group.api[0].id
  cdn_frontdoor_origin_ids      = [azurerm_cdn_frontdoor_origin.api[0].id]

  cdn_frontdoor_custom_domain_ids = length(var.domain_names) > 0 ? (
    [azurerm_cdn_frontdoor_custom_domain.frontend[0].id]
  ) : []

  supported_protocols    = ["Http", "Https"]
  patterns_to_match      = ["/api/*"]
  forwarding_protocol    = "HttpsOnly"
  link_to_default_domain = true
  https_redirect_enabled = true
}

# Front Door custom domain (when using Front Door with custom domains)
resource "azurerm_cdn_frontdoor_custom_domain" "frontend" {
  count = var.use_front_door && length(var.domain_names) > 0 ? 1 : 0

  name                     = "${var.project_name}-custom-domain"
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.frontend[0].id
  host_name                = var.domain_names[0]

  tls {
    certificate_type    = "ManagedCertificate"
    minimum_tls_version = "TLS12"
  }
}

# Front Door rule set for SPA routing
# Azure Blob Storage static website returns 404 status for unknown paths even with
# error_404_document set. This rule rewrites non-file paths to /index.html before
# the request reaches the origin, ensuring SPA client-side routing works correctly.
resource "azurerm_cdn_frontdoor_rule_set" "spa_routing" {
  count = var.use_front_door ? 1 : 0

  name                     = "sparouting"
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.frontend[0].id
}

resource "azurerm_cdn_frontdoor_rule" "spa_rewrite" {
  count = var.use_front_door ? 1 : 0

  depends_on = [azurerm_cdn_frontdoor_origin_group.storage, azurerm_cdn_frontdoor_origin.storage]

  name                      = "SPARewrite"
  cdn_frontdoor_rule_set_id = azurerm_cdn_frontdoor_rule_set.spa_routing[0].id
  order                     = 1
  behavior_on_match         = "Continue"

  conditions {
    url_file_extension_condition {
      operator         = "Equal"
      negate_condition = true
      match_values     = ["css", "js", "html", "json", "png", "jpg", "svg", "ico", "woff2", "map"]
      transforms       = ["Lowercase"]
    }
  }

  actions {
    url_rewrite_action {
      source_pattern          = "/"
      destination             = "/index.html"
      preserve_unmatched_path = false
    }
  }
}

# Front Door route
resource "azurerm_cdn_frontdoor_route" "frontend" {
  count = var.use_front_door ? 1 : 0

  name                          = "frontend-route"
  cdn_frontdoor_endpoint_id     = azurerm_cdn_frontdoor_endpoint.frontend[0].id
  cdn_frontdoor_origin_group_id = azurerm_cdn_frontdoor_origin_group.storage[0].id
  cdn_frontdoor_origin_ids      = [azurerm_cdn_frontdoor_origin.storage[0].id]

  cdn_frontdoor_custom_domain_ids = length(var.domain_names) > 0 ? (
    [azurerm_cdn_frontdoor_custom_domain.frontend[0].id]
  ) : []

  cdn_frontdoor_rule_set_ids = [azurerm_cdn_frontdoor_rule_set.spa_routing[0].id]

  supported_protocols    = ["Http", "Https"]
  patterns_to_match      = ["/*"]
  forwarding_protocol    = "HttpsOnly"
  link_to_default_domain = true
  https_redirect_enabled = true
}

# Front Door custom domain association
resource "azurerm_cdn_frontdoor_custom_domain_association" "frontend" {
  count = var.use_front_door && length(var.domain_names) > 0 ? 1 : 0

  cdn_frontdoor_custom_domain_id = azurerm_cdn_frontdoor_custom_domain.frontend[0].id
  cdn_frontdoor_route_ids = [
    azurerm_cdn_frontdoor_route.frontend[0].id,
    azurerm_cdn_frontdoor_route.api[0].id,
  ]
}

# Monitor for CDN health (only with classic CDN)
resource "azurerm_monitor_metric_alert" "cdn_errors" {
  count               = !var.use_front_door && var.action_group_id != "" ? 1 : 0
  name                = "${var.project_name}-cdn-errors"
  resource_group_name = var.resource_group_name
  scopes              = [azurerm_cdn_endpoint.frontend[0].id]
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
