# Runbook: DDoS Mitigation

**Trigger**: Service is experiencing unusually high traffic volumes causing degraded performance or unavailability.

**Owner**: On-call engineer
**Severity**: P1 (service down) / P2 (degraded)

---

## Step 1: Confirm It's DDoS (5 min)

Check CloudWatch metrics:

```bash
# Lambda: request count spike
aws cloudwatch get-metric-statistics \
  --namespace AWS/Lambda \
  --metric-name Invocations \
  --period 60 --statistics Sum \
  --start-time $(date -u -d '30 minutes ago' +%Y-%m-%dT%H:%M:%SZ) \
  --end-time $(date -u +%Y-%m-%dT%H:%M:%SZ) \
  --dimensions Name=FunctionName,Value=<FUNCTION_NAME>

# CloudFront: request rate
aws cloudwatch get-metric-statistics \
  --namespace AWS/CloudFront \
  --metric-name Requests \
  --period 60 --statistics Sum \
  --start-time $(date -u -d '30 minutes ago' +%Y-%m-%dT%H:%M:%SZ) \
  --end-time $(date -u +%Y-%m-%dT%H:%M:%SZ)
```

Differentiate DDoS from legitimate traffic spike:

- DDoS: requests from single/few IPs, unusual user-agents, no session cookies, hitting non-existent endpoints
- Legitimate spike: distributed IPs, real user agents, normal endpoint distribution

---

## Step 2: Immediate Mitigation

### Option A: WAF Rate Limiting (if WAF is enabled)

```bash
# Add a rate-based rule blocking IPs with >1000 req/5min
aws wafv2 create-rule-group --scope CLOUDFRONT --name ddos-emergency \
  --capacity 100 --visibility-config ...
```

### Option B: Block IPs at Security Group Level

```bash
# Block specific attacker IP ranges
aws ec2 authorize-security-group-ingress \
  --group-id <SG_ID> \
  --protocol tcp --port 443 \
  --cidr <ATTACKER_CIDR> \
  --description "DDoS block $(date +%Y-%m-%d)"
```

Wait — security groups are ALLOW lists, not deny lists. Use NACLs to block:

```bash
# Block at NACL level (evaluated before security groups)
aws ec2 create-network-acl-entry \
  --network-acl-id <NACL_ID> \
  --rule-number 100 \
  --protocol -1 \
  --rule-action deny \
  --ingress \
  --cidr-block <ATTACKER_CIDR>
```

### Option C: Lambda Throttling (reduce blast radius)

```bash
# Set reserved concurrency to limit Lambda scale
aws lambda put-function-concurrency \
  --function-name <FUNCTION_NAME> \
  --reserved-concurrent-executions 50
```

### Option D: AWS Shield Advanced

If attacks are sustained and large-scale, engage AWS Shield Advanced:

1. Go to AWS Shield console
2. Enable Shield Advanced (if not already enabled)
3. Contact AWS DDoS Response Team (DRT): available 24/7 to Shield Advanced customers

---

## Step 3: Monitor and Adjust

```bash
# Watch CloudFront 5xx error rate
watch -n 10 aws cloudwatch get-metric-statistics \
  --namespace AWS/CloudFront --metric-name 5xxErrorRate \
  --period 60 --statistics Average \
  --start-time $(date -u -d '5 minutes ago' +%Y-%m-%dT%H:%M:%SZ) \
  --end-time $(date -u +%Y-%m-%dT%H:%M:%SZ)
```

---

## Step 4: Post-Mitigation

- [ ] Document attacker characteristics (IPs, ASNs, user-agents, request patterns)
- [ ] Enable WAF permanently with rate-based rules (see NET-001 recommendation)
- [ ] Enable AWS Shield Standard (free, always-on) at minimum for CloudFront distributions
- [ ] Consider Shield Advanced for production workloads
- [ ] Set up CloudWatch alarm for request rate spikes (>10x baseline)
- [ ] Remove temporary NACL/SG blocks once attack subsides
