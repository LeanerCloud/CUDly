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
canceled mid-apply, network error during apply). The workflow does NOT
auto-delete the lock on failure: a blanket failure-path delete would itself
risk state corruption, because a run that fails *because* it could not acquire
the lock would delete the lock another run is still actively holding. Terraform
releases its own lock on a clean apply error, so a lock that survives a run
means that run died abnormally; clearing it requires operator confirmation that
the owning run is dead.

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

## Why locks are not cleared automatically

Any unconditional lock delete (before `terraform init`, or in a blanket
failure-path cleanup step) lets two concurrent deploys race: one run could
remove another run's active lock, enabling parallel state writes and potential
state corruption. A failure-path delete is especially dangerous because a run
that fails *because* it lost the race to acquire the lock would then delete the
winner's live lock. The lock must only be cleared when you know the holder is no
longer active, which is why clearing is gated behind the operator-triggered
`clear_stale_lock` input rather than an automatic step.

Terraform releases its own lock on a clean apply error within the same run, so
this gating only affects the rarer case where a run dies abnormally before that
release happens.
