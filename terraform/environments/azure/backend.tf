# Terraform Backend Configuration for Azure
# Use -backend-config flag to specify environment:
#   terraform init -backend-config=backends/dev.tfbackend

terraform {
  backend "azurerm" {
    # Configuration provided via -backend-config flag
  }
}
