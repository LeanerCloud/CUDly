# Host-side custom reservation-purchaser role definition (BOOTSTRAP).
#
# Creating an azurerm_role_definition requires
# Microsoft.Authorization/roleDefinitions/write, a bootstrap-class permission
# the runtime CI deploy service principal intentionally does NOT have (it only
# has roleDefinitions/read; see locals_data.tf). This module is applied once by
# a privileged human with role-admin authority, so it is the correct home for
# the role *definition*.
#
# The runtime container-apps module
# (terraform/modules/compute/azure/container-apps) no longer creates this role.
# It looks it up via data.azurerm_role_definition and creates only the
# *assignment* (which the deploy SP can do via roleAssignments/write). If this
# bootstrap has not been (re-)applied, that runtime data lookup fails loudly,
# signalling the operator to re-run this module.
#
# name_suffix matches the runtime side (the subscription ID) so both reference
# the identical role display name.
module "cudly_reservation_role" {
  source      = "../../../modules/iam/azure/cudly-reservation-role"
  scope       = "/subscriptions/${var.subscription_id}"
  name_suffix = var.subscription_id
}
