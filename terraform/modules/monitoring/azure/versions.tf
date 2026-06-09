terraform {
  required_version = ">= 1.6.0"

  required_providers {
    azurerm = {
      # Pinned to ~> 4.0 (not the codebase's ~> 3.0 azurerm convention)
      # to match this module's previously-committed lock file (4.61.0).
      # The Azure environment doesn't currently instantiate this module
      # so the version split doesn't conflict with any running plan.
      source  = "hashicorp/azurerm"
      version = "~> 4.0"
    }
  }
}
