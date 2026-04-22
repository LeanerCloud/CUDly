package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"text/template"

	"github.com/google/uuid"

	cudlyiac "github.com/LeanerCloud/CUDly/iac"
	"github.com/LeanerCloud/CUDly/internal/iacfiles"
	"github.com/aws/aws-lambda-go/events"
)

// federationIaCData holds the template variables used when rendering IaC templates.
type federationIaCData struct {
	AccountName       string
	AccountExternalID string
	AccountSlug       string
	Source            string
	SourceAccountID   string // AWS account ID where CUDly runs (resolved via STS)
	// AWS WIF / cross-account
	OIDCIssuerURL  string
	OIDCIssuerHost string // issuer URL without https:// prefix (used as IAM condition key)
	OIDCAudience   string
	// Azure-specific
	SubscriptionID string
	TenantID       string
	// GCP-specific
	ProjectID           string
	ServiceAccountEmail string
	OIDCIssuerURI       string
	CUDlyAPIURL         string
	// ContactEmail is the email of the authenticated user who downloaded the bundle.
	// Pre-filled from Session.Email — no manual entry required.
	ContactEmail string
}

// FederationIaCResponse is returned by the /api/federation/iac endpoint.
type FederationIaCResponse struct {
	Filename        string `json:"filename"`
	Content         string `json:"content"`
	ContentType     string `json:"content_type"`
	ContentEncoding string `json:"content_encoding,omitempty"` // "base64" for binary (zip)
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
	switch source {
	case "azure":
		if tenantID != "" {
			return "https://login.microsoftonline.com/" + tenantID + "/v2.0"
		}
		return "https://login.microsoftonline.com/<AZURE_TENANT_ID>/v2.0"
	default:
		return ""
	}
}

// renderTemplate renders a named template from the embedded iacfiles.Templates FS.
func renderTemplate(tmplPath string, data federationIaCData) (string, error) {
	tmplBytes, err := iacfiles.Templates.ReadFile(tmplPath)
	if err != nil {
		return "", fmt.Errorf("read template %s: %w", tmplPath, err)
	}
	tmpl, err := template.New("iac").Parse(string(tmplBytes))
	if err != nil {
		return "", fmt.Errorf("parse template %s: %w", tmplPath, err)
	}
	var buf bytes.Buffer
	if err = tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render template %s: %w", tmplPath, err)
	}
	return buf.String(), nil
}

// getFederationIaC handles GET /api/federation/iac
// Query params: target, source, format — all required.
//
// Generates generic IaC templates for self-registration. Target account owners
// fill in their own values via -var flags when running terraform apply.
//
// The rendered template embeds the CUDly host's AWS account ID (resolved via
// STS GetCallerIdentity) so target-account deployments can trust the right
// source. That identity must not leak to unauthenticated callers — the
// endpoint requires view:accounts, matching the audience that legitimately
// consumes these templates (admins onboarding cross-account federations).
//
//   - format="cli"      → self-contained shell script (single file)
//   - format="cfn"      → zip with CFN template + params + deploy script (aws target only)
//   - format="bicep"    → zip with Bicep template + params + deploy script (azure target only)
//   - format="arm"      → zip with ARM JSON template + params + deploy script (azure target only)
//   - format="bundle"   → zip with Terraform module + pre-filled tfvars (and CFN fallback on aws)
func (h *Handler) getFederationIaC(ctx context.Context, req *events.LambdaFunctionURLRequest) (*FederationIaCResponse, error) {
	session, err := h.requirePermission(ctx, req, "view", "accounts")
	if err != nil {
		return nil, err
	}

	target, source, format, err := federationIaCParams(req.QueryStringParameters)
	if err != nil {
		return nil, err
	}

	apiURL := deriveFederationAPIURL(h.dashboardURL, req.RequestContext.DomainName)
	data := buildGenericIaCData(target, source, apiURL)
	if err = h.populateSourceAccountID(ctx, source, &data); err != nil {
		return nil, err
	}
	// ContactEmail is always the email of the authenticated user who requested
	// the bundle — the route is Auth: AuthUser so session.Email is always set
	// for normal traffic. No env-var override, no admin-curated fallback: the
	// person who downloads the bundle IS the contact.
	data.ContactEmail = session.Email

	if formatNeedsZip(format) {
		return h.buildZipResponse(data, target, source, format, "target")
	}
	return h.renderSingleFile(data, target, source, format)
}

