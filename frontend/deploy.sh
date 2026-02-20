#!/bin/bash
# Frontend Deployment Script for CUDly
# Supports AWS (S3+CloudFront), Azure (Blob+CDN), and GCP (Cloud Storage+CDN)

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Print colored message
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Usage function
usage() {
    cat <<EOF
Usage: $0 [OPTIONS]

Deploy CUDly frontend to cloud storage and CDN.

OPTIONS:
    -p, --provider PROVIDER    Cloud provider (aws|azure|gcp) [required]
    -e, --environment ENV      Environment (dev|staging|prod) [default: dev]
    -b, --bucket BUCKET        Bucket/storage account name [required]
    -d, --distribution ID      Distribution/CDN ID [required for cache invalidation]
    -r, --region REGION        AWS region or Azure location [default: us-east-1]
    -h, --help                 Show this help message

EXAMPLES:
    # AWS deployment
    $0 -p aws -e prod -b cudly-frontend-prod -d E1234567890ABC

    # Azure deployment
    $0 -p azure -e prod -b cudlyfrontendprod -d cudly-cdn-endpoint

    # GCP deployment
    $0 -p gcp -e prod -b cudly-frontend-prod -d cudly-url-map

EOF
    exit 1
}

# Parse arguments
PROVIDER=""
ENVIRONMENT="dev"
BUCKET=""
DISTRIBUTION=""
REGION="us-east-1"

while [[ $# -gt 0 ]]; do
    case $1 in
        -p|--provider)
            PROVIDER="$2"
            shift 2
            ;;
        -e|--environment)
            ENVIRONMENT="$2"
            shift 2
            ;;
        -b|--bucket)
            BUCKET="$2"
            shift 2
            ;;
        -d|--distribution)
            DISTRIBUTION="$2"
            shift 2
            ;;
        -r|--region)
            REGION="$2"
            shift 2
            ;;
        -h|--help)
            usage
            ;;
        *)
            log_error "Unknown option: $1"
            usage
            ;;
    esac
done

# Validate required arguments
if [[ -z "$PROVIDER" ]]; then
    log_error "Provider is required"
    usage
fi

if [[ -z "$BUCKET" ]]; then
    log_error "Bucket/storage account name is required"
    usage
fi

# Validate provider
if [[ ! "$PROVIDER" =~ ^(aws|azure|gcp)$ ]]; then
    log_error "Provider must be one of: aws, azure, gcp"
    exit 1
fi

# Build frontend
log_info "Building frontend for $ENVIRONMENT environment..."
npm run build

if [[ ! -d "dist" ]]; then
    log_error "Build failed: dist/ directory not found"
    exit 1
fi

log_info "Build completed successfully"

