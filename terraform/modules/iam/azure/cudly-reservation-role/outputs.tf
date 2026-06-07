output "role_definition_resource_id" {
  description = "Full ARM resource ID of the custom role definition. Use as role_definition_id in azurerm_role_assignment blocks."
  value       = azurerm_role_definition.cudly_reservation_purchaser.role_definition_resource_id
}

output "role_definition_name" {
  description = "Display name of the custom role definition. Runtime modules that look the role up via data.azurerm_role_definition must match this exact name."
  value       = local.role_definition_name
}
