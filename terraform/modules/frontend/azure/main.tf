# Azure Frontend Module - CDN routing to Container Apps
# All traffic (static files + API) is routed to the Container Apps origin.
# The container serves static files directly; the CDN provides edge caching,
# HTTPS termination, custom domains, and SPA routing fallback.

terraform {
  required_version = ">= 1.0"
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
  }
}

# ==============================================
# Classic CDN (when use_front_door = false)
# ==============================================

# CDN profile
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

# CDN endpoint - single origin pointing to Container Apps
resource "azurerm_cdn_endpoint" "frontend" {
  count = var.use_front_door ? 0 : 1

  name                = "${var.project_name}-cdn-endpoint"
  profile_name        = azurerm_cdn_profile.frontend[0].name
  location            = azurerm_cdn_profile.frontend[0].location
  resource_group_name = var.resource_group_name

  origin_host_header     = var.api_hostname
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

  # Single origin - Container Apps serves both static files and API
  origin {
    name      = "container-apps-origin"
    host_name = var.api_hostname
  }

  # HTTPS redirect
  global_delivery_rule {
    url_redirect_action {
      redirect_type = "Moved"
      protocol      = "Https"
    }
  }

  # SPA routing - rewrite non-file paths to /index.html
  delivery_rule {
    name  = "sparouting"
    order = 1

    url_file_extension_condition {
      operator         = "Equal"
      negate_condition = true
      match_values     = ["css", "js", "html", "json", "png", "jpg", "svg", "ico", "woff2", "map"]
    }

    url_path_condition {
      operator         = "BeginsWith"
      negate_condition = true
      match_values     = ["api/"]
    }

    url_rewrite_action {
      source_pattern          = "/"
      destination             = "/index.html"
      preserve_unmatched_path = false
    }
  }

  tags = merge(var.tags, {
    Name        = "${var.project_name}-cdn-endpoint"
    Environment = var.environment
  })
}

# Custom domain (classic CDN, using custom_domain variable)
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

# Monitor for CDN health (classic CDN only)
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

# ==============================================
# Azure Front Door (when use_front_door = true)
# ==============================================

# Front Door profile
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