# Deploy based on provider
case $PROVIDER in
    aws)
        log_info "Deploying to AWS S3: $BUCKET"

        # Check AWS CLI
        if ! command -v aws &> /dev/null; then
            log_error "AWS CLI not found. Please install it first."
            exit 1
        fi

        # Upload all files except index.html with long cache
        log_info "Uploading static assets..."
        aws s3 sync dist/ "s3://${BUCKET}/" \
            --region "$REGION" \
            --delete \
            --cache-control "public,max-age=31536000,immutable" \
            --exclude "index.html" \
            --exclude "*.map"

        # Upload index.html with short cache
        log_info "Uploading index.html..."
        aws s3 cp dist/index.html "s3://${BUCKET}/index.html" \
            --region "$REGION" \
            --cache-control "public,max-age=300" \
            --content-type "text/html"

        log_info "Upload completed"

        # Invalidate CloudFront cache if distribution provided
        if [[ -n "$DISTRIBUTION" ]]; then
            log_info "Creating CloudFront invalidation..."
            INVALIDATION_ID=$(aws cloudfront create-invalidation \
                --distribution-id "$DISTRIBUTION" \
                --paths "/*" \
                --query 'Invalidation.Id' \
                --output text)

            log_info "Invalidation created: $INVALIDATION_ID"
            log_info "Cache invalidation may take 5-10 minutes"
        else
            log_warn "No distribution ID provided, skipping cache invalidation"
        fi
        ;;

    azure)
        log_info "Deploying to Azure Blob Storage: $BUCKET"

        # Check Azure CLI
        if ! command -v az &> /dev/null; then
            log_error "Azure CLI not found. Please install it first."
            exit 1
        fi

        # Upload files to $web container
        log_info "Uploading files..."
        az storage blob upload-batch \
            --account-name "$BUCKET" \
            --destination '$web' \
            --source dist/ \
            --overwrite \
            --content-cache-control "public, max-age=31536000, immutable" \
            --pattern "*" \
            --exclude-pattern "index.html"

        # Upload index.html separately
        log_info "Uploading index.html..."
        az storage blob upload \
            --account-name "$BUCKET" \
            --container-name '$web' \
            --name index.html \
            --file dist/index.html \
            --overwrite \
            --content-cache-control "public, max-age=300" \
            --content-type "text/html"

        log_info "Upload completed"

        # Purge CDN cache if endpoint provided
        if [[ -n "$DISTRIBUTION" ]]; then
            log_info "Purging CDN cache..."

            # Extract resource group and profile from tags or use defaults
            RESOURCE_GROUP="${RESOURCE_GROUP:-cudly-${ENVIRONMENT}-rg}"
            CDN_PROFILE="${CDN_PROFILE:-cudly-cdn-profile}"

            az cdn endpoint purge \
                --resource-group "$RESOURCE_GROUP" \
                --profile-name "$CDN_PROFILE" \
                --name "$DISTRIBUTION" \
                --content-paths "/*"

            log_info "CDN cache purged"
        else
            log_warn "No CDN endpoint provided, skipping cache purge"
        fi
        ;;

    gcp)
        log_info "Deploying to Google Cloud Storage: $BUCKET"

        # Check gcloud CLI
        if ! command -v gsutil &> /dev/null; then
            log_error "gsutil not found. Please install Google Cloud SDK first."
            exit 1
        fi

        # Upload files
        log_info "Uploading files..."
        gsutil -m rsync -r -d \
            -x ".*\.map$" \
            dist/ "gs://${BUCKET}/"

        # Set cache metadata for static assets
        log_info "Setting cache metadata..."
        gsutil -m setmeta \
            -h "Cache-Control:public, max-age=31536000, immutable" \
            "gs://${BUCKET}/js/**" 2>/dev/null || true

        gsutil -m setmeta \
            -h "Cache-Control:public, max-age=31536000, immutable" \
            "gs://${BUCKET}/css/**" 2>/dev/null || true

        # Set short cache for index.html
        gsutil setmeta \
            -h "Cache-Control:public, max-age=300" \
            "gs://${BUCKET}/index.html"

        log_info "Upload completed"

        # Invalidate CDN cache if URL map provided
        if [[ -n "$DISTRIBUTION" ]]; then
            log_info "Invalidating Cloud CDN cache..."
            gcloud compute url-maps invalidate-cdn-cache "$DISTRIBUTION" \
                --path "/*" \
                --async

            log_info "Cache invalidation initiated"
        else
            log_warn "No URL map provided, skipping cache invalidation"
        fi
        ;;
esac

log_info "✅ Deployment completed successfully!"
log_info ""
log_info "Summary:"
log_info "  Provider:     $PROVIDER"
log_info "  Environment:  $ENVIRONMENT"
log_info "  Bucket:       $BUCKET"
if [[ -n "$DISTRIBUTION" ]]; then
    log_info "  CDN ID:       $DISTRIBUTION"
fi

# Show next steps
log_info ""
log_info "Next steps:"
log_info "  1. Wait for CDN cache invalidation to complete (5-10 minutes)"
log_info "  2. Test the frontend at your CDN URL"
log_info "  3. Check browser console for any errors"
log_info "  4. Verify API calls are working correctly"
