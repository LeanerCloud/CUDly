# Frontend Build and Deployment Resources for Azure
# This file handles building the frontend and uploading to Azure Blob Storage

# Build frontend with npm
resource "terraform_data" "frontend_build" {
  count = var.enable_frontend_build ? 1 : 0

  triggers_replace = {
    # Rebuild when package.json or source files change
    package_json = fileexists("${path.root}/${var.frontend_path}/package.json") ? filemd5("${path.root}/${var.frontend_path}/package.json") : "none"
    src_hash     = fileexists("${path.root}/${var.frontend_path}/src") ? sha256(join("", [for f in fileset("${path.root}/${var.frontend_path}/src", "**") : filesha256("${path.root}/${var.frontend_path}/src/${f}")])) : "none"
  }

  provisioner "local-exec" {
    working_dir = "${path.root}/${var.frontend_path}"
    command     = <<-EOT
      echo "Building frontend..."
      npm install --production
      npm run build
      echo "✅ Frontend build complete"
    EOT
  }
}

# Upload frontend files to Azure Blob Storage
resource "terraform_data" "frontend_upload" {
  count = var.enable_frontend_build ? 1 : 0

  triggers_replace = {
    # Re-upload when build changes
    build_hash = terraform_data.frontend_build[0].id
    files_hash = fileexists("${path.root}/${var.frontend_path}/dist") ? sha256(join("", [for f in fileset("${path.root}/${var.frontend_path}/dist", "**") : filemd5("${path.root}/${var.frontend_path}/dist/${f}")])) : "none"
  }

  provisioner "local-exec" {
    command = <<-EOT
      echo "Uploading frontend to Azure Blob Storage..."
      az storage blob upload-batch \
        --account-name ${var.storage_account_name} \
        --destination '$web' \
        --source ${path.root}/${var.frontend_path}/dist \
        --overwrite \
        --content-cache-control "public, max-age=31536000, immutable" \
        --pattern "*.js" \
        --pattern "*.css" \
        --pattern "*.png" \
        --pattern "*.jpg" \
        --pattern "*.svg" \
        --pattern "*.woff*" \
        --pattern "*.ttf"

      # Upload HTML files with no-cache
      az storage blob upload-batch \
        --account-name ${var.storage_account_name} \
        --destination '$web' \
        --source ${path.root}/${var.frontend_path}/dist \
        --overwrite \
        --content-cache-control "no-cache, no-store, must-revalidate" \
        --pattern "*.html"

      echo "✅ Frontend uploaded to Azure Blob Storage"
    EOT
  }

  depends_on = [terraform_data.frontend_build[0]]
}

# Purge Azure CDN cache after deployment
resource "terraform_data" "cdn_purge" {
  count = var.enable_frontend_build ? 1 : 0

  triggers_replace = {
    # Purge when files change
    upload_hash = terraform_data.frontend_upload[0].id
  }

  provisioner "local-exec" {
    command = <<-EOT
      echo "Purging Azure CDN cache..."
      az cdn endpoint purge \
        --resource-group ${var.resource_group_name} \
        --profile-name ${var.project_name}-cdn-profile \
        --name ${var.project_name}-cdn-endpoint \
        --content-paths "/*"
      echo "✅ Azure CDN cache purged"
    EOT
  }

  depends_on = [terraform_data.frontend_upload[0]]
}
