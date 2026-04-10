package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"text/template"

	cudlyiac "github.com/LeanerCloud/CUDly/iac"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/iacfiles"
	"github.com/aws/aws-lambda-go/events"
)

// federationIaCData holds the template variables used when rendering IaC templates.
type federationIaCData struct {
	AccountName       string
	AccountExternalID string
	AccountSlug       string
	Source            string
	// AWS WIF / cross-account
	OIDCIssuerURL string
	OIDCAudience  string
	// Azure-specific
	SubscriptionID string
	TenantID       string
	// GCP-specific
	ProjectID           string
	ServiceAccountEmail string
	OIDCIssuerURI       string
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
// Query params: target, source, account_id, format
//
//   - format=""         → single file (tfvars, JSON, or sh)
//   - format="cf-params" → CloudFormation parameters JSON (aws target only)
//   - format="bundle"   → zip with tfvars + Terraform module + CF template/script
func (h *Handler) getFederationIaC(ctx context.Context, req *events.LambdaFunctionURLRequest) (*FederationIaCResponse, error) {
	q := req.QueryStringParameters
	target, source, accountID, format, err := federationIaCParams(q)
	if err != nil {
		return nil, err
	}

	account, err := h.config.GetCloudAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("federation iac: get account %s: %w", accountID, err)
	}
	if account == nil {
		return nil, NewClientError(404, "account not found")
	}

	slug := slugify(account.Name)
	if slug == "" {
		slug = slugify(account.ID)
	}

	data := federationIaCData{
		AccountName:       account.Name,
		AccountExternalID: account.ExternalID,
		AccountSlug:       slug,
		Source:            source,
	}

	if err = populateIaCData(&data, target, source, account); err != nil {
		return nil, err
	}

	if format == "bundle" {
		return h.buildFederationBundle(data, target, source, slug)
	}

	tmplPath, filename, contentType, err := singleFileSpec(target, source, format, slug)
	if err != nil {
		return nil, err
	}
	content, err := renderTemplate(tmplPath, data)
	if err != nil {
		return nil, fmt.Errorf("federation iac: %w", err)
	}
	return &FederationIaCResponse{Filename: filename, Content: content, ContentType: contentType}, nil
}

// federationIaCParams validates and extracts query parameters from the request.
func federationIaCParams(q map[string]string) (target, source, accountID, format string, err error) {
	target, source, accountID, format = q["target"], q["source"], q["account_id"], q["format"]
	if target == "" || source == "" {
		return "", "", "", "", NewClientError(400, "target and source query parameters are required")
	}
	if accountID == "" {
		return "", "", "", "", NewClientError(400, "account_id query parameter is required")
	}
	return target, source, accountID, format, nil
}

// populateIaCData fills target-specific fields on data from the account record.
func populateIaCData(data *federationIaCData, target, source string, account *config.CloudAccount) error {
	switch target {
	case "aws":
		data.OIDCIssuerURL = awsOIDCIssuer(source, account.AzureTenantID)
		data.OIDCAudience = awsOIDCAudience(source)
	case "azure":
		data.SubscriptionID = account.AzureSubscriptionID
		if data.SubscriptionID == "" {
			data.SubscriptionID = account.ExternalID
		}
		data.TenantID = account.AzureTenantID
	case "gcp":
		data.ProjectID = account.GCPProjectID
		if data.ProjectID == "" {
			data.ProjectID = account.ExternalID
		}
		data.ServiceAccountEmail = account.GCPClientEmail
		if data.ServiceAccountEmail == "" {
			data.ServiceAccountEmail = "cudly@" + data.ProjectID + ".iam.gserviceaccount.com"
		}
		data.OIDCIssuerURI = gcpOIDCIssuerURI(source, account.AzureTenantID)
	default:
		return NewClientError(400, "target must be aws, azure, or gcp")
	}
	return nil
}

