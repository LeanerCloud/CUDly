# Terraform Backend Configuration
# Use -backend-config flag to specify environment:
#   terraform init -backend-config=backends/dev.tfbackend
#   terraform init -backend-config=backends/staging.tfbackend
#   terraform init -backend-config=backends/prod.tfbackend
#   terraform init -backend-config=backends/fargate-dev.tfbackend

terraform {
  backend "s3" {
    # Configuration provided via -backend-config flag
    # See backends/*.tfbackend for environment-specific values
  }
}
