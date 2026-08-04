//go:build ignore

// generate-federation-iac.go renders CUDly federation IaC templates locally.
//
// The templates in internal/iacfiles/templates/ are the single source of truth
// for all CUDly federation IaC — both this script and the CUDly backend API
// (GET /api/federation/iac) render output from exactly those files.
//
// The Terraform modules are in iac/federation/. For a self-contained deployment
// package, use --format bundle to generate a zip that includes both the pre-filled
// .tfvars file and the supporting Terraform module files.
//
// # Prerequisites
//
// Go 1.21+ in PATH. No other dependencies. Run from the repository root.
//
// # The --oidc-subject-claim flag
//
// Every AWS-target combination other than aws->aws federates via OIDC and
// requires --oidc-subject-claim: it is the workload subject the generated trust
// policy pins to, and without it the policy would accept every identity the
// issuer can mint (#1543, #1602, #1640). Pass the calling workload's subject:
// a GCP service account's numeric unique ID, or an Azure managed identity's
// object ID. The value is validated against an allowlist before anything is
// rendered; see validateOIDCSubjectClaim.
//
// The remaining combinations render nothing that reads this flag, so passing it
// there is an error rather than a no-op: a silently discarded subject claim
// would look like the trust was pinned when it was not. That is not the same as
// saying they need no pinning. Each gcp-target combination emits its own
// REQUIRED pin, none of which --oidc-subject-claim populates:
//
//	--source aws   -> <slug>-gcp-wif.tfvars, aws_role_name (blank)
//	--source azure -> <slug>-gcp-wif.tfvars, oidc_subject (blank)
//	--source gcp   -> <slug>-gcp-sa-impersonation.tfvars, source_service_account
//	                  (a <SOURCE_SERVICE_ACCOUNT_EMAIL> placeholder)
//
// # Quick examples
//
//	# AWS target, Azure source — Terraform tfvars
//	go run scripts/generate-federation-iac.go \
//	  --target aws --source azure \
//	  --account-name "prod-aws" --account-id "123456789012" \
//	  --tenant-id "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" \
//	  --oidc-subject-claim "11111111-2222-3333-4444-555555555555"
//
//	# AWS target, Azure source — CloudFormation parameters JSON
//	go run scripts/generate-federation-iac.go \
//	  --target aws --source azure --format cf-params \
//	  --account-name "prod-aws" --account-id "123456789012" \
//	  --tenant-id "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" \
//	  --oidc-subject-claim "11111111-2222-3333-4444-555555555555"
//
//	# AWS target, AWS source — cross-account IAM role tfvars
//	go run scripts/generate-federation-iac.go \
//	  --target aws --source aws \
//	  --account-name "target-aws" --account-id "999888777666"
//
//	# Azure target — WIF App Registration tfvars
//	go run scripts/generate-federation-iac.go \
//	  --target azure --source aws \
//	  --account-name "prod-azure" --account-id "sub-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" \
//	  --tenant-id "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
//
//	# GCP target, AWS source — WIF pool tfvars
//	go run scripts/generate-federation-iac.go \
//	  --target gcp --source aws \
//	  --account-name "prod-gcp" --account-id "my-gcp-project"
//
//	# GCP target, GCP source — service account impersonation tfvars
//	go run scripts/generate-federation-iac.go \
//	  --target gcp --source gcp \
//	  --account-name "target-gcp" --account-id "target-project-id"
//
//	# Bundle zip (tfvars + Terraform module + CF template for aws-target)
//	go run scripts/generate-federation-iac.go \
//	  --target aws --source azure --format bundle \
//	  --account-name "prod-aws" --account-id "123456789012" \
//	  --tenant-id "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" \
//	  --oidc-subject-claim "11111111-2222-3333-4444-555555555555"
//
//	# Print tfvars to stdout
//	go run scripts/generate-federation-iac.go \
//	  --target aws --source gcp \
//	  --account-name "prod" --account-id "123456789012" \
//	  --oidc-subject-claim "123456789012345678901" --output -

