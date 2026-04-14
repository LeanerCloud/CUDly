# Secret-free Azure Workload Identity Federation

## Problem

The existing `workload_identity_federation` path for Azure is misnamed: it's
actually certificate-based client-assertion auth. The flow is:

1. `azure-wif-cli.sh` generates an RSA keypair + self-signed X.509 cert locally.
2. Uploads the public cert to the Azure AD App Registration via
   `az ad app credential reset --cert @...`.
3. Asks the operator to paste the private-key PEM into CUDly, which stores it
   as `azure_wif_private_key` (encrypted at rest).
4. At each Azure API call, CUDly mints a JWT signed with that stored private
   key, embeds the cert's x5t thumbprint, and presents it as `client_assertion`
   to `https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token`.

The private key is a long-lived secret that CUDly owns and can leak. This is
exactly what WIF is supposed to eliminate.

## Target

True Azure federation, where CUDly stores **no** Azure-specific secret:

1. CUDly's own AWS Lambda exposes OIDC discovery + JWKS endpoints
   (`/.well-known/openid-configuration`, `/.well-known/jwks.json`) on its
   Function URL. The JWKS publishes the public half of an AWS KMS asymmetric
   signing key (RSA_2048, SIGN_VERIFY). The private half never leaves KMS.
2. The Azure AD App Registration gets a **federated identity credential**
   (`az ad app federated-credential create`) pointing at CUDly's OIDC issuer
   with `subject=cudly-controller` and `audience=api://AzureADTokenExchange`.
3. At each Azure API call, CUDly mints a JWT with
   `iss = <cudly-url>`, `sub = cudly-controller`,
   `aud = api://AzureADTokenExchange`, signs it via `kms:Sign`, and presents
   it as `client_assertion`. Azure verifies the signature against the JWKS
   it fetches from the CUDly issuer, matches it against the federated
   credential entry, and issues a short-lived access token.

No PEMs, no passwords, no symmetric secrets.

## Architecture

```text
 ┌──────────────────────────────── AWS ─────────────────────────────────┐
 │                                                                      │
 │  ┌────────────────────┐   kms:Sign    ┌────────────────────────┐     │
 │  │ CUDly Lambda       │◀─────────────▶│ KMS asymmetric         │     │
 │  │ (api handler)      │  GetPublicKey │ (RSA_2048 SIGN_VERIFY) │     │
 │  └────────────────────┘               └────────────────────────┘     │
 │          │                                                           │
 │          │  serves on Function URL:                                  │
 │          ▼    GET /.well-known/openid-configuration                  │
 │          ▼    GET /.well-known/jwks.json                             │
 │                                                                      │
 └──────────────────────────────────────────────────────────────────────┘
            ▲
            │ Azure AD fetches JWKS to verify client_assertion
            │
 ┌──────────┴───────────────────── Azure ──────────────────────────────┐
 │                                                                      │
 │  Azure AD App Registration                                           │
 │   └── Federated identity credential                                  │
 │        issuer  = https://<cudly-lambda-url>                          │
 │        subject = cudly-controller                                    │
 │        audience = api://AzureADTokenExchange                         │
 │                                                                      │
 │  Token endpoint: /oauth2/v2.0/token                                  │
 │  ← client_assertion=<JWT signed by KMS>                              │
 │  → access_token                                                      │
 │                                                                      │
 └──────────────────────────────────────────────────────────────────────┘
```

## Implementation plan

One commit per layer, each independently reviewable.

1. **`specs/azure-wif-redesign.md`** (this file).
2. **`terraform/modules/compute/aws/lambda/kms.tf`** — new KMS asymmetric key,
   alias, IAM policy on the Lambda role for `kms:Sign` / `kms:GetPublicKey`,
   wiring through a new `signing_key_id` output. New Lambda env var
   `CUDLY_SIGNING_KEY_ID` (the KMS key ARN).
3. **`internal/oidc/signer.go`** — `KMSSigner` with:
   - `NewKMSSigner(ctx, keyARN)` fetches the public key once, caches it.
   - `Sign(ctx, claims)` marshals the JWT header + claims, SHA-256 hashes the
     signing input, calls `kms:Sign` with `RSASSA_PKCS1_V1_5_SHA_256`, returns
     the compact serialized JWT.
   - `PublicJWK()` returns the RFC 7517 JSON Web Key (`kty=RSA`, `use=sig`,
     `alg=RS256`, `kid=<key-id-hash>`).
4. **`internal/api/handler_oidc.go`** — two new routes, public (no auth):
   - `GET /.well-known/openid-configuration`
   - `GET /.well-known/jwks.json`
5. **`internal/credentials/azure_federated.go`** — a new
   `azcore.TokenCredential` that uses the signer to mint a client_assertion
   JWT and exchanges it at Azure's token endpoint via
   `client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer`.
   `ResolveAzureTokenCredential` routes to this path when
   `AzureAuthMode == "workload_identity_federation"` and **no** stored
   `azure_wif_private_key` credential is present (backward-compat: legacy
   cert-based accounts keep working during the transition).
6. **`internal/iacfiles/templates/azure-wif-cli.sh.tmpl`** — drop the cert
   generation, the `az ad app credential reset`, and the certificate env-var
   prompt. Add `az ad app federated-credential create` using the CUDly
   Lambda URL as the issuer and `cudly-controller` as the subject.
7. Tests: unit tests for `KMSSigner` (with a fake KMS client), handler tests
   for the two new routes, template rendering test asserting the new CLI
   shape.

## Subject value

`sub = "cudly-controller"` is a single global string. Azure federated
credentials are bound per-app-registration, so the `sub` doesn't need to be
unique across CUDly deployments — each target tenant's federated credential
entry already names the deployment's issuer URL (the Lambda Function URL),
which **is** unique per CUDly deployment.

A per-tenant-account subject would add complexity without meaningful
separation — the subject just has to match what the federated credential was
registered with, and a fixed constant is easier to operate.

## Key rotation

KMS handles rotation. The JWKS endpoint always publishes whatever
`GetPublicKey` returns for the configured key ARN. Azure refreshes its JWKS
cache periodically (default ~24h). Rotation flow:

1. Create a new KMS key (or use key versions if KMS supports them for
   asymmetric keys — verify; last I checked asymmetric keys do NOT support
   auto-rotation, so rotation means a new key ARN).
2. Update `CUDLY_SIGNING_KEY_ID` env var, redeploy.
3. Operators wait ~24h for Azure to refresh JWKS, then delete the old key.

If simultaneous validity is needed during rotation, the JWKS endpoint can
publish multiple keys and the signer can cycle; deferred as future work.

## Out of scope (this redesign)

- GCP source-cloud federation. GCP→Azure uses a different issuer (the GCP
  SA token endpoint) and is a separate follow-up.
- Multiple signing keys / rotation without downtime. Single key, single `kid`.
- Removing the cert-based `workload_identity_federation` path from
  `internal/credentials/resolver.go`. Kept for backward compatibility with
  existing accounts; the routing just prefers the federated path when no
  stored PEM is present.
- Database schema change. `azure_wif_private_key` credential type stays as a
  legacy value.
