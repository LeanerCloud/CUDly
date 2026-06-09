# Known Issues: Federation IaC — Old bundles need re-download for zero-touch registration

> **Audit status (2026-04-22):** `1 needs triage · 0 resolved (new file)`

Surfaced during the zero-touch federation bundle work
(`fix(iac): drop email gate from Terraform registration.tf across all bundles`,
`fix(iac): always run registration in CLI shell scripts; tighten error handling`).

## LOW: Customers with bundles downloaded before the zero-touch fix retain fail-quiet behaviour

**Description**:

Customers who downloaded a federation IaC bundle before the zero-touch fix landed
keep their old copy, which includes:

- Terraform `registration.tf` with `do_register = var.cudly_api_url != "" && var.contact_email != ""`
- CLI shell scripts gated on `if [[ -n "${CUDLY_CONTACT_EMAIL:-}" ]]; then ...`
- CFN deploy scripts with no registration curl call at all

These old bundles silently skip registration unless the customer manually edits
them. The fix (pre-filled `contact_email` from Session.Email, unconditional
registration) only takes effect in bundles downloaded after the fix ships.

**There is no way to back-fix already-downloaded bundles** — once downloaded,
they are the customer's own files.

**Fix**: Add a one-line notice to the next release notes / CHANGELOG entry:

> If you downloaded a federation IaC bundle before [release date], re-download it
> from the CUDly UI to get zero-touch registration (the bundle now auto-registers
> your account with no manual edits required).

**Effort**: trivial (~3 lines in the release notes/CHANGELOG). No code change needed.

**Status**: not yet triaged. Action required when the next release notes are
drafted.