// singleFileSpec returns the template path, output filename, and content-type for a
// single-file IaC download (i.e. format is "" or "cf-params").
func singleFileSpec(target, source, format, slug string) (tmplPath, filename, contentType string, err error) {
	switch {
	case target == "aws" && source == "aws":
		return "templates/aws-cross-account.tfvars.tmpl", slug + "-aws-cross-account.tfvars", "text/plain", nil
	case target == "aws" && format == "cf-params":
		return "templates/aws-wif-cf-params.json.tmpl", slug + "-aws-wif-cf-params.json", "application/json", nil
	case target == "aws":
		return "templates/aws-wif.tfvars.tmpl", slug + "-aws-wif.tfvars", "text/plain", nil
	case target == "azure":
		return "templates/azure-wif.tfvars.tmpl", slug + "-azure-wif.tfvars", "text/plain", nil
	case target == "gcp" && source == "gcp":
		return "templates/gcp-sa-impersonation.tfvars.tmpl", slug + "-gcp-sa-impersonation.tfvars", "text/plain", nil
	case target == "gcp":
		return "templates/gcp-wif.tfvars.tmpl", slug + "-gcp-wif.tfvars", "text/plain", nil
	default:
		return "", "", "", NewClientError(400, fmt.Sprintf("unsupported target/source combination: %s/%s", target, source))
	}
}

// buildFederationBundle creates a zip bundle containing:
//   - The generated .tfvars file
//   - The Terraform module files (main.tf, variables.tf, outputs.tf)
//   - For aws-target: the CloudFormation template + parameters JSON + deploy script
func (h *Handler) buildFederationBundle(data federationIaCData, target, source, slug string) (*FederationIaCResponse, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	if err := addBundleTerraform(zw, data, target, source, slug); err != nil {
		return nil, err
	}
	if err := addBundleCFN(zw, data, target, source, slug); err != nil {
		return nil, err
	}

	readme := buildBundleReadme(data, target, source)
	if err := addStringToZip(zw, "README.txt", readme); err != nil {
		return nil, fmt.Errorf("bundle: write readme: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("bundle: finalize zip: %w", err)
	}

	return &FederationIaCResponse{
		Filename:        bundleZipName(target, source, slug),
		Content:         base64.StdEncoding.EncodeToString(buf.Bytes()),
		ContentType:     "application/zip",
		ContentEncoding: "base64",
	}, nil
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

// addBundleCFN adds CloudFormation files to the zip for AWS WIF (non-same-cloud) bundles.
func addBundleCFN(zw *zip.Writer, data federationIaCData, target, source, slug string) error {
	if target != "aws" || source == "aws" {
		return nil
	}
	cfTemplate, err := cudlyiac.Modules.ReadFile("federation/aws-target/cloudformation/template.yaml")
	if err != nil {
		return fmt.Errorf("bundle: read cf template: %w", err)
	}
	if err = addBytesToZip(zw, "cloudformation/template.yaml", cfTemplate); err != nil {
		return fmt.Errorf("bundle: write cf template: %w", err)
	}
	cfParams, err := renderTemplate("templates/aws-wif-cf-params.json.tmpl", data)
	if err != nil {
		return fmt.Errorf("bundle: %w", err)
	}
	if err = addStringToZip(zw, "cloudformation/"+slug+"-cf-params.json", cfParams); err != nil {
		return fmt.Errorf("bundle: write cf params: %w", err)
	}
	deployScript, err := renderTemplate("templates/aws-cfn-deploy.sh.tmpl", data)
	if err != nil {
		return fmt.Errorf("bundle: %w", err)
	}
	if err = addStringToZip(zw, "cloudformation/deploy-cfn.sh", deployScript); err != nil {
		return fmt.Errorf("bundle: write deploy script: %w", err)
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
		sb.WriteString("  terraform/*.tfvars   - Pre-filled variable values for this account\n\n")
		sb.WriteString("Deploy (Terraform):\n")
		sb.WriteString(fmt.Sprintf("  cd terraform && terraform init && terraform apply -var-file=%s-aws-cross-account.tfvars\n\n", data.AccountSlug))
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
// Zip helpers
// ---------------------------------------------------------------------------

func addDirToZip(zw *zip.Writer, fsys fs.ReadFileFS, srcDir, destPrefix string) error {
	entries, err := fs.ReadDir(fsys, srcDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		b, err := fsys.ReadFile(srcDir + "/" + entry.Name())
		if err != nil {
			return err
		}
		if err = addBytesToZip(zw, destPrefix+"/"+entry.Name(), b); err != nil {
			return err
		}
	}
	return nil
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