package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// iacData mirrors internal/api/handler_federation.go:federationIaCData.
// Keep in sync with any template variable changes.
type iacData struct {
	AccountName       string
	AccountExternalID string
	AccountSlug       string
	Source            string
	// AWS WIF / cross-account
	OIDCIssuerURL string
	OIDCAudience  string
	// OIDCSubjectClaim restricts the AWS trust policy to a single workload
	// subject. Empty unless --oidc-subject-claim is passed; every AWS-WIF
	// template requires it (no working subject-less default, see #1640).
	OIDCSubjectClaim string
	// Azure-specific
	SubscriptionID string
	TenantID       string
	// GCP-specific
	ProjectID           string
	ServiceAccountEmail string
	OIDCIssuerURI       string
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	return strings.Trim(slugRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

func awsOIDCIssuer(source, tenantID string) string {
	switch source {
	case "azure":
		if tenantID != "" {
			return "https://login.microsoftonline.com/" + tenantID + "/v2.0"
		}
		return "https://login.microsoftonline.com/<AZURE_TENANT_ID>/v2.0"
	case "gcp":
		return "https://accounts.google.com"
	default:
		return ""
	}
}

func awsOIDCAudience(source string) string {
	if source == "azure" {
		return "api://AzureADTokenExchange"
	}
	return "sts.amazonaws.com"
}

func gcpOIDCIssuerURI(source, tenantID string) string {
	if source == "azure" {
		if tenantID != "" {
			return "https://login.microsoftonline.com/" + tenantID + "/v2.0"
		}
		return "https://login.microsoftonline.com/<AZURE_TENANT_ID>/v2.0"
	}
	return ""
}

func renderTmpl(tmplPath string, data iacData) (string, error) {
	b, err := os.ReadFile(tmplPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w\n\nRun this script from the repository root directory.", tmplPath, err)
	}
	t, err := template.New("iac").Parse(string(b))
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", tmplPath, err)
	}
	var buf bytes.Buffer
	if err = t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render %s: %w", tmplPath, err)
	}
	return buf.String(), nil
}

func addToZip(zw *zip.Writer, name string, content []byte) error {
	f, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = f.Write(content)
	return err
}

// bundleModuleSpec returns the Terraform module directory, tfvars template path,
// and zip destination name for the given target/source combination.
func bundleModuleSpec(target, source, slug, templDir, modulesDir string) (moduleDir, tfvarsTmpl, tfvarsName string) {
	switch {
	case target == "aws" && source == "aws":
		return filepath.Join(modulesDir, "aws-cross-account", "terraform"),
			filepath.Join(templDir, "aws-cross-account.tfvars.tmpl"),
			"terraform/" + slug + "-aws-cross-account.tfvars"
	case target == "aws":
		return filepath.Join(modulesDir, "aws-target", "terraform"),
			filepath.Join(templDir, "aws-wif.tfvars.tmpl"),
			"terraform/" + slug + "-aws-wif.tfvars"
	case target == "azure":
		return filepath.Join(modulesDir, "azure-target", "terraform"),
			filepath.Join(templDir, "azure-wif.tfvars.tmpl"),
			"terraform/" + slug + "-azure-wif.tfvars"
	case target == "gcp" && source == "gcp":
		return filepath.Join(modulesDir, "gcp-sa-impersonation", "terraform"),
			filepath.Join(templDir, "gcp-sa-impersonation.tfvars.tmpl"),
			"terraform/" + slug + "-gcp-sa-impersonation.tfvars"
	default: // gcp, non-gcp source
		return filepath.Join(modulesDir, "gcp-target", "terraform"),
			filepath.Join(templDir, "gcp-wif.tfvars.tmpl"),
			"terraform/" + slug + "-gcp-wif.tfvars"
	}
}

// addTerraformToBundle writes the Terraform module files and generated .tfvars into zw.
func addTerraformToBundle(zw *zip.Writer, data iacData, target, source, slug, templDir, modulesDir string) error {
	moduleDir, tfvarsTmpl, tfvarsName := bundleModuleSpec(target, source, slug, templDir, modulesDir)
	entries, err := os.ReadDir(moduleDir)
	if err != nil {
		return fmt.Errorf("read module dir %s: %w\n\nRun from the repository root.", moduleDir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(moduleDir, entry.Name()))
		if err != nil {
			return err
		}
		if err = addToZip(zw, "terraform/"+entry.Name(), b); err != nil {
			return err
		}
	}
	content, err := renderTmpl(tfvarsTmpl, data)
	if err != nil {
		return err
	}
	return addToZip(zw, tfvarsName, []byte(content))
}