# Single origin group pointing to Container Apps
resource "azurerm_cdn_frontdoor_origin_group" "container_apps" {
  count = var.use_front_door ? 1 : 0

  name                     = "container-apps-origin-group"
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

# Single origin - Container Apps serves everything
resource "azurerm_cdn_frontdoor_origin" "container_apps" {
  count = var.use_front_door ? 1 : 0

  name                          = "container-apps-origin"
  cdn_frontdoor_origin_group_id = azurerm_cdn_frontdoor_origin_group.container_apps[0].id
  enabled                       = true

  certificate_name_check_enabled = true
  host_name                      = var.api_hostname
  http_port                      = 80
  https_port                     = 443
  origin_host_header             = var.api_hostname
  priority                       = 1
  weight                         = 1000
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

# Rule set: HTTP -> HTTPS 301 redirect
# Azure Front Door's built-in https_redirect_enabled uses 307 (Temporary Redirect).
# This rule set uses 301 (Moved Permanently) which is better for SEO and caching.
resource "azurerm_cdn_frontdoor_rule_set" "https_redirect" {
  count = var.use_front_door ? 1 : 0

  name                     = "httpsredirect"
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.frontend[0].id
}

resource "azurerm_cdn_frontdoor_rule" "https_redirect" {
  count = var.use_front_door ? 1 : 0

  depends_on = [
    azurerm_cdn_frontdoor_origin_group.container_apps,
    azurerm_cdn_frontdoor_origin.container_apps,
  ]

  name                      = "HttpsRedirect"
  cdn_frontdoor_rule_set_id = azurerm_cdn_frontdoor_rule_set.https_redirect[0].id
  order                     = 1
  behavior_on_match         = "Stop"

  conditions {
    request_scheme_condition {
      operator     = "Equal"
      match_values = ["HTTP"]
    }
  }

  actions {
    url_redirect_action {
      redirect_type        = "Moved"
      redirect_protocol    = "Https"
      destination_hostname = ""
    }
  }
}

# Rule set: SPA routing
# Rewrites non-file paths to /index.html so client-side routing works.
# This is kept as a CDN-level fallback even though the container handles SPA
# routing, to ensure correct behavior for edge-cached responses.
resource "azurerm_cdn_frontdoor_rule_set" "spa_routing" {
  count = var.use_front_door ? 1 : 0

  name                     = "sparouting"
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.frontend[0].id
}

resource "azurerm_cdn_frontdoor_rule" "spa_rewrite" {
  count = var.use_front_door ? 1 : 0

  depends_on = [
    azurerm_cdn_frontdoor_origin_group.container_apps,
    azurerm_cdn_frontdoor_origin.container_apps,
  ]

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

    url_path_condition {
      operator         = "BeginsWith"
      negate_condition = true
      match_values     = ["api/"]
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

# Rule set: Security response headers
# Matches the security headers set by CloudFront Function in the AWS module.
resource "azurerm_cdn_frontdoor_rule_set" "security_headers" {
  count = var.use_front_door ? 1 : 0

  name                     = "securityheaders"
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.frontend[0].id
}

resource "azurerm_cdn_frontdoor_rule" "security_headers" {
  count = var.use_front_door ? 1 : 0

  depends_on = [
    azurerm_cdn_frontdoor_origin_group.container_apps,
    azurerm_cdn_frontdoor_origin.container_apps,
  ]

  name                      = "SecurityHeaders"
  cdn_frontdoor_rule_set_id = azurerm_cdn_frontdoor_rule_set.security_headers[0].id
  order                     = 1
  behavior_on_match         = "Continue"

  actions {
    response_header_action {
      header_action = "Overwrite"
      header_name   = "Strict-Transport-Security"
      value         = "max-age=31536000; includeSubDomains; preload"
    }

    response_header_action {
      header_action = "Overwrite"
      header_name   = "X-Content-Type-Options"
      value         = "nosniff"
    }

    response_header_action {
      header_action = "Overwrite"
      header_name   = "X-Frame-Options"
      value         = "DENY"
    }

    response_header_action {
      header_action = "Overwrite"
      header_name   = "X-XSS-Protection"
      value         = "1; mode=block"
    }

    response_header_action {
      header_action = "Overwrite"
      header_name   = "Referrer-Policy"
      value         = "strict-origin-when-cross-origin"
    }
  }
}

# Single route - all traffic goes to Container Apps origin
resource "azurerm_cdn_frontdoor_route" "frontend" {
  count = var.use_front_door ? 1 : 0

  name                          = "frontend-route"
  cdn_frontdoor_endpoint_id     = azurerm_cdn_frontdoor_endpoint.frontend[0].id
  cdn_frontdoor_origin_group_id = azurerm_cdn_frontdoor_origin_group.container_apps[0].id
  cdn_frontdoor_origin_ids      = [azurerm_cdn_frontdoor_origin.container_apps[0].id]

  cdn_frontdoor_custom_domain_ids = length(var.domain_names) > 0 ? (
    [azurerm_cdn_frontdoor_custom_domain.frontend[0].id]
  ) : []

  cdn_frontdoor_rule_set_ids = [
    azurerm_cdn_frontdoor_rule_set.https_redirect[0].id,
    azurerm_cdn_frontdoor_rule_set.spa_routing[0].id,
    azurerm_cdn_frontdoor_rule_set.security_headers[0].id,
  ]

  supported_protocols    = ["Http", "Https"]
  patterns_to_match      = ["/*"]
  forwarding_protocol    = "HttpsOnly"
  link_to_default_domain = true
  https_redirect_enabled = false
}

# Front Door custom domain association
resource "azurerm_cdn_frontdoor_custom_domain_association" "frontend" {
  count = var.use_front_door && length(var.domain_names) > 0 ? 1 : 0

  cdn_frontdoor_custom_domain_id = azurerm_cdn_frontdoor_custom_domain.frontend[0].id
  cdn_frontdoor_route_ids = [
    azurerm_cdn_frontdoor_route.frontend[0].id,
  ]
}
