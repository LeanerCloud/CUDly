#!/bin/bash
# Initialize Terraform S3 backend for state management
# Usage: ./scripts/init-backend.sh <environment> <region>
# Example: ./scripts/init-backend.sh dev us-east-1

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored messages
print_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Default values
AWS_PROFILE=""
ENVIRONMENT=""
REGION=""

# Parse command line arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --profile)
      AWS_PROFILE="$2"
      shift 2
      ;;
    --region)
      REGION="$2"
      shift 2
      ;;
    --environment)
      ENVIRONMENT="$2"
      shift 2
      ;;
    *)
      # Legacy positional arguments
      if [ -z "$ENVIRONMENT" ]; then
        ENVIRONMENT="$1"
      elif [ -z "$REGION" ]; then
        REGION="$1"
      else
        print_error "Unknown argument: $1"
        exit 1
      fi
      shift
      ;;
  esac
done

# Check required arguments
if [ -z "$ENVIRONMENT" ] || [ -z "$REGION" ]; then
    print_error "Usage: $0 --environment <env> --region <region> [--profile <profile>]"
    print_error "   or: $0 <environment> <region>  # Legacy format"
    print_error "Example: $0 --environment dev --region us-east-1 --profile personal"
    exit 1
fi

# Validate environment
if [[ ! "$ENVIRONMENT" =~ ^(dev|staging|prod)$ ]]; then
    print_error "Environment must be one of: dev, staging, prod"
    exit 1
fi

# Configuration
PROJECT_NAME="cudly"
BUCKET_NAME="${PROJECT_NAME}-terraform-state-${ENVIRONMENT}"
DYNAMODB_TABLE="${PROJECT_NAME}-terraform-locks-${ENVIRONMENT}"

print_info "Initializing Terraform backend for ${ENVIRONMENT} environment in ${REGION}"
print_info "S3 Bucket: ${BUCKET_NAME}"
print_info "DynamoDB Table: ${DYNAMODB_TABLE}"

# Check if AWS CLI is installed
if ! command -v aws &> /dev/null; then
    print_error "AWS CLI is not installed. Please install it first."
    exit 1
fi

# Build AWS CLI command with optional profile
AWS_CMD="aws"
if [ -n "$AWS_PROFILE" ]; then
    AWS_CMD="aws --profile $AWS_PROFILE"
    print_info "Using AWS Profile: ${AWS_PROFILE}"
fi

# Check AWS credentials
if ! $AWS_CMD sts get-caller-identity &> /dev/null; then
    print_error "AWS credentials not configured or invalid"
    if [ -n "$AWS_PROFILE" ]; then
        print_error "Check profile: $AWS_PROFILE"
    else
        print_error "Run: aws configure"
    fi
    exit 1
fi

ACCOUNT_ID=$($AWS_CMD sts get-caller-identity --query Account --output text)
print_info "AWS Account ID: ${ACCOUNT_ID}"

# ==============================================
# Create S3 Bucket for Terraform State
# ==============================================

print_info "Creating S3 bucket: ${BUCKET_NAME}"

# Check if bucket already exists
if $AWS_CMD s3 ls "s3://${BUCKET_NAME}" 2>&1 | grep -q 'NoSuchBucket'; then
    # Create bucket
    if [ "$REGION" == "us-east-1" ]; then
        # us-east-1 doesn't require LocationConstraint
        $AWS_CMD s3api create-bucket \
            --bucket "${BUCKET_NAME}" \
            --region "${REGION}"
    else
        $AWS_CMD s3api create-bucket \
            --bucket "${BUCKET_NAME}" \
            --region "${REGION}" \
            --create-bucket-configuration LocationConstraint="${REGION}"
    fi

    print_info "S3 bucket created successfully"
else
    print_warn "S3 bucket already exists"
fi

# Enable versioning
print_info "Enabling S3 bucket versioning"
$AWS_CMD s3api put-bucket-versioning \
    --bucket "${BUCKET_NAME}" \
    --versioning-configuration Status=Enabled \
    --region "${REGION}"

# Enable encryption
print_info "Enabling S3 bucket encryption"
$AWS_CMD s3api put-bucket-encryption \
    --bucket "${BUCKET_NAME}" \
    --server-side-encryption-configuration '{
        "Rules": [{
            "ApplyServerSideEncryptionByDefault": {
                "SSEAlgorithm": "AES256"
            },
            "BucketKeyEnabled": true
        }]
    }' \
    --region "${REGION}"

# Block public access
print_info "Blocking public access to S3 bucket"
$AWS_CMD s3api put-public-access-block \
    --bucket "${BUCKET_NAME}" \
    --public-access-block-configuration \
        BlockPublicAcls=true,\
IgnorePublicAcls=true,\
BlockPublicPolicy=true,\
RestrictPublicBuckets=true \
    --region "${REGION}"

