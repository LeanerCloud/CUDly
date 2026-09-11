#!/bin/bash
# Setup git-secrets to prevent committing sensitive information

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo "========================================"
echo "Git Secrets Setup"
echo "========================================"
echo ""

# Check if git-secrets is installed
if ! command -v git-secrets &> /dev/null; then
    echo -e "${RED}✗ git-secrets not installed${NC}"
    echo ""
    echo "Installation instructions:"
    echo "  macOS: brew install git-secrets"
    echo "  Linux: git clone https://github.com/awslabs/git-secrets.git && cd git-secrets && make install"
    echo ""
    exit 1
fi

echo -e "${GREEN}✓ git-secrets is installed${NC}"
echo ""

# Install git hooks
#
# git-secrets 1.3.0's install_hook() writes and chmods the hook file, then
# reports success via a `say` call. `say` was never its own function:
# git-secrets sources git's git-sh-setup and relied on `say` being defined
# there (introduced in git-sh-setup.sh in 2009). Git removed it in commit
# 5b893f7d81 ("git-sh-setup.sh: remove 'say' function, change last users"),
# first shipped in Git 2.38 (2022), because it was undocumented and unused
# within git's own tree, breaking git-secrets as an unintended side effect
# of a git upgrade rather than a git-secrets regression. On macOS the bare
# `say` call resolves to /usr/bin/say (the text-to-speech binary) instead
# and exits 0 by accident; on Linux (or on a new-enough git anywhere) there
# is no such fallback, so it's "command not found" and `git secrets
# --install -f` returns non-zero even though every hook file was already
# written correctly. Define `say` as a no-op here and export it so the
# exported function is visible in the git-secrets child process on both
# platforms, making the real hook-writing exit status the one that reaches
# the check below.
say() { :; }
export -f say

echo "Installing git-secrets hooks..."
if git secrets --install -f; then
    echo -e "${GREEN}✓ Git hooks installed${NC}"
else
    echo -e "${RED}✗ Failed to install git hooks${NC}"
    exit 1
fi

# Register AWS secret patterns
echo ""
echo "Registering AWS secret patterns..."
git secrets --register-aws

# Add custom patterns
echo ""
echo "Adding custom secret patterns..."

# AWS patterns
git secrets --add 'AKIA[0-9A-Z]{16}'                                    # AWS Access Key ID
git secrets --add '[^A-Za-z0-9/+=]{40}[^A-Za-z0-9/+=]'                 # AWS Secret Access Key
git secrets --add 'aws(.{0,20})?['\''"][0-9a-zA-Z/+]{40}['\''"]'       # AWS Credentials

# GCP patterns
git secrets --add 'AIza[0-9A-Za-z_-]{35}'                              # GCP API Key

# Azure patterns
git secrets --add 'DefaultEndpointsProtocol=https'                      # Azure Connection String

# Generic secrets (require quoted values to avoid matching variable declarations)
git secrets --add 'password\s*[=:]\s*['\''"][^'\''"]{8,}'             # Password with quoted value
git secrets --add 'api[_-]?key\s*[=:]\s*['\''"][^'\''"]{8,}'         # API key with quoted value
git secrets --add 'secret[_-]?key\s*[=:]\s*['\''"][^'\''"]{8,}'      # Secret key with quoted value
git secrets --add 'BEGIN[[:space:]]((RSA|DSA|EC|OPENSSH|ENCRYPTED)[[:space:]])?PRIVATE[[:space:]]KEY-----'  # PEM private keys

# Database connection strings
git secrets --add 'postgres://[^:]+:[^@]+@'                           # PostgreSQL
git secrets --add 'mysql://[^:]+:[^@]+@'                              # MySQL
git secrets --add 'mongodb(\+srv)?://[^:]+:[^@]+@'                    # MongoDB

# Allowed patterns live in .gitallowed (versioned, applied by every scan, including CI).
# They are matched against the whole "path:line:content" scanner output line, so an entry
# must describe the benign literal or anchor on the path; a bare keyword whitelists
# every line that contains it (#1972).

echo -e "${GREEN}✓ Secret patterns registered${NC}"

# Scan existing repository
echo ""
echo "========================================"
echo "Scanning Existing Repository"
echo "========================================"
echo ""

if git secrets --scan -r; then
    echo ""
    echo -e "${GREEN}✓ No secrets found in repository${NC}"
else
    echo ""
    echo -e "${RED}✗ Secrets detected in repository!${NC}"
    echo "Please remove them before committing."
    exit 1
fi

echo ""
echo "========================================"
echo "Setup Complete"
echo "========================================"
echo ""
echo "Git-secrets is now active and will prevent commits containing secrets."
echo ""
echo "Useful commands:"
echo "  git secrets --scan        - Scan staged files"
echo "  git secrets --scan-history - Scan entire history"
echo "  git secrets --list        - List registered patterns"
echo ""
