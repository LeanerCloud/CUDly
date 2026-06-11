# Runbook: Terraform Stuck State Lock

## Symptoms

A Terraform workflow fails with an error similar to:

```text
Error: Error acquiring the state lock
...
Lock Info:
  ID:        <lock-id>
  Operation: OperationTypeApply
  ...
```

or the S3 backend reports the `.tflock` object already exists from a prior run.

## When this happens

A lock file is left behind when a CI run is interrupted (runner killed, job
cancelled mid-apply, network error during apply). The lock is NOT automatically
cleared on normal failure; the "Release state lock on failure" step handles that
for clean failures and cancellations within the same run. A lock persisting
across runs means a previous job exited abnormally before that cleanup step
ran.

## Safe recovery steps

Before clearing the lock, confirm the prior run that owns it is truly dead:

1. Note the `Lock Info.ID` from the error output.
2. In GitHub Actions, find the run that created the lock (check the timestamp in
   `Lock Info.Created`). Verify its status is "Failed" or "Cancelled" -- not
   still in progress.
3. If the owning run is still running, do NOT clear the lock. Wait for it to
   finish or cancel it explicitly first.

## Clearing the lock via workflow dispatch

Once you have confirmed the owning run is dead:

1. Go to **Actions > Deploy to AWS Fargate > Run workflow**.
2. Select the affected environment.
3. Check **"Force-clear a stale S3 state lock from a prior crashed run"**.
4. Click **Run workflow**.

The `Clear stale state lock (operator-triggered only)` step will remove the
`.tflock` object from S3 and then proceed with a normal deploy.

## Manual recovery (AWS CLI)

If you prefer to clear the lock outside CI:

```bash
# Replace <bucket> and <env> with the actual backend bucket and environment name
BUCKET=<bucket>
ENV=<env>
LOCK_KEY="github-fargate-${ENV}/terraform.tfstate.tflock"

aws s3 ls "s3://${BUCKET}/${LOCK_KEY}"   # confirm it exists
aws s3 rm "s3://${BUCKET}/${LOCK_KEY}"   # remove it
```

Then re-run the deployment workflow normally (without `clear_stale_lock`).

## Why locks are not cleared automatically on every run

Unconditionally deleting the lock before `terraform init` would allow two
concurrent deploys to race: the second run could remove the first run's active
lock, enabling parallel state writes and potential state corruption. The lock
must only be cleared when you know the holder is no longer active.

`deploy-aws-lambda.yml` follows the same pattern: cleanup only runs in the
`if: failure() || cancelled()` step of the same job, which covers the
within-run case; cross-run stuck locks require explicit operator action.
