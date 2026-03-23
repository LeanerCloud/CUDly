# ==============================================
# Resource Group
# ==============================================

output "resource_group_name" {
  description = "Resource group name"
  value       = azurerm_resource_group.main.name
}

output "resource_group_id" {
  description = "Resource group ID"
  value       = azurerm_resource_group.main.id
}

# ==============================================
# Networking
# ==============================================

output "vnet_id" {
  description = "Virtual Network ID"
  value       = module.networking.vnet_id
}

output "vnet_name" {
  description = "Virtual Network name"
  value       = module.networking.vnet_name
}

output "container_apps_subnet_id" {
  description = "Container Apps subnet ID"
  value       = module.networking.container_apps_subnet_id
}

output "database_subnet_id" {
  description = "Database subnet ID"
  value       = module.networking.database_subnet_id
}

output "log_analytics_workspace_id" {
  description = "Log Analytics workspace ID"
  value       = module.networking.log_analytics_workspace_id
}

# ==============================================
# Key Vault (Secrets)
# ==============================================

output "key_vault_id" {
  description = "Key Vault ID"
  value       = module.secrets.key_vault_id
}

output "key_vault_name" {
  description = "Key Vault name"
  value       = module.secrets.key_vault_name
}

output "key_vault_uri" {
  description = "Key Vault URI"
  value       = module.secrets.key_vault_uri
}

output "all_secret_names" {
  description = "List of all secrets in Key Vault"
  value       = module.secrets.all_secret_names
}

# ==============================================
# Database
# ==============================================

output "database_server_id" {
  description = "PostgreSQL server ID"
  value       = module.database.server_id
}

output "database_server_name" {
  description = "PostgreSQL server name"
  value       = module.database.server_name
}

output "database_server_fqdn" {
  description = "PostgreSQL server FQDN"
  value       = module.database.server_fqdn
}

output "database_name" {
  description = "Database name"
  value       = module.database.database_name
}

output "database_connection_string" {
  description = "Database connection string (without password)"
  value       = "postgresql://${module.database.administrator_login}@${module.database.server_fqdn}:5432/${module.database.database_name}?sslmode=require"
  sensitive   = true
}

# ==============================================
# Compute Platform
# ==============================================

output "compute_platform" {
  description = "Selected compute platform"
  value       = var.compute_platform
}

# Container Apps Outputs (when using container-apps platform)
output "container_app_id" {
  description = "Container App ID"
  value       = var.compute_platform == "container-apps" ? module.compute_container_apps[0].container_app_id : null
}

output "container_app_name" {
  description = "Container App name"
  value       = var.compute_platform == "container-apps" ? module.compute_container_apps[0].container_app_name : null
}

output "container_app_url" {
  description = "Container App URL"
  value       = var.compute_platform == "container-apps" ? module.compute_container_apps[0].container_app_url : null
}

output "container_app_fqdn" {
  description = "Container App FQDN"
  value       = var.compute_platform == "container-apps" ? module.compute_container_apps[0].container_app_fqdn : null
}

# AKS Outputs (when using aks platform)
output "aks_cluster_id" {
  description = "AKS cluster ID"
  value       = var.compute_platform == "aks" ? module.compute_aks[0].cluster_id : null
}

output "aks_cluster_name" {
  description = "AKS cluster name"
  value       = var.compute_platform == "aks" ? module.compute_aks[0].cluster_name : null
}

output "aks_api_url" {
  description = "AKS API URL (Load Balancer IP)"
  value       = var.compute_platform == "aks" ? module.compute_aks[0].api_url : null
}

output "aks_load_balancer_ip" {
  description = "AKS Load Balancer IP"
  value       = var.compute_platform == "aks" ? module.compute_aks[0].load_balancer_ip : null
}

# Frontend URL (CDN or compute default endpoint)
output "frontend_url" {
  description = "Frontend URL (CDN, custom domain, or compute default endpoint)"
  value = (
    var.enable_cdn ? module.frontend[0].frontend_url :
    length(var.frontend_domain_names) > 0 ? "https://${var.frontend_domain_names[0]}" :
    var.compute_platform == "container-apps" ? module.compute_container_apps[0].container_app_url :
    module.compute_aks[0].api_url
  )
}

# Unified Outputs (work for both platforms)
output "api_endpoint" {
  description = "API endpoint URL (works for both platforms)"
  value       = var.compute_platform == "container-apps" ? module.compute_container_apps[0].container_app_url : module.compute_aks[0].api_url
}

output "managed_identity_id" {
  description = "Managed identity ID"
  value       = var.compute_platform == "container-apps" ? module.compute_container_apps[0].managed_identity_id : null
}

output "managed_identity_principal_id" {
  description = "Managed identity principal ID"
  value       = var.compute_platform == "container-apps" ? module.compute_container_apps[0].managed_identity_principal_id : null
}

output "managed_identity_client_id" {
  description = "Managed identity client ID"
  value       = var.compute_platform == "container-apps" ? module.compute_container_apps[0].managed_identity_client_id : null
}

# ==============================================
# Deployment Summary
# ==============================================

