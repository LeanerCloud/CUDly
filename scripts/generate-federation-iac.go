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
// # Quick examples
//
//	# AWS target, Azure source — Terraform tfvars
//	go run scripts/generate-federation-iac.go \
//	  --target aws --source azure \
//	  --account-name "prod-aws" --account-id "123456789012" \
//	  --tenant-id "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
//
//	# AWS target, Azure source — CloudFormation parameters JSON
//	go run scripts/generate-federation-iac.go \
//	  --target aws --source azure --format cf-params \
//	  --account-name "prod-aws" --account-id "123456789012" \
//	  --tenant-id "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
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
//	  --tenant-id "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
//
//	# Print tfvars to stdout
//	go run scripts/generate-federation-iac.go \
//	  --target aws --source gcp \
//	  --account-name "prod" --account-id "123456789012" --output -

package main

import (
	"archive/zip"
	"bytes"
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

// populateData fills target-specific fields on data from CLI flags.
func populateData(data *iacData, target, source, tenantID, projectID, saEmail string) bool {
	switch target {
	case "aws":
		data.OIDCIssuerURL = awsOIDCIssuer(source, tenantID)
		data.OIDCAudience = awsOIDCAudience(source)
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
		return false
	}
	return true
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
	if !populateData(&data, *target, *source, *tenantID, *projectID, *saEmail) {
		fmt.Fprintf(os.Stderr, "Error: --target must be aws, azure, or gcp (got %q)\n", *target)
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