# Enable lifecycle policy to delete old versions
print_info "Configuring S3 lifecycle policy"
LIFECYCLE_JSON=$(mktemp)
trap 'rm -f "$LIFECYCLE_JSON"' EXIT
cat > "$LIFECYCLE_JSON" <<EOF
{
    "Rules": [{
        "ID": "DeleteOldVersions",
        "Status": "Enabled",
        "Filter": {},
        "NoncurrentVersionExpiration": {
            "NoncurrentDays": 90
        },
        "AbortIncompleteMultipartUpload": {
            "DaysAfterInitiation": 7
        }
    }]
}
EOF

$AWS_CMD s3api put-bucket-lifecycle-configuration \
    --bucket "${BUCKET_NAME}" \
    --lifecycle-configuration "file://${LIFECYCLE_JSON}" \
    --region "${REGION}"

# Add bucket tags
print_info "Adding tags to S3 bucket"
$AWS_CMD s3api put-bucket-tagging \
    --bucket "${BUCKET_NAME}" \
    --tagging "TagSet=[
        {Key=Project,Value=${PROJECT_NAME}},
        {Key=Environment,Value=${ENVIRONMENT}},
        {Key=ManagedBy,Value=Terraform},
        {Key=Purpose,Value=TerraformState}
    ]" \
    --region "${REGION}"

# ==============================================
# Create DynamoDB Table for State Locking
# ==============================================

print_info "Creating DynamoDB table: ${DYNAMODB_TABLE}"

# Check if table already exists
if $AWS_CMD dynamodb describe-table \
    --table-name "${DYNAMODB_TABLE}" \
    --region "${REGION}" &> /dev/null; then
    print_warn "DynamoDB table already exists"
else
    # Create table
    $AWS_CMD dynamodb create-table \
        --table-name "${DYNAMODB_TABLE}" \
        --attribute-definitions AttributeName=LockID,AttributeType=S \
        --key-schema AttributeName=LockID,KeyType=HASH \
        --billing-mode PAY_PER_REQUEST \
        --tags \
            Key=Project,Value="${PROJECT_NAME}" \
            Key=Environment,Value="${ENVIRONMENT}" \
            Key=ManagedBy,Value=Terraform \
            Key=Purpose,Value=TerraformStateLocking \
        --region "${REGION}"

    print_info "Waiting for DynamoDB table to be active..."
    $AWS_CMD dynamodb wait table-exists \
        --table-name "${DYNAMODB_TABLE}" \
        --region "${REGION}"

    print_info "DynamoDB table created successfully"
fi

# Enable point-in-time recovery (for production)
if [ "$ENVIRONMENT" == "prod" ]; then
    print_info "Enabling point-in-time recovery for production"
    $AWS_CMD dynamodb update-continuous-backups \
        --table-name "${DYNAMODB_TABLE}" \
        --point-in-time-recovery-specification PointInTimeRecoveryEnabled=true \
        --region "${REGION}"
fi

# ==============================================
# Update Terraform Backend Configuration
# ==============================================

BACKEND_CONFIG_FILE="terraform/environments/aws/${ENVIRONMENT}/backend.tf"

print_info "Creating backend configuration file: ${BACKEND_CONFIG_FILE}"

mkdir -p "terraform/environments/aws/${ENVIRONMENT}"

cat > "${BACKEND_CONFIG_FILE}" <<EOF
# Terraform Backend Configuration
# Auto-generated by scripts/init-backend.sh
# DO NOT EDIT MANUALLY

terraform {
  backend "s3" {
    bucket         = "${BUCKET_NAME}"
    key            = "${ENVIRONMENT}/terraform.tfstate"
    region         = "${REGION}"
    encrypt        = true
    dynamodb_table = "${DYNAMODB_TABLE}"

    # Optional: Use KMS for encryption
    # kms_key_id = "arn:aws:kms:${REGION}:${ACCOUNT_ID}:key/YOUR-KMS-KEY-ID"
  }
}
EOF

print_info "Backend configuration file created"

# ==============================================
# Summary
# ==============================================

echo ""
print_info "Terraform backend initialized successfully!"
echo ""
echo "Backend Configuration:"
echo "  S3 Bucket:       ${BUCKET_NAME}"
echo "  DynamoDB Table:  ${DYNAMODB_TABLE}"
echo "  Region:          ${REGION}"
echo "  Config File:     ${BACKEND_CONFIG_FILE}"
echo ""
echo "Next steps:"
echo "  1. cd terraform/environments/aws/${ENVIRONMENT}"
echo "  2. cp terraform.tfvars.example terraform.tfvars"
echo "  3. Edit terraform.tfvars with your configuration"
echo "  4. terraform init"
echo "  5. terraform plan"
echo "  6. terraform apply"
echo ""
print_warn "Note: The backend configuration has been uncommented in ${BACKEND_CONFIG_FILE}"
print_warn "You will need to run 'terraform init' to migrate state to S3"
echo ""
