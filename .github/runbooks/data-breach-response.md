# Runbook: Data Breach Response

**Trigger**: Personal data (email addresses, passwords, MFA secrets, purchase history) may have been accessed or exfiltrated without authorization.

**Owner**: On-call engineer + management
**Severity**: P1

---

## Immediate Actions (first 30 min)

- [ ] **Do not panic or act hastily** — preservation of evidence is critical
- [ ] Assign Incident Commander immediately
- [ ] Open private incident channel; do not discuss in public channels or on public issue trackers
- [ ] Snapshot affected system logs **before making any changes**

---

## Step 1: Assess Scope

Determine what data may have been exposed:

| Data Type | Location | Sensitivity |
| --------- | -------- | ----------- |
| Email addresses | `users` table | High |
| Hashed passwords | `users` table | High |
| MFA secrets (TOTP) | `mfa_secrets` table | Critical |
| Session tokens | `sessions` table | Critical |
| API keys | `api_keys` table | High |
| Cloud account data | `cloud_accounts` table | High |
| Purchase history | `purchases` table | High |

Questions to answer:

- Which database tables were accessed?
- How many rows / which user records?
- Was the access read-only or did data leave the system?
- What was the attack vector?

---

## Step 2: Contain

- [ ] If breach is ongoing: take the service offline or restrict to maintenance mode
- [ ] Invalidate all active sessions immediately:

  ```sql
  TRUNCATE TABLE sessions;
  ```

- [ ] Rotate application secrets (session-secret) → see [credential-compromise.md](credential-compromise.md)
- [ ] If MFA secrets were exposed: disable TOTP for affected users and force re-enrolment
- [ ] If API keys were exposed: revoke all affected API keys:

  ```sql
  UPDATE api_keys SET revoked = true WHERE user_id IN (<affected_user_ids>);
  ```

- [ ] Block the attacker's IP/user-agent at WAF or security group level

---

## Step 3: Preserve Evidence

```bash
# Export relevant CloudWatch Logs before they expire
aws logs create-export-task \
  --log-group-name /aws/lambda/cudly \
  --from <START_TIMESTAMP_MS> \
  --to <END_TIMESTAMP_MS> \
  --destination <S3_BUCKET> \
  --destination-prefix incident-YYYY-MM-DD

# Capture RDS audit logs
aws rds download-db-log-file-portion \
  --db-instance-identifier <DB_ID> \
  --log-file-name <LOG_FILE>
```

---

## Step 4: Eradicate

- [ ] Patch the vulnerability that allowed the breach
- [ ] Deploy the fix to staging and verify
- [ ] Deploy to production

---

## Step 5: Notify (GDPR Art. 33 / 34)

**The 72-hour clock starts when you become aware of the breach.**

### Data Protection Authority (DPA)

If the breach involves EU residents' personal data and is likely to result in risk:

- [ ] File notification with the relevant DPA within **72 hours**
- Required information:
  - Nature of the breach (categories of data, approximate number of records)
  - Contact details of the DPO/responsible person
  - Likely consequences of the breach
  - Measures taken or proposed to address it

DPA contacts: <https://edpb.europa.eu/about-edpb/about-edpb/members_en>

### Affected Users

If the breach is likely to result in **high risk** to users' rights:

- Notify affected users **without undue delay** (aim for 24-48 hours after DPA notification)
- Include:
  - What happened (in plain language)
  - What data was involved
  - Recommended actions (change password, enable MFA, watch for phishing)
  - Contact for questions

---

## Step 6: Post-Breach Hardening

- [ ] Enable field-level encryption for sensitive columns (MFA secrets)
- [ ] Add database query audit logging
- [ ] Implement anomaly detection on query volume (detect bulk exports)
- [ ] Review and tighten IAM/database permissions
- [ ] Conduct a full security review of authentication flows