// deriveFederationAPIURL returns the configured dashboard URL, or derives it
// from the Lambda Function URL domain name (trusted, not the Host header).
func deriveFederationAPIURL(configured, lambdaDomain string) string {
	if configured != "" {
		return configured
	}
	if lambdaDomain != "" {
		return "https://" + lambdaDomain
	}
	return ""
}

// populateSourceAccountID fills data.SourceAccountID when CUDly runs on AWS,
// and returns an error if the cross-account bundle would embed an empty account ID.
// WIF targets (source=gcp|azure) don't use SourceAccountID — their trust is based
// on the OIDC issuer, not CUDly's AWS account number, so no error is returned for them.
func (h *Handler) populateSourceAccountID(ctx context.Context, source string, data *federationIaCData) error {
	if sourceCloud() != "aws" {
		return nil
	}
	data.SourceAccountID = h.resolveSourceAccountID(ctx)
	if source == "aws" && data.SourceAccountID == "" {
		// resolveAWSAccountID returns "" on AWS SDK config load failure or STS
		// GetCallerIdentity failure. Check Lambda logs for the preceding
		// "Failed to resolve source account ID via STS" warning, and verify
		// the execution role has sts:GetCallerIdentity and AWS_REGION is set.
		return fmt.Errorf("federation iac: CUDly failed to resolve its own AWS account ID; " +
			"check that the execution role has sts:GetCallerIdentity and AWS_REGION is set")
	}
	return nil
}

// renderSingleFile renders a single-file IaC template (currently only "cli" is supported).
func (h *Handler) renderSingleFile(data federationIaCData, target, source, format string) (*FederationIaCResponse, error) {
	tmplPath, filename, contentType, err := singleFileSpec(target, source, format, "target")
	if err != nil {
		return nil, err
	}
	renderData := data
	if format == "cli" {
		renderData = shellEscapeData(data)
	}
	content, err := renderTemplate(tmplPath, renderData)
	if err != nil {
		return nil, fmt.Errorf("federation iac: %w", err)
	}
	return &FederationIaCResponse{Filename: filename, Content: content, ContentType: contentType}, nil
}

// shellEscapeData returns a copy of data with all fields interpolated into CLI
// shell templates escaped for safe use inside double-quoted bash strings.
func shellEscapeData(data federationIaCData) federationIaCData {
	d := data
	d.AccountName = shellEscape(data.AccountName)
	d.AccountExternalID = shellEscape(data.AccountExternalID)
	d.AccountSlug = shellEscape(data.AccountSlug)
	d.Source = shellEscape(data.Source)
	d.OIDCIssuerURL = shellEscape(data.OIDCIssuerURL)
	d.OIDCIssuerHost = shellEscape(data.OIDCIssuerHost)
	d.OIDCAudience = shellEscape(data.OIDCAudience)
	d.SubscriptionID = shellEscape(data.SubscriptionID)
	d.TenantID = shellEscape(data.TenantID)
	d.ProjectID = shellEscape(data.ProjectID)
	d.ServiceAccountEmail = shellEscape(data.ServiceAccountEmail)
	d.OIDCIssuerURI = shellEscape(data.OIDCIssuerURI)
	d.CUDlyAPIURL = shellEscape(data.CUDlyAPIURL)
	d.SourceAccountID = shellEscape(data.SourceAccountID)
	d.ContactEmail = shellEscape(data.ContactEmail)
	return d
}

// formatNeedsZip returns true for IaC formats that ship as multi-file zip archives.
func formatNeedsZip(format string) bool {
	switch format {
	case "bundle", "cfn", "bicep", "arm":
		return true
	}
	return false
}

// buildZipResponse is the single encoder for zip-format IaC downloads. It dispatches
// to the appropriate builder, which returns raw bytes + filename, then base64-wraps
// the result into a FederationIaCResponse.
func (h *Handler) buildZipResponse(data federationIaCData, target, source, format, slug string) (*FederationIaCResponse, error) {
	var (
		zipBytes []byte
		filename string
		err      error
	)
	switch format {
	case "bundle":
		zipBytes, filename, err = buildFederationBundle(data, target, source, slug)
	case "cfn":
		zipBytes, filename, err = buildCFNZip(data, target, source, slug)
	case "bicep", "arm":
		zipBytes, filename, err = buildAzureTemplateZip(format, data, target, slug)
	default:
		return nil, NewClientError(400, "unsupported zip format: "+format)
	}
	if err != nil {
		return nil, err
	}
	return &FederationIaCResponse{
		Filename:        filename,
		Content:         base64.StdEncoding.EncodeToString(zipBytes),
		ContentType:     "application/zip",
		ContentEncoding: "base64",
	}, nil
}

