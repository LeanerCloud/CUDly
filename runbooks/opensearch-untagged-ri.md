# Runbook: OpenSearch RI purchased but not tagged

## Alert

Log pattern (grep / CloudWatch Logs Insights):

```text
OPENSEARCH_TAG_FAILED commitment_id=<id> error=<msg>
```

Emitted by `providers/aws/services/opensearch/client.go` when
`opensearch:AddTags` fails after a successful RI purchase. The RI is active;
only the CUDly source tag is absent.

## Background

AWS's `PurchaseReservedInstanceOffering` API has no inline `Tags` field.
CUDly attempts a best-effort `AddTags` call after purchase using a
constructed ARN of the form:

```text
arn:aws:es:<region>:<account>:reserved-instance/<uuid>
```

AWS has not officially documented `reserved-instance` as a supported ARN
type for `opensearch:AddTags` (only `domain`, `data-source`, and
`application` are listed). The call is wrapped in `retry.ErrPermanent` so
the retry budget is not exhausted on calls AWS will never accept. If AWS
extends support, the tag call will start succeeding with no code change.

## Impact

The RI is purchased and active. Cost attribution and audit queries that
rely on the CUDly source tag (key: `cudly:purchase-source`) will not find
this reservation unless it is manually tagged.

## Remediation

1. Identify the untagged RI from the log line:

   ```text
   OPENSEARCH_TAG_FAILED commitment_id=<ri-uuid> error=...
   ```

2. Tag it manually via the AWS CLI:

   ```bash
   REGION=<region>
   ACCOUNT=<account-id>
   RI_ID=<ri-uuid>
   SOURCE=<purchase-source>   # e.g. cudly-cli or cudly-web

   aws opensearch add-tags \
     --arn "arn:aws:es:${REGION}:${ACCOUNT}:reserved-instance/${RI_ID}" \
     --tag-list \
       Key=Purpose,Value="Reserved Instance Purchase" \
       Key=Tool,Value=CUDly \
       "Key=cudly:purchase-source,Value=${SOURCE}"
   ```

   If the call returns a `ValidationException` the ARN type is still
   unsupported by AWS; proceed to the fallback below.

3. **Fallback (AWS still rejects reserved-instance ARN):** Tag the parent
   OpenSearch domain instead, or record the RI ID in your cost-allocation
   spreadsheet until AWS adds native support.

## Follow-up

If this alert fires repeatedly (not just on ValidationException but on
transient errors), open an issue to add a retry with backoff instead of
the current permanent-error short-circuit. Reference issue #250.

If AWS releases documentation confirming `reserved-instance` support for
`AddTags`, remove the `retry.ErrPermanent` wrapper in
`providers/aws/services/opensearch/client.go:tagReservedInstance` and
update this runbook.
