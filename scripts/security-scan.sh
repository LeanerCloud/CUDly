#!/bin/bash
# Comprehensive security scanning script for CUDly
# Runs multiple security scanners and generates a combined report

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Output directory
REPORT_DIR="security-reports"
mkdir -p "$REPORT_DIR"

echo "========================================"
echo "CUDly Security Scan"
echo "========================================"
echo ""

# Track overall status
OVERALL_STATUS=0

# Function to check if a command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Function to print section header
print_section() {
    echo ""
    echo "========================================"
    echo "$1"
    echo "========================================"
}

# 1. Go Security Scanner (gosec)
print_section "1. Go Security Scanner (gosec)"
if command_exists gosec; then
    if gosec -fmt=json -out="$REPORT_DIR/gosec-report.json" -exclude-dir=vendor -exclude-dir=testdata ./...; then
        echo -e "${GREEN}✓ gosec scan completed${NC}"
        # Also generate human-readable output
        gosec -fmt=text -out="$REPORT_DIR/gosec-report.txt" -exclude-dir=vendor -exclude-dir=testdata ./... || true
    else
        echo -e "${RED}✗ gosec found security issues${NC}"
        OVERALL_STATUS=1
    fi
else
    echo -e "${YELLOW}⚠ gosec not installed. Install: go install github.com/securego/gosec/v2/cmd/gosec@latest${NC}"
fi

# 2. Static Analysis (staticcheck)
print_section "2. Static Analysis (staticcheck)"
if command_exists staticcheck; then
    if staticcheck ./...; then
        echo -e "${GREEN}✓ staticcheck passed${NC}"
    else
        echo -e "${RED}✗ staticcheck found issues${NC}"
        OVERALL_STATUS=1
    fi
else
    echo -e "${YELLOW}⚠ staticcheck not installed. Install: go install honnef.co/go/tools/cmd/staticcheck@latest${NC}"
fi

# 3. Container Security (Trivy)
print_section "3. Container Security (Trivy)"
if command_exists trivy; then
    # Scan filesystem
    echo "Scanning filesystem..."
    if trivy fs --security-checks vuln,config,secret . \
        --format json \
        --output "$REPORT_DIR/trivy-fs-report.json" \
        --severity HIGH,CRITICAL; then
        echo -e "${GREEN}✓ Trivy filesystem scan completed${NC}"
    else
        echo -e "${YELLOW}⚠ Trivy found vulnerabilities${NC}"
        OVERALL_STATUS=1
    fi

    # Scan Docker image if it exists
    if docker images cudly:latest -q | grep -q .; then
        echo "Scanning Docker image..."
        if trivy image cudly:latest \
            --format json \
            --output "$REPORT_DIR/trivy-image-report.json" \
            --severity HIGH,CRITICAL; then
            echo -e "${GREEN}✓ Trivy image scan completed${NC}"
        else
            echo -e "${YELLOW}⚠ Trivy found vulnerabilities in Docker image${NC}"
            OVERALL_STATUS=1
        fi
    else
        echo "Docker image not found, skipping image scan"
    fi
else
    echo -e "${YELLOW}⚠ trivy not installed. Install: https://aquasecurity.github.io/trivy/${NC}"
fi

# 4. Terraform Security (tfsec)
print_section "4. Terraform Security (tfsec)"
if command_exists tfsec; then
    if tfsec terraform/ \
        --format json \
        --out "$REPORT_DIR/tfsec-report.json" \
        --soft-fail; then
        echo -e "${GREEN}✓ tfsec scan completed${NC}"
        # Also generate human-readable output
        tfsec terraform/ --format default --out "$REPORT_DIR/tfsec-report.txt" --soft-fail || true
    else
        echo -e "${YELLOW}⚠ tfsec found security issues${NC}"
        OVERALL_STATUS=1
    fi
else
    echo -e "${YELLOW}⚠ tfsec not installed. Install: https://aquasecurity.github.io/tfsec/${NC}"
fi

# 5. Dependency Check
print_section "5. Dependency Vulnerability Check"
if command_exists nancy; then
    echo "Checking Go dependencies with nancy..."
    if go list -json -m all | nancy sleuth; then
        echo -e "${GREEN}✓ nancy dependency check passed${NC}"
    else
        echo -e "${YELLOW}⚠ nancy found vulnerable dependencies${NC}"
        OVERALL_STATUS=1
    fi
else
    echo -e "${YELLOW}⚠ nancy not installed. Install: go install github.com/sonatype-nexus-community/nancy@latest${NC}"
fi

# 6. Go mod verify
print_section "6. Go Modules Verification"
if go mod verify; then
    echo -e "${GREEN}✓ go mod verify passed${NC}"
else
    echo -e "${RED}✗ go mod verify failed${NC}"
    OVERALL_STATUS=1
fi

# 7. Check for secrets in git history (if git-secrets is available)
print_section "7. Git Secrets Scan"
if command_exists git-secrets; then
    if git secrets --scan; then
        echo -e "${GREEN}✓ No secrets found in git${NC}"
    else
        echo -e "${RED}✗ Secrets found in git repository!${NC}"
        OVERALL_STATUS=1
    fi
else
    echo -e "${YELLOW}⚠ git-secrets not installed. Install: https://github.com/awslabs/git-secrets${NC}"
fi

# 8. Check for hardcoded credentials
print_section "8. Hardcoded Credentials Check"
echo "Searching for potential hardcoded credentials..."
CREDENTIAL_PATTERNS=(
    "password\s*=\s*['\"]"
    "api_key\s*=\s*['\"]"
    "secret\s*=\s*['\"]"
    "token\s*=\s*['\"]"
    "AWS_SECRET_ACCESS_KEY"
    "private_key"
)

FOUND_ISSUES=0
for pattern in "${CREDENTIAL_PATTERNS[@]}"; do
    if grep -r -n -i -E "$pattern" --include="*.go" --include="*.tf" --include="*.yaml" --include="*.yml" \
        --exclude-dir=vendor --exclude-dir=.git --exclude-dir=testdata --exclude-dir=node_modules . 2>/dev/null; then
        FOUND_ISSUES=1
    fi
done

if [ $FOUND_ISSUES -eq 0 ]; then
    echo -e "${GREEN}✓ No obvious hardcoded credentials found${NC}"
else
    echo -e "${YELLOW}⚠ Potential hardcoded credentials found (review above)${NC}"
    OVERALL_STATUS=1
fi

# Generate summary report
print_section "Security Scan Summary"
echo "Reports generated in: $REPORT_DIR/"
ls -lh "$REPORT_DIR/"

echo ""
if [ $OVERALL_STATUS -eq 0 ]; then
    echo -e "${GREEN}========================================${NC}"
    echo -e "${GREEN}✓ All security scans passed!${NC}"
    echo -e "${GREEN}========================================${NC}"
else
    echo -e "${YELLOW}========================================${NC}"
    echo -e "${YELLOW}⚠ Some security issues found${NC}"
    echo -e "${YELLOW}Review reports in: $REPORT_DIR/${NC}"
    echo -e "${YELLOW}========================================${NC}"
fi

exit $OVERALL_STATUS