output "deployment_summary" {
  description = "Deployment summary"
  value = {
    environment             = var.environment
    location                = var.location
    resource_group          = azurerm_resource_group.main.name
    compute_platform        = var.compute_platform
    application_url         = var.compute_platform == "container-apps" ? module.compute_container_apps[0].container_app_url : module.compute_aks[0].api_url
    database_fqdn           = module.database.server_fqdn
    key_vault_name          = module.secrets.key_vault_name
    log_analytics_workspace = module.networking.log_analytics_workspace_id
  }
}

# ==============================================
# Quick Start Commands
# ==============================================

output "quick_start_commands" {
  description = "Quick start commands for post-deployment"
  sensitive   = true
  value       = <<-EOT
    ================================================================================
    CUDly Azure Deployment - ${upper(var.environment)} Environment
    Compute Platform: ${upper(var.compute_platform)}
    ================================================================================

    Application URL:
      ${var.compute_platform == "container-apps" ? module.compute_container_apps[0].container_app_url : module.compute_aks[0].api_url}

    Health Check:
      curl ${var.compute_platform == "container-apps" ? module.compute_container_apps[0].container_app_url : module.compute_aks[0].api_url}/health

    Database Connection (Azure CLI):
      az postgres flexible-server connect \
        --name ${module.database.server_name} \
        --admin-user ${module.database.administrator_login} \
        --database-name ${module.database.database_name}

    View Application Logs:
      ${var.compute_platform == "container-apps" ? "az containerapp logs show \\\n        --name ${module.compute_container_apps[0].container_app_name} \\\n        --resource-group ${azurerm_resource_group.main.name} \\\n        --follow" : "az aks get-credentials --name ${module.compute_aks[0].cluster_name} --resource-group ${azurerm_resource_group.main.name}\n      kubectl logs -f deployment/${var.project_name}-api -n ${var.environment}"}

    View Key Vault Secrets:
      az keyvault secret list \
        --vault-name ${module.secrets.key_vault_name} \
        --output table

    Get Database Password:
      az keyvault secret show \
        --vault-name ${module.secrets.key_vault_name} \
        --name db-password \
        --query value -o tsv

    Update Application:
      ${var.compute_platform == "container-apps" ? "az containerapp update \\\n        --name ${module.compute_container_apps[0].container_app_name} \\\n        --resource-group ${azurerm_resource_group.main.name} \\\n        --image YOUR_NEW_IMAGE_URI" : "kubectl set image deployment/${var.project_name}-api api=YOUR_NEW_IMAGE_URI -n ${var.environment}"}

    Scale Application:
      ${var.compute_platform == "container-apps" ? "az containerapp update \\\n        --name ${module.compute_container_apps[0].container_app_name} \\\n        --resource-group ${azurerm_resource_group.main.name} \\\n        --min-replicas 2 \\\n        --max-replicas 20" : "kubectl scale deployment/${var.project_name}-api --replicas=3 -n ${var.environment}"}

    View Application Metrics:
      ${var.compute_platform == "container-apps" ? "az monitor metrics list \\\n        --resource ${module.compute_container_apps[0].container_app_id} \\\n        --metric Requests \\\n        --aggregation Total" : "kubectl top pods -n ${var.environment}"}

    View Database Metrics:
      az monitor metrics list \
        --resource ${module.database.server_id} \
        --metric cpu_percent \
        --aggregation Average

    Run Database Migrations Manually:
      ${var.compute_platform == "container-apps" ? "az containerapp exec \\\n        --name ${module.compute_container_apps[0].container_app_name} \\\n        --resource-group ${azurerm_resource_group.main.name} \\\n        --command \"/app/cudly migrate up\"" : "kubectl exec -it deployment/${var.project_name}-api -n ${var.environment} -- /app/cudly migrate up"}

    View Log Analytics Logs (requires workspace access):
      az monitor log-analytics query \
        --workspace ${module.networking.log_analytics_workspace_id} \
        --analytics-query "ContainerAppConsoleLogs_CL | where TimeGenerated > ago(1h) | order by TimeGenerated desc"

    ================================================================================
    Cost Optimization Tips:
    ================================================================================

    1. Scale to zero when not in use:
       - Set min_replicas = 0 for non-production environments
       - Container Apps will scale down to 0 during idle periods

    2. Use Burstable database tier for dev/test:
       - B_Standard_B1ms: ~$12/month (1 vCore, 2 GB RAM)
       - Scales up automatically when needed

    3. Disable geo-redundant backups for dev:
       - Saves ~50% on backup storage costs

    4. Use short log retention for dev:
       - 30 days instead of 90 days saves on Log Analytics costs

    5. Disable high availability for dev:
       - HA mode = "Disabled" saves ~100% on standby instance costs

    Current Configuration:
      - Database SKU: ${var.database_sku_name}
      - Container CPU: ${var.container_cpu} cores
      - Container Memory: ${var.container_memory}
      - Min Replicas: ${var.min_replicas}
      - Max Replicas: ${var.max_replicas}
      - HA Mode: ${var.database_high_availability_mode}
      - Geo-Redundant Backup: ${var.database_geo_redundant_backup}

    ================================================================================
  EOT
}