// buildGenericIaCData builds template data with source-specific OIDC values.
// Account-specific fields are left empty — the user provides them at apply time.
func buildGenericIaCData(target, source, dashboardURL string) federationIaCData {
	data := federationIaCData{
		AccountSlug: "target",
		Source:      source,
		CUDlyAPIURL: dashboardURL,
	}
	switch target {
	case "aws":
		data.OIDCIssuerURL = awsOIDCIssuer(source, "")
		data.OIDCIssuerHost = strings.TrimPrefix(data.OIDCIssuerURL, "https://")
		data.OIDCAudience = awsOIDCAudience(source)
	case "gcp":
		data.OIDCIssuerURI = gcpOIDCIssuerURI(source, "")
	}
	return data
}

// validFederationSources is the allowlist of valid source cloud providers.
var validFederationSources = map[string]bool{"aws": true, "azure": true, "gcp": true}

// validFederationFormats is the allowlist of supported IaC format codes. The
// legacy empty/"tfvars" single-file format was removed — the full bundle
// supersedes it.
var validFederationFormats = map[string]bool{
	"cli":    true,
	"bundle": true,
	"cfn":    true,
	"bicep":  true,
	"arm":    true,
}

// federationIaCParams validates and extracts query parameters from the request.
func federationIaCParams(q map[string]string) (target, source, format string, err error) {
	target, source, format = q["target"], q["source"], q["format"]
	if target == "" || source == "" || format == "" {
		return "", "", "", NewClientError(400, "target, source, and format query parameters are required")
	}
	if !validFederationSources[source] {
		return "", "", "", NewClientError(400, "source must be aws, azure, or gcp")
	}
	if !validFederationTargets[target] {
		return "", "", "", NewClientError(400, "target must be aws, azure, or gcp")
	}
	if !validFederationFormats[format] {
		return "", "", "", NewClientError(400, "format must be one of: cli, bundle, cfn, bicep, arm")
	}
	return target, source, format, nil
}

