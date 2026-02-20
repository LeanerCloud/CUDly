# Terraform Backend Configuration for GCP
# Use -backend-config flag to specify environment:
#   terraform init -backend-config=backends/dev.tfbackend

terraform {
  backend "gcs" {
    # Configuration provided via -backend-config flag
  }
}
