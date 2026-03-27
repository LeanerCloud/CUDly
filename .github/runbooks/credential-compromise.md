# Runbook: Credential Compromise

**Trigger**: A secret, API key, password, or access token has been exposed (committed to git, found in logs, leaked via third-party breach, etc.)

**Owner**: On-call engineer
**Severity**: P1 (if production credentials) / P2 (if dev/staging credentials)

---

## Step 1: Identify Scope (5 min)

Determine which credentials were compromised:

- [ ] AWS IAM credentials (access key ID + secret)?
- [ ] Azure Service Principal client secret?
- [ ] GCP Service Account JSON key?
- [ ] Database passwords (PostgreSQL)?
- [ ] Application secrets (session secret, JWT secret)?
- [ ] SMTP/email credentials?
- [ ] Terraform state credentials?

---

## Step 2: Rotate Immediately

Do NOT wait until you understand the full impact. Rotate first, investigate after.

### AWS Credentials

```bash
# Disable the old key immediately
aws iam update-access-key --access-key-id <OLD_KEY_ID> --status Inactive

# Create new key
aws iam create-access-key --user-name <IAM_USER>

# Update the secret in Secrets Manager
aws secretsmanager update-secret --secret-id <SECRET_ARN> --secret-string '{"access_key_id":"NEW","secret_access_key":"NEW"}'

# Delete old key after confirming new one works
aws iam delete-access-key --access-key-id <OLD_KEY_ID>
```

### Database Password (PostgreSQL on RDS)

```bash
# Generate a new strong password
NEW_PASS=$(openssl rand -base64 32)

# Rotate via AWS Secrets Manager (if using auto-rotation)
aws secretsmanager rotate-secret --secret-id <DB_PASSWORD_SECRET_ARN>

# Or manually via RDS
aws rds modify-db-instance --db-instance-identifier <DB_ID> \
  --master-user-password "$NEW_PASS" --apply-immediately

# Update the Secrets Manager value
aws secretsmanager put-secret-value --secret-id <DB_PASSWORD_SECRET_ARN> \
  --secret-string "{\"password\":\"$NEW_PASS\"}"
```

### Azure Client Secret

1. Go to Azure Portal → Azure Active Directory → App Registrations → CUDly app
2. Certificates & Secrets → Delete the old secret → Add new secret
3. Update in AWS Secrets Manager: `aws secretsmanager put-secret-value --secret-id <AZURE_CREDS_ARN> --secret-string '{"tenant_id":"...","client_id":"...","client_secret":"NEW_SECRET","subscription_id":"..."}'`

### Application Secrets (session-secret, jwt-secret)

```bash
# Generate new secret
NEW_SECRET=$(openssl rand -hex 32)

# Update in AWS Secrets Manager
aws secretsmanager put-secret-value --secret-id <SESSION_SECRET_ARN> \
  --secret-string "{\"value\":\"$NEW_SECRET\"}"

# IMPORTANT: This invalidates all existing sessions. Users will be logged out.
```

---

## Step 3: Invalidate Active Sessions

If application secrets were compromised, all existing user sessions must be invalidated:

```sql
-- Connect to the production database
DELETE FROM sessions WHERE created_at < NOW();

-- Or to be safe, invalidate all sessions
TRUNCATE TABLE sessions;
```

---

## Step 4: Investigate

After credentials are rotated:

- [ ] Determine when the credentials were first exposed (git blame, CloudTrail, log search)
- [ ] Check CloudTrail for unauthorized API calls using the compromised credentials:

  ```bash
  aws cloudtrail lookup-events \
    --lookup-attributes AttributeKey=AccessKeyId,AttributeValue=<OLD_KEY_ID> \
    --start-time <EXPOSURE_TIME> --end-time <ROTATION_TIME>
  ```

- [ ] Check AWS Config for resources created/modified during the exposure window
- [ ] Review CloudWatch Logs for unusual application activity

---

## Step 5: Notify

- [ ] Notify incident channel immediately with: "Credential X rotated. Investigating exposure window."
- [ ] If AWS credentials were used maliciously: contact AWS Support to assist with forensics
- [ ] If user data was accessed: initiate data breach response → see [data-breach-response.md](data-breach-response.md)
- [ ] If git commit: remove from history using BFG Repo Cleaner, force-push, notify all contributors to re-clone

---

## Step 6: Verify & Harden

- [ ] Confirm new credentials are working in production
- [ ] Add the exposed credential type to `.gitignore` / `.dockerignore` (if applicable)
- [ ] Enable gitleaks pre-commit hook to prevent future commits of secrets
- [ ] Add detection alert so this exposure type triggers an alarm in future
