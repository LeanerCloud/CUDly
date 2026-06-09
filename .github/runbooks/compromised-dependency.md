# Runbook: Compromised Dependency

**Trigger**: A Go module, npm package, Docker base image, or GitHub Action used by CUDly has been found to be malicious or critically vulnerable.

**Owner**: On-call engineer
**Severity**: P1 (active exploitation) / P2 (known critical CVE, no evidence of exploitation)

---

## Step 1: Assess Impact

```bash
# For Go dependency CVE
govulncheck ./...

# For npm dependency CVE
cd frontend && npm audit

# For Docker image CVE
trivy image <IMAGE_URI>

# For GitHub Actions compromise
# Check: https://github.com/advisories (filter by Actions category)
```

Questions to answer:

- Is the vulnerable code path reachable in production?
- Does exploitation require authentication?
- Is there evidence of active exploitation in logs?

---

## Step 2: Pin to Safe Version Immediately

### Go Dependency

```bash
# Pin to last known-good version
go get github.com/affected/package@v1.2.3-safe

# If no safe version exists, pin to a known commit hash
go get github.com/affected/package@<SAFE_COMMIT_SHA>

go mod tidy
go mod verify

# Run tests
go test ./...
```

### npm Dependency

```bash
cd frontend

# Force a specific version
npm install affected-package@<SAFE_VERSION>

# Or use npm audit fix
npm audit fix

# Verify
npm audit
npm test
```

### Docker Base Image

Update the `FROM` line in Dockerfile(s) to a patched version:

```dockerfile
# Before
FROM golang:1.25.4-alpine3.21

# After (example: patch to new minor that fixes CVE)
FROM golang:1.25.5-alpine3.21
```

Rebuild and push the container image.

### GitHub Action

Pin the compromised action to the last known-good commit SHA:

```yaml
# Before (vulnerable)
- uses: some-org/some-action@v1.2.3

# After (pinned to safe commit)
- uses: some-org/some-action@<SAFE_COMMIT_SHA>
```

Or remove the action entirely and replace with equivalent logic using trusted actions or shell commands.

---

## Step 3: Deploy the Fix

```bash
# Tag and push
git add go.mod go.sum Dockerfile
git commit -m "security: pin <package> to safe version (CVE-YYYY-XXXXX)"
git push

# CI/CD will build and deploy
# If CI is also affected by the compromised dependency, run manually:
make build && make push
```

---

## Step 4: Investigate for Active Exploitation

```bash
# Check application logs for exploitation patterns
aws logs filter-log-events \
  --log-group-name /aws/lambda/cudly \
  --filter-pattern "<exploitation_signature>" \
  --start-time <CVE_DISCLOSURE_EPOCH_MS>

# Check CloudTrail for unexpected API calls from application role
aws cloudtrail lookup-events \
  --lookup-attributes AttributeKey=Username,AttributeValue=<APP_ROLE_NAME> \
  --start-time <CVE_DISCLOSURE_TIME>
```

---

## Step 5: Verify & Post-Mortem

- [ ] Confirm `govulncheck` / `npm audit` / `trivy` no longer report the CVE
- [ ] Run full test suite
- [ ] Deploy to staging, then production
- [ ] Document the incident timeline
- [ ] Add the CVE to monitoring rules (detect if patched version is reverted)
- [ ] Review dependency update cadence — consider automating with Dependabot/Renovate
- [ ] Review GitHub Actions pinning strategy → see SC-003 recommendation