// addCFNToBundle writes CloudFormation files into zw for AWS WIF (non-same-cloud) bundles.
func addCFNToBundle(zw *zip.Writer, data iacData, source, slug, templDir, modulesDir string) error {
	cfTemplate, err := os.ReadFile(filepath.Join(modulesDir, "aws-target", "cloudformation", "template.yaml"))
	if err != nil {
		return fmt.Errorf("read cf template: %w", err)
	}
	if err = addToZip(zw, "cloudformation/template.yaml", cfTemplate); err != nil {
		return err
	}
	cfParams, err := renderTmpl(filepath.Join(templDir, "aws-wif-cf-params.json.tmpl"), data)
	if err != nil {
		return err
	}
	if err = addToZip(zw, "cloudformation/"+slug+"-cf-params.json", []byte(cfParams)); err != nil {
		return err
	}
	_ = source // used only to gate the call; available for future extension
	deployScript, err := renderTmpl(filepath.Join(templDir, "aws-cfn-deploy.sh.tmpl"), data)
	if err != nil {
		return err
	}
	return addToZip(zw, "cloudformation/deploy-cfn.sh", []byte(deployScript))
}

// buildBundle creates a zip: tfvars + Terraform module + (for aws WIF) CF files.
func buildBundle(data iacData, target, source, slug, templDir, modulesDir string) ([]byte, string, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	if err := addTerraformToBundle(zw, data, target, source, slug, templDir, modulesDir); err != nil {
		return nil, "", err
	}
	if target == "aws" && source != "aws" {
		if err := addCFNToBundle(zw, data, source, slug, templDir, modulesDir); err != nil {
			return nil, "", err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, "", fmt.Errorf("finalize zip: %w", err)
	}

	zipName := slug + "-" + target + "-federation-bundle.zip"
	switch {
	case target == "aws" && source == "aws":
		zipName = slug + "-aws-cross-account-bundle.zip"
	case target == "gcp" && source == "gcp":
		zipName = slug + "-gcp-sa-impersonation-bundle.zip"
	}
	return buf.Bytes(), zipName, nil
}

// singleFileTmpl returns the template filename and default output name for single-file mode.
func singleFileTmpl(target, source, format, slug string) (tmplFile, outName string, ok bool) {
	switch {
	case target == "aws" && source == "aws":
		return "aws-cross-account.tfvars.tmpl", slug + "-aws-cross-account.tfvars", true
	case target == "aws" && format == "cf-params":
		return "aws-wif-cf-params.json.tmpl", slug + "-aws-wif-cf-params.json", true
	case target == "aws":
		return "aws-wif.tfvars.tmpl", slug + "-aws-wif.tfvars", true
	case target == "azure":
		return "azure-wif.tfvars.tmpl", slug + "-azure-wif.tfvars", true
	case target == "gcp" && source == "gcp":
		return "gcp-sa-impersonation.tfvars.tmpl", slug + "-gcp-sa-impersonation.tfvars", true
	case target == "gcp":
		return "gcp-wif.tfvars.tmpl", slug + "-gcp-wif.tfvars", true
	default:
		return "", "", false
	}
}

// oidcSubjectClaimMaxLen bounds --oidc-subject-claim. The two subject formats
// this flag can carry are a GCP service account's numeric unique ID (typically
// 21 digits; Google documents it as a numeric string without guaranteeing a
// length) and an Azure managed identity's object ID (a 36-character UUID), so
// 255 sits far above any real value and exists only to keep an absurd argument
// out of the generated artifacts.
const oidcSubjectClaimMaxLen = 255

// displayClaim renders a rejected claim for an error message. Long values are
// truncated so that a multi-kilobyte argument cannot flood the operator's
// terminal; the true length is reported instead. Doing this here rather than
// relying on the length check to run first lets each rejection below report the
// most useful diagnosis without any of them having to worry about size.
func displayClaim(claim string) string {
	const maxShown = 64
	if len(claim) <= maxShown {
		return fmt.Sprintf("%q", claim)
	}
	// Cutting at a byte offset can split a multi-byte rune. %q escapes the
	// orphaned bytes rather than emitting them raw, which is also what keeps a
	// claim carrying terminal control sequences from reaching the terminal.
	return fmt.Sprintf("%q... (%d bytes total)", claim[:maxShown], len(claim))
}

// oidcSubjectClaimRE is a positive allowlist, not a denylist, because this
// script interpolates the value verbatim into three different grammars:
//   - Bash: aws-cfn-deploy.sh.tmpl renders it into the "OIDCSubjectClaim=..."
//     double-quoted word passed to `aws cloudformation deploy`.
//   - JSON: aws-wif-cf-params.json.tmpl renders it as a string value.
//   - HCL:  aws-wif.tfvars.tmpl renders it as a quoted attribute value.
//
// No single escaping helper is correct for all three, so the value is
// constrained to characters that are inert in every one of them: letters,
// digits and . _ : / @ = + - with a leading letter or digit so a value can
// never begin with '-' and be re-read as a flag by a downstream command.
// Excluded by construction are whitespace and $ * " ' ` \ ( ) { } ; & | < > ,
// % and newlines.
//
// This is deliberately stricter than the AllowedPattern ^[^\s*$]+$ that the
// CloudFormation template and the Terraform module enforce on the same value.
// Those two run on a value that is already a typed parameter, so they only need
// to reject what IAM itself mis-handles: '$' (IAM expands ${...} policy
// variables inside Condition values) and '*' (compared literally by
// StringEquals). This check runs earlier, on a value about to become shell,
// JSON and HCL *source*, so it has to reject the metacharacters of those
// grammars as well.
//
// The practical cost of the extra strictness is nil here: awsOIDCIssuer emits
// only login.microsoftonline.com, accounts.google.com, or "" for a source it
// does not recognise, so the only subjects a bundle from this script can
// legitimately pin are an Azure object-ID UUID and a GCP numeric unique ID.
// Subject formats from issuers that need the wider pattern, such as Auth0's
// "<connection>|<id>" and Bitbucket's "{repo-uuid}:{step-uuid}", cannot be
// produced by this script and remain deployable by editing the tfvars or the
// CFN parameters directly.
//
// For the same reason this disagrees with iac/federation/gcp-target/terraform/
// variables.tf, whose oidc_subject allowlist deliberately permits '|' for
// Auth0/Okta subjects. That value reaches a CEL attribute condition and an IAM
// principal path, where '|' is inert once quotes are excluded. This one reaches
// a Bash word, where '|' is a pipe. Same field name, different sinks, so the
// two allowlists are correctly different rather than accidentally divergent.
var oidcSubjectClaimRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@=+-]*$`)

// subjectClaimMode says what --oidc-subject-claim means for a given
// target/source pair, so an inapplicable value is rejected instead of silently
// dropped.
type subjectClaimMode int

const (
	// subjectClaimRequired: the pair renders an AWS-WIF trust policy, whose
	// :sub condition has no safe default. It is first so that the zero value of
	// subjectClaimMode is the strict one: a mode left unset fails closed.
	subjectClaimRequired subjectClaimMode = iota
	// subjectClaimNotApplicable: the pair renders no artifact that reads
	// .OIDCSubjectClaim, so a supplied claim would be silently discarded.
	//
	// This is narrower than "renders no trust to pin". Every gcp-target
	// combination emits a REQUIRED pin of its own: aws_role_name or
	// oidc_subject in gcp-wif.tfvars.tmpl, or source_service_account in
	// gcp-sa-impersonation.tfvars.tmpl when the source is gcp. Those are
	// different fields which this flag has never populated, and pinning them is
	// out of scope for --oidc-subject-claim. See the doc comment at the top of
	// this file for the full per-source table.
	subjectClaimNotApplicable
)

// subjectClaimModeFor reports whether --oidc-subject-claim feeds anything for
// this target/source pair. Only an AWS target with a non-AWS source renders the
// AWS-WIF artifacts: aws->aws is the cross-account path and selects
// aws-cross-account.tfvars.tmpl, whose role is trusted by source account plus
// external ID rather than by an OIDC :sub condition, and no azure or gcp
// template references .OIDCSubjectClaim at all. Those targets are not
// necessarily subject-less, they just pin their subject through a different
// variable (see the subjectClaimNotApplicable comment above).
func subjectClaimModeFor(target, source string) subjectClaimMode {
	if target == "aws" && source != "aws" {
		return subjectClaimRequired
	}
	return subjectClaimNotApplicable
}

// validateOIDCSubjectClaim checks --oidc-subject-claim before any template is
// rendered. Generation time is the only place this can be enforced for the
// shell artifact: the operator runs the generated deploy-cfn.sh, by which point
// the value is already Bash source. The same ordering problem exists in the
// server-rendered aws-wif-cli.sh, whose OIDC_SUBJECT_CLAIM guard runs *after*
// the OIDC_SUBJECT_CLAIM="${OIDC_SUBJECT_CLAIM:-<value>}" line that embeds the
// value, so a command substitution baked into the default has already executed
// by the time that guard inspects it. A validated value is the only control
// that runs before either.
func validateOIDCSubjectClaim(claim string, mode subjectClaimMode) error {
	if claim == "" {
		if mode == subjectClaimNotApplicable {
			return nil
		}
		return errors.New("--oidc-subject-claim is required when --target=aws and --source is not aws. " +
			"Without it the generated trust policy has no :sub condition and every identity the issuer " +
			"can mint is able to assume the role. Set it to the calling workload's subject claim: " +
			"a GCP service account's numeric unique ID (not its email), or an Azure managed identity's object ID")
	}
	// Applicability first: if the flag does not belong on this combination at
	// all, saying so is more useful than complaining about its contents.
	if mode == subjectClaimNotApplicable {
		return fmt.Errorf("--oidc-subject-claim %s is not applicable to this target/source combination. "+
			"It pins the AWS workload identity federation trust policy, which is only generated for "+
			"--target=aws with a non-aws --source. Drop the flag, or correct --target/--source",
			displayClaim(claim))
	}
	if len(claim) > oidcSubjectClaimMaxLen {
		return fmt.Errorf("--oidc-subject-claim is %d bytes, over the %d-byte limit",
			len(claim), oidcSubjectClaimMaxLen)
	}
	if !oidcSubjectClaimRE.MatchString(claim) {
		return fmt.Errorf("--oidc-subject-claim %s is not an accepted subject claim: it must start with a letter "+
			"or digit and contain only letters, digits and the characters . _ : / @ = + - . The value is "+
			"interpolated verbatim into the generated Bash, JSON and HCL artifacts, so shell metacharacters, "+
			"quotes and whitespace are rejected rather than escaped", displayClaim(claim))
	}
	return nil
}

// validTargets is the allowlist of target clouds, mirroring
// validFederationTargets in internal/api/handler_federation.go.
var validTargets = map[string]bool{"aws": true, "azure": true, "gcp": true}

// populateData fills target-specific fields on data from CLI flags. It reports
// an error rather than writing an invalid value into data, so a rejected flag
// stops the run before any template is rendered.
func populateData(data *iacData, target, source, tenantID, projectID, saEmail, oidcSubjectClaim string) error {
	// --target is checked first so that a typo there is reported as a bad
	// --target rather than as an inapplicable --oidc-subject-claim, which would
	// send the operator off to drop a flag that was never the problem.
	if !validTargets[target] {
		return fmt.Errorf("--target must be aws, azure, or gcp (got %q)", target)
	}
	// The claim is then validated outside the switch, so the check covers the
	// targets that must NOT carry a subject claim as well as the one that must.
	if err := validateOIDCSubjectClaim(oidcSubjectClaim, subjectClaimModeFor(target, source)); err != nil {
		return err
	}
	switch target {
	case "aws":
		data.OIDCIssuerURL = awsOIDCIssuer(source, tenantID)
		data.OIDCAudience = awsOIDCAudience(source)
		data.OIDCSubjectClaim = oidcSubjectClaim
	case "azure":
		data.SubscriptionID = data.AccountExternalID
		data.TenantID = tenantID
	case "gcp":
		data.ProjectID = projectID
		if data.ProjectID == "" {
			data.ProjectID = data.AccountExternalID
		}
		data.ServiceAccountEmail = saEmail
		if data.ServiceAccountEmail == "" {
			data.ServiceAccountEmail = "cudly@" + data.ProjectID + ".iam.gserviceaccount.com"
		}
		data.OIDCIssuerURI = gcpOIDCIssuerURI(source, tenantID)
	default:
		// Unreachable while validTargets and these arms agree; kept so that
		// adding a target to the map without an arm here fails loudly instead of
		// emitting an artifact with no target-specific fields filled in.
		return fmt.Errorf("--target %q is in validTargets but has no populateData branch", target)
	}
	return nil
}

func main() {
	target := flag.String("target", "", "Target cloud: aws, azure, gcp (required)")
	source := flag.String("source", "", "Source cloud: aws, azure, gcp (required)")
	accountName := flag.String("account-name", "", "Account display name (required)")
	accountID := flag.String("account-id", "", "Provider account ID — AWS 12-digit number, Azure subscription ID, GCP project ID (required)")
	accountSlug := flag.String("account-slug", "", "Slug used in output filenames (default: derived from --account-name)")
	format := flag.String("format", "", "Output format: cf-params | bundle (default: tfvars)")
	tenantID := flag.String("tenant-id", "", "Azure tenant ID (required when source or target is azure)")
	projectID := flag.String("project-id", "", "GCP project ID (defaults to --account-id when target is gcp)")
	saEmail := flag.String("service-account-email", "", "GCP service account email (defaults to cudly@<project>.iam.gserviceaccount.com)")
	oidcSubjectClaim := flag.String("oidc-subject-claim", "", "Subject (sub) claim restricting the AWS trust policy to one workload: a GCP service account's numeric unique ID or an Azure managed identity's object ID. Required when --target=aws and --source is not aws; there is no working default (see #1640). Letters, digits and . _ : / @ = + - only")
	outFile := flag.String("output", "", "Output file path; use '-' to print to stdout (default: derived filename in current directory)")
	templDir := flag.String("templates-dir", "internal/iacfiles/templates", "Path to templates directory (run from repo root)")
	modulesDir := flag.String("modules-dir", "iac/federation", "Path to Terraform modules directory (used by --format bundle)")
	flag.Parse()

	if *target == "" || *source == "" || *accountName == "" || *accountID == "" {
		fmt.Fprintln(os.Stderr, "Error: --target, --source, --account-name, and --account-id are required")
		flag.Usage()
		os.Exit(1)
	}

	slug := *accountSlug
	if slug == "" {
		slug = slugify(*accountName)
	}
	if slug == "" {
		slug = slugify(*accountID)
	}

	data := iacData{AccountName: *accountName, AccountExternalID: *accountID, AccountSlug: slug, Source: *source}
	if err := populateData(&data, *target, *source, *tenantID, *projectID, *saEmail, *oidcSubjectClaim); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if *format == "bundle" {
		runBundle(data, *target, *source, slug, *templDir, *modulesDir, *outFile)
		return
	}
	runSingleFile(data, *target, *source, *format, slug, *templDir, *outFile)
}

func runBundle(data iacData, target, source, slug, templDir, modulesDir, outFile string) {
	zipBytes, zipName, err := buildBundle(data, target, source, slug, templDir, modulesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	dest := outFile
	if dest == "" {
		dest = zipName
	}
	if err = os.WriteFile(dest, zipBytes, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: write %s: %v\n", dest, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Written: %s\n", dest)
}

func runSingleFile(data iacData, target, source, format, slug, templDir, outFile string) {
	tmplFile, outName, ok := singleFileTmpl(target, source, format, slug)
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: unsupported target/source combination: %s/%s\n", target, source)
		os.Exit(1)
	}
	content, err := renderTmpl(filepath.Join(templDir, tmplFile), data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if outFile == "-" {
		fmt.Print(content)
		return
	}
	dest := outFile
	if dest == "" {
		dest = outName
	}
	if err = os.WriteFile(dest, []byte(content), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: write %s: %v\n", dest, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Written: %s\n", dest)
}