// shellEscape escapes a string for safe use inside a double-quoted bash argument.
// It escapes characters that have special meaning in double-quoted strings: \, $, `, "
func shellEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`)
	return r.Replace(s)
}

// validFederationTargets is the allowlist of valid target cloud providers.
var validFederationTargets = map[string]bool{"aws": true, "azure": true, "gcp": true}

// singleFileSpec returns the template path, output filename, and content-type for a
// single-file IaC download. Zip formats are intercepted earlier by formatNeedsZip
// in getFederationIaC and never reach this function. Only "cli" is a valid single-file
// format after the tfvars-only path was removed.
func singleFileSpec(target, source, format, slug string) (tmplPath, filename, contentType string, err error) {
	if format == "cli" {
		return cliScriptSpec(target, source, slug)
	}
	return "", "", "", NewClientError(400, "unsupported format: "+format)
}

// cliScriptSpec returns the CLI shell-script template path + filename for the
// target/source combination. All CLI scripts are rendered with shell-escaped data.
func cliScriptSpec(target, source, slug string) (tmplPath, filename, contentType string, err error) {
	const ct = "text/x-shellscript"
	switch {
	case target == "aws" && source == "aws":
		return "templates/aws-cross-account-cli.sh.tmpl", slug + "-aws-cross-account-cli.sh", ct, nil
	case target == "aws":
		return "templates/aws-wif-cli.sh.tmpl", slug + "-aws-wif-cli.sh", ct, nil
	case target == "azure":
		return "templates/azure-wif-cli.sh.tmpl", slug + "-azure-wif-cli.sh", ct, nil
	case target == "gcp" && source == "gcp":
		return "templates/gcp-sa-impersonation-cli.sh.tmpl", slug + "-gcp-sa-impersonation-cli.sh", ct, nil
	case target == "gcp":
		return "templates/gcp-wif-cli.sh.tmpl", slug + "-gcp-wif-cli.sh", ct, nil
	default:
		return "", "", "", NewClientError(400, fmt.Sprintf("unsupported target/source combination: %s/%s", target, source))
	}
}

// buildFederationBundle creates a zip bundle containing:
//   - The generated .tfvars file
//   - The Terraform module files (main.tf, variables.tf, outputs.tf)
//   - For aws-target: the CloudFormation template + parameters JSON + deploy script
//
// Returns the raw zip bytes and output filename. base64 wrapping happens in
// buildZipResponse.
func buildFederationBundle(data federationIaCData, target, source, slug string) ([]byte, string, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	if err := addBundleTerraform(zw, data, target, source, slug); err != nil {
		return nil, "", err
	}
	if err := addBundleCFN(zw, data, target, source, slug); err != nil {
		return nil, "", err
	}

	readme := buildBundleReadme(data, target, source)
	if err := addStringToZip(zw, "README.txt", readme); err != nil {
		return nil, "", fmt.Errorf("bundle: write readme: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, "", fmt.Errorf("bundle: finalize zip: %w", err)
	}

	return buf.Bytes(), bundleZipName(target, source, slug), nil
}

// buildCFNZip creates a self-contained CloudFormation zip with template.yaml,
// the parameters JSON, and deploy-cfn.sh. Returns raw zip bytes + filename.
func buildCFNZip(data federationIaCData, target, source, slug string) ([]byte, string, error) {
	if target != "aws" {
		return nil, "", NewClientError(400, "format=cfn requires target=aws")
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := writeCFNFiles(zw, data, source, slug); err != nil {
		return nil, "", err
	}
	if err := zw.Close(); err != nil {
		return nil, "", fmt.Errorf("cfn: finalize zip: %w", err)
	}
	zipName := slug + "-aws-wif-cfn.zip"
	if source == "aws" {
		zipName = slug + "-aws-cross-account-cfn.zip"
	}
	return buf.Bytes(), zipName, nil
}

// azureTemplateName maps a format to the static template filename inside
// iac/federation/azure-target/bicep/. Returns "" for unknown formats.
func azureTemplateName(format string) string {
	switch format {
	case "bicep":
		return "azure-wif.bicep"
	case "arm":
		return "azure-wif.arm.json"
	}
	return ""
}

// buildAzureTemplateZip creates a zip containing the Azure Bicep or ARM template,
// a rendered parameters file, the deploy-azure.sh wrapper script, and a README
// describing the two-step flow (run azure-wif-cli.sh first to create the
// identity, then deploy this template to assign the Reservation Purchaser role).
//
// format must be "bicep" or "arm". target must be "azure".
func buildAzureTemplateZip(format string, data federationIaCData, target, slug string) ([]byte, string, error) {
	if target != "azure" {
		return nil, "", NewClientError(400, "format="+format+" requires target=azure")
	}
	templateName := azureTemplateName(format)
	if templateName == "" {
		return nil, "", NewClientError(400, "unsupported azure template format: "+format)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := writeAzureTemplateFiles(zw, data, format, templateName); err != nil {
		return nil, "", err
	}
	if err := zw.Close(); err != nil {
		return nil, "", fmt.Errorf("azure %s: finalize zip: %w", format, err)
	}
	return buf.Bytes(), slug + "-azure-wif-" + format + ".zip", nil
}

// writeAzureTemplateFiles reads the static Azure template + deploy script,
// renders the parameters file and README, and writes all four into the zip.
func writeAzureTemplateFiles(zw *zip.Writer, data federationIaCData, format, templateName string) error {
	templateBytes, err := cudlyiac.Modules.ReadFile("federation/azure-target/bicep/" + templateName)
	if err != nil {
		return fmt.Errorf("azure %s: read template: %w", format, err)
	}
	deployScript, err := renderTemplate("templates/azure-wif-deploy.sh.tmpl", data)
	if err != nil {
		return fmt.Errorf("azure %s: render deploy script: %w", format, err)
	}
	paramsJSON, err := renderTemplate("templates/azure-wif-bicep-params.json.tmpl", data)
	if err != nil {
		return fmt.Errorf("azure %s: render params: %w", format, err)
	}
	entries := []struct {
		name    string
		content []byte
	}{
		{templateName, templateBytes},
		{"azure-wif-bicep-params.json", []byte(paramsJSON)},
		{"deploy-azure.sh", []byte(deployScript)},
		{"README.txt", []byte(buildAzureTemplateReadme(data, format))},
	}
	for _, e := range entries {
		if err = addBytesToZip(zw, e.name, e.content); err != nil {
			return fmt.Errorf("azure %s: write %s: %w", format, e.name, err)
		}
	}
	return nil
}

func buildAzureTemplateReadme(data federationIaCData, format string) string {
	var sb strings.Builder
	sb.WriteString("CUDly Azure Federation — ")
	if format == "bicep" {
		sb.WriteString("Bicep deployment\n")
		sb.WriteString("===================================\n\n")
	} else {
		sb.WriteString("ARM deployment\n")
		sb.WriteString("================================\n\n")
	}
	sb.WriteString(fmt.Sprintf("Account : %s (%s)\n\n", data.AccountName, data.AccountExternalID))
	sb.WriteString("The deploy script creates an Azure AD App Registration with a federated\n")
	sb.WriteString("identity credential bound to CUDly's OIDC issuer, then deploys the role\n")
	sb.WriteString("assignment template. No certificate or secret is created.\n\n")
	sb.WriteString("One-step deployment:\n\n")
	sb.WriteString("  bash deploy-azure.sh [--location <region>] [--template " + format + "]\n\n")
	sb.WriteString("Registration with CUDly runs automatically after deployment.\n")
	sb.WriteString("Set CUDLY_CONTACT_EMAIL to override the pre-filled contact email.\n\n")
	sb.WriteString("After deployment, set azure_auth_mode=workload_identity_federation in CUDly.\n")
	return sb.String()
}

// addBundleTerraform adds the Terraform module files and generated .tfvars to the zip.
func addBundleTerraform(zw *zip.Writer, data federationIaCData, target, source, slug string) error {
	tfDir := bundleModuleDir(target, source) + "/terraform"
	if err := addDirToZip(zw, cudlyiac.Modules, tfDir, "terraform"); err != nil {
		return fmt.Errorf("bundle: terraform dir: %w", err)
	}
	tfvarsTmpl, tfvarsName := bundleTfvarsSpec(target, source, slug)
	tfvarsContent, err := renderTemplate(tfvarsTmpl, data)
	if err != nil {
		return fmt.Errorf("bundle: %w", err)
	}
	if err = addStringToZip(zw, tfvarsName, tfvarsContent); err != nil {
		return fmt.Errorf("bundle: write tfvars: %w", err)
	}
	return nil
}

// addBundleCFN adds CloudFormation files to the zip for AWS target bundles
// (both cross-account and WIF). Thin wrapper around writeCFNFiles.
func addBundleCFN(zw *zip.Writer, data federationIaCData, target, source, slug string) error {
	if target != "aws" {
		return nil
	}
	return writeCFNFiles(zw, data, source, slug)
}

// writeCFNFiles writes the CloudFormation template, parameters JSON, and deploy
// script into the given zip.Writer. Used by both addBundleCFN (for the bundle
// format) and buildCFNZip (for format=cfn).
//
// Dispatches on source: "aws" → cross-account IAM role template; anything else
// → AWS WIF template.
func writeCFNFiles(zw *zip.Writer, data federationIaCData, source, slug string) error {
	cfTemplatePath := "federation/aws-target/cloudformation/template.yaml"
	deployTmplPath := "templates/aws-cfn-deploy.sh.tmpl"
	if source == "aws" {
		cfTemplatePath = "federation/aws-cross-account/cloudformation/template.yaml"
		deployTmplPath = "templates/aws-cross-account-cfn-deploy.sh.tmpl"
	}
	cfTemplate, err := cudlyiac.Modules.ReadFile(cfTemplatePath)
	if err != nil {
		return fmt.Errorf("cfn: read cf template: %w", err)
	}
	if err = addBytesToZip(zw, "cloudformation/template.yaml", cfTemplate); err != nil {
		return fmt.Errorf("cfn: write cf template: %w", err)
	}
	cfParams, err := buildCFParamsJSON(data, source)
	if err != nil {
		return fmt.Errorf("cfn: %w", err)
	}
	if err = addStringToZip(zw, "cloudformation/"+slug+"-cf-params.json", cfParams); err != nil {
		return fmt.Errorf("cfn: write cf params: %w", err)
	}
	// Shell-escape template values before rendering the deploy script to prevent
	// injection via account names or OIDC URLs containing shell metacharacters.
	escapedData := shellEscapeData(data)
	deployScript, err := renderTemplate(deployTmplPath, escapedData)
	if err != nil {
		return fmt.Errorf("cfn: %w", err)
	}
	if err = addStringToZip(zw, "cloudformation/deploy-cfn.sh", deployScript); err != nil {
		return fmt.Errorf("cfn: write deploy script: %w", err)
	}
	return nil
}

// bundleModuleDir returns the embedded FS path for the Terraform module.
func bundleModuleDir(target, source string) string {
	switch {
	case target == "aws" && source == "aws":
		return "federation/aws-cross-account"
	case target == "aws":
		return "federation/aws-target"
	case target == "azure":
		return "federation/azure-target"
	case target == "gcp" && source == "gcp":
		return "federation/gcp-sa-impersonation"
	default:
		return "federation/gcp-target"
	}
}

// bundleTfvarsSpec returns the template path and zip destination name for the .tfvars file.
func bundleTfvarsSpec(target, source, slug string) (tmplPath, name string) {
	switch {
	case target == "aws" && source == "aws":
		return "templates/aws-cross-account.tfvars.tmpl", "terraform/" + slug + "-aws-cross-account.tfvars"
	case target == "aws":
		return "templates/aws-wif.tfvars.tmpl", "terraform/" + slug + "-aws-wif.tfvars"
	case target == "azure":
		return "templates/azure-wif.tfvars.tmpl", "terraform/" + slug + "-azure-wif.tfvars"
	case target == "gcp" && source == "gcp":
		return "templates/gcp-sa-impersonation.tfvars.tmpl", "terraform/" + slug + "-gcp-sa-impersonation.tfvars"
	default:
		return "templates/gcp-wif.tfvars.tmpl", "terraform/" + slug + "-gcp-wif.tfvars"
	}
}

// bundleZipName returns the output filename for the zip bundle.
func bundleZipName(target, source, slug string) string {
	switch {
	case target == "aws" && source == "aws":
		return slug + "-aws-cross-account-bundle.zip"
	case target == "gcp" && source == "gcp":
		return slug + "-gcp-sa-impersonation-bundle.zip"
	default:
		return slug + "-" + target + "-federation-bundle.zip"
	}
}

func buildBundleReadme(data federationIaCData, target, source string) string {
	var sb strings.Builder
	sb.WriteString("CUDly Federation IaC Bundle\n")
	sb.WriteString("===========================\n\n")
	sb.WriteString(fmt.Sprintf("Account : %s (%s)\n", data.AccountName, data.AccountExternalID))
	sb.WriteString(fmt.Sprintf("Target  : %s\n", target))
	sb.WriteString(fmt.Sprintf("Source  : %s\n\n", source))

	switch {
	case target == "aws" && source == "aws":
		sb.WriteString("Contents:\n  terraform/           - Cross-account IAM role Terraform module\n")
		sb.WriteString("  terraform/*.tfvars   - Pre-filled variable values for this account\n")
		sb.WriteString("  cloudformation/      - CloudFormation alternative\n\n")
		sb.WriteString("Deploy (Terraform):\n")
		sb.WriteString(fmt.Sprintf("  cd terraform && terraform init && terraform apply -var-file=%s-aws-cross-account.tfvars\n\n", data.AccountSlug))
		sb.WriteString("Deploy (CloudFormation):\n")
		sb.WriteString("  cd cloudformation && bash deploy-cfn.sh --region <region>\n\n")
		sb.WriteString("After apply, set aws_auth_mode=role_arn and aws_role_arn in CUDly.\n")
	case target == "aws":
		sb.WriteString("Contents:\n  terraform/           - IAM OIDC provider + role Terraform module\n")
		sb.WriteString("  terraform/*.tfvars   - Pre-filled variable values for this account\n")
		sb.WriteString("  cloudformation/      - CloudFormation alternative\n\n")
		sb.WriteString("Deploy (Terraform):\n")
		sb.WriteString(fmt.Sprintf("  cd terraform && terraform init && terraform apply -var-file=%s-aws-wif.tfvars\n\n", data.AccountSlug))
		sb.WriteString("Deploy (CloudFormation):\n")
		sb.WriteString("  cd cloudformation && bash deploy-cfn.sh --region <region>\n\n")
		sb.WriteString("After apply, set aws_auth_mode=workload_identity_federation and aws_role_arn in CUDly.\n")
	case target == "azure":
		sb.WriteString("Contents:\n  terraform/           - Azure App Registration + cert WIF Terraform module\n")
		sb.WriteString("  terraform/*.tfvars   - Pre-filled variable values for this account\n\n")
		sb.WriteString("Prerequisites:\n  1. Generate an RSA key and self-signed certificate (see tfvars comments).\n")
		sb.WriteString("  2. Paste the certificate PEM into the tfvars file.\n")
		sb.WriteString("  3. Store the private key PEM in CUDly as azure_wif_private_key.\n\n")
		sb.WriteString("Deploy (Terraform):\n")
		sb.WriteString(fmt.Sprintf("  cd terraform && terraform init && terraform apply -var-file=%s-azure-wif.tfvars\n\n", data.AccountSlug))
		sb.WriteString("After apply, set azure_auth_mode=workload_identity_federation in CUDly.\n")
	case target == "gcp" && source == "gcp":
		sb.WriteString("Contents:\n  terraform/           - Service account impersonation Terraform module\n")
		sb.WriteString("  terraform/*.tfvars   - Pre-filled variable values for this account\n\n")
		sb.WriteString("Deploy (Terraform):\n")
		sb.WriteString(fmt.Sprintf("  cd terraform && terraform init && terraform apply -var-file=%s-gcp-sa-impersonation.tfvars\n\n", data.AccountSlug))
		sb.WriteString("After apply, set gcp_auth_mode=application_default in CUDly.\n")
	case target == "gcp":
		sb.WriteString("Contents:\n  terraform/           - Workload Identity Pool + provider Terraform module\n")
		sb.WriteString("  terraform/*.tfvars   - Pre-filled variable values for this account\n\n")
		sb.WriteString("Deploy (Terraform):\n")
		sb.WriteString(fmt.Sprintf("  cd terraform && terraform init && terraform apply -var-file=%s-gcp-wif.tfvars\n\n", data.AccountSlug))
		sb.WriteString("After apply, run the gcloud_command output to generate the WIF credential JSON,\nthen paste it into CUDly as gcp_workload_identity_config.\n")
	}

	return sb.String()
}

// ---------------------------------------------------------------------------
// CF params JSON helper — uses encoding/json for correct escaping
// ---------------------------------------------------------------------------

type cfParam struct {
	ParameterKey   string `json:"ParameterKey"`
	ParameterValue string `json:"ParameterValue"`
}

// buildCFParamsJSON produces the CloudFormation parameter overrides JSON using
// encoding/json, which correctly escapes special characters in values.
//
// Dispatches on source: "aws" → cross-account params (SourceAccountID, ExternalID);
// anything else → AWS WIF params (OIDC values).
func buildCFParamsJSON(data federationIaCData, source string) (string, error) {
	var params []cfParam
	if source == "aws" {
		params = []cfParam{
			{ParameterKey: "SourceAccountID", ParameterValue: data.SourceAccountID},
			{ParameterKey: "ExternalID", ParameterValue: uuid.New().String()},
			{ParameterKey: "RoleName", ParameterValue: "CUDly-" + data.AccountSlug},
			{ParameterKey: "CUDlyAPIURL", ParameterValue: data.CUDlyAPIURL},
			{ParameterKey: "ContactEmail", ParameterValue: ""},
		}
	} else {
		params = []cfParam{
			{ParameterKey: "OIDCIssuerURL", ParameterValue: data.OIDCIssuerURL},
			{ParameterKey: "OIDCIssuerHost", ParameterValue: strings.TrimPrefix(data.OIDCIssuerURL, "https://")},
			{ParameterKey: "OIDCAudience", ParameterValue: data.OIDCAudience},
			{ParameterKey: "RoleName", ParameterValue: "CUDly-" + data.AccountSlug},
		}
	}
	out, err := json.MarshalIndent(params, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal cf params: %w", err)
	}
	return string(out) + "\n", nil
}

// ---------------------------------------------------------------------------
// Zip helpers
// ---------------------------------------------------------------------------

func addDirToZip(zw *zip.Writer, fsys fs.ReadFileFS, srcDir, destPrefix string) error {
	return fs.WalkDir(fsys, srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fsys.ReadFile(path)
		if err != nil {
			return err
		}
		// Compute the relative path from srcDir to this file.
		rel := strings.TrimPrefix(path, srcDir+"/")
		return addBytesToZip(zw, destPrefix+"/"+rel, b)
	})
}

func addBytesToZip(zw *zip.Writer, name string, content []byte) error {
	f, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = f.Write(content)
	return err
}

func addStringToZip(zw *zip.Writer, name, content string) error {
	return addBytesToZip(zw, name, []byte(content))
}
