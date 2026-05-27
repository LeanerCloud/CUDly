output "role_definition_resource_id" {
  description = "Full ARM resource ID of the custom role definition. Use as role_definition_id in azurerm_role_assignment blocks."
  value       = azurerm_role_definition.cudly_reservation_purchaser.role_definition_resource_id
}
