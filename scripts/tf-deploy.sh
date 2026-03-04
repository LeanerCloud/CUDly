#!/bin/bash
# Terraform Deployment Helper Script
# Usage: ./scripts/tf-deploy.sh <provider> <profile> [action]
# Example: ./scripts/tf-deploy.sh aws dev
# Example: ./scripts/tf-deploy.sh aws prod plan

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Functions
log_info() {
    echo -e "${BLUE}ℹ️  $1${NC}"
}

log_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

log_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

log_error() {
    echo -e "${RED}❌ $1${NC}"
}

# Parse arguments
PROVIDER=$1
PROFILE=$2
ACTION=${3:-apply}

if [ -z "$PROVIDER" ] || [ -z "$PROFILE" ]; then
    log_error "Usage: $0 <provider> <profile> [action]"
    echo ""
    echo "Examples:"
    echo "  $0 aws dev           # Deploy to AWS dev"
    echo "  $0 aws prod plan     # Plan AWS prod deployment"
    echo "  $0 azure dev         # Deploy to Azure dev"
    echo "  $0 gcp dev           # Deploy to GCP dev"
    echo ""
    echo "Available providers: aws, azure, gcp"
    echo "Available actions: plan, apply, destroy, show, refresh"
    exit 1
fi

# Paths
PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PROFILE_FILE="${PROJECT_ROOT}/terraform/profiles/${PROVIDER}/${PROFILE}.tfvars"
ENV_DIR="${PROJECT_ROOT}/terraform/environments/${PROVIDER}/${PROFILE}"

# Validate profile exists
if [ ! -f "$PROFILE_FILE" ]; then
    log_error "Profile not found: $PROFILE_FILE"
    echo ""
    echo "Available ${PROVIDER} profiles:"
    ls -1 "${PROJECT_ROOT}/terraform/profiles/${PROVIDER}/"*.tfvars 2>/dev/null | xargs -n1 basename | sed 's/.tfvars$//' || echo "  (none)"
    echo ""
    echo "Create a new profile:"
    echo "  cp terraform/profiles/${PROVIDER}/dev.tfvars terraform/profiles/${PROVIDER}/${PROFILE}.tfvars"
    exit 1
fi

# Validate environment directory exists
if [ ! -d "$ENV_DIR" ]; then
    log_warning "Environment directory not found: $ENV_DIR"
    log_info "Creating environment directory..."
    mkdir -p "$ENV_DIR"

    # Create symlink to provider main.tf
    ln -sf "../../${PROVIDER}/main.tf" "${ENV_DIR}/main.tf"

    log_success "Environment directory created"
fi

# Display configuration
echo ""
log_info "Terraform Deployment"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Provider:     ${PROVIDER}"
echo "Profile:      ${PROFILE}"
echo "Action:       ${ACTION}"
echo "Profile File: ${PROFILE_FILE}"
echo "Working Dir:  ${ENV_DIR}"
if [ -n "${AWS_PROFILE:-}" ]; then
    echo "AWS Profile:  ${AWS_PROFILE}"
elif [ -n "${TF_VAR_aws_profile:-}" ]; then
    echo "AWS Profile:  ${TF_VAR_aws_profile}"
else
    log_warning "AWS_PROFILE not set. Set it with: export AWS_PROFILE=your-profile"
fi
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

# Change to environment directory
cd "$ENV_DIR"

# Initialize Terraform if needed
if [ ! -d ".terraform" ]; then
    log_info "Initializing Terraform..."
    terraform init
    log_success "Terraform initialized"
    echo ""
fi

# Execute Terraform command
log_info "Running terraform ${ACTION}..."
echo ""

case $ACTION in
    plan)
        terraform plan -var-file="$PROFILE_FILE"
        ;;
    apply)
        terraform apply -var-file="$PROFILE_FILE"
        ;;
    destroy)
        log_warning "This will destroy all resources!"
        read -p "Are you sure? (type 'yes' to confirm): " confirm
        if [ "$confirm" = "yes" ]; then
            terraform destroy -var-file="$PROFILE_FILE"
        else
            log_info "Destroy cancelled"
            exit 0
        fi
        ;;
    show)
        terraform show
        ;;
    refresh)
        terraform refresh -var-file="$PROFILE_FILE"
        ;;
    output)
        terraform output
        ;;
    *)
        terraform $ACTION -var-file="$PROFILE_FILE"
        ;;
esac

# Show outputs after successful apply
if [ $? -eq 0 ] && [ "$ACTION" = "apply" ]; then
    echo ""
    log_success "Deployment complete!"
    echo ""
    log_info "Deployment Outputs:"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    terraform output
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
fi

log_success "Done!"
