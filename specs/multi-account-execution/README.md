<!-- markdownlint-disable MD040 MD060 -->
# Spec: Multi-Account Execution

**Status**: Draft
**Created**: 2026-04-02
**Authors**: Engineering

## Problem Statement

CUDly currently manages a single AWS account (via IAM role), a single Azure subscription, and a single GCP project. Organizations using CUDly at scale run workloads across dozens or hundreds of cloud accounts. Cost-optimization tooling that cannot see all accounts simultaneously cannot produce accurate recommendations, cannot purchase commitments across an org, and forces users to log in separately to each account.

This feature enables CUDly to:

1. Manage any number of cloud accounts/subscriptions per provider
2. Collect recommendations and purchase commitments across all accounts in parallel
3. Display aggregated or per-account data in every view
4. Apply service-level settings globally or override them per account

## Scope

All three supported cloud providers are in scope:

- **AWS**: EC2, RDS, ElastiCache, OpenSearch, Redshift, Savings Plans — across any number of AWS accounts
- **Azure**: VM, SQL, Cosmos DB reservations — across any number of Azure subscriptions
- **GCP**: Compute, Cloud SQL committed use discounts — across any number of GCP projects

## Document Index

| Document | Contents |
|----------|----------|
| [data-model.md](./data-model.md) | New DB tables, column additions to existing tables, migration strategy |
| [api.md](./api.md) | New REST endpoints + modifications to existing endpoints |
| [backend.md](./backend.md) | Credential resolution, org discovery, parallel execution engine, service config cascade |
| [frontend.md](./frontend.md) | Settings UI (account CRUD), filter changes, plan account association, state model |
| [security.md](./security.md) | Credential encryption, access control, audit logging, input validation |
| [iac.md](./iac.md) | Terraform changes: Secrets Manager key, Lambda IAM policies, env vars for all environments |
| [acceptance.md](./acceptance.md) | BDD-style acceptance scenarios (the definition of done) |
| [tasks.md](./tasks.md) | Ordered implementation tasks with file paths, dependencies, and test requirements |

## Key Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| AWS auth modes | `access_keys`, `role_arn`, `bastion` | Covers all real-world patterns: simple dev accounts, cross-account role delegation, AWS Organizations with a central hub |
| Bastion pattern | One hub account (keys or role) assumes roles in N target accounts | Standard AWS Organizations security model; avoids giving CUDly direct keys to every account |
| AWS Org discovery | Add org root/management account → CUDly calls Organizations API to list member accounts | No separate discovery step; the management account IS the discovery mechanism |
| Service override cascade | Sparse: `NULL` fields in account override inherit the global default | Minimises configuration duplication; operators set global sensible defaults and only override what differs per account |
| Plan ↔ Account relationship | Many-to-many via `plan_accounts` join table | One plan can fan out across multiple accounts; one account can participate in multiple plans |
| Parallel execution | Backend goroutine fan-out per account; per-account errors are isolated | Failure in one account does not block others; results collected and tagged with `cloud_account_id` |
| Credential storage | AES-256-GCM encrypted blob in `account_credentials` table; key loaded from AWS Secrets Manager (ARN via `CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN` env) in prod; direct hex env var for local dev | Consistent with existing project secret management pattern; encryption key never in plaintext Lambda env |

## Glossary

| Term | Definition |
|------|-----------|
| **Cloud Account** | A single AWS account, Azure subscription, or GCP project managed by CUDly |
| **Bastion Account** | An AWS account whose credentials CUDly holds directly and uses to assume roles in other (target) accounts |
| **Org Root Account** | An AWS management account that has AWS Organizations access; used to discover member accounts |
| **Service Override** | A per-account, per-service config value that overrides the global default |
| **Plan Fan-out** | The process of executing a single purchase plan concurrently across all accounts it targets |
| **Effective Config** | The merged config for (account, provider, service): account overrides applied on top of global defaults |
