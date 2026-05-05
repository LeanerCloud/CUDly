package email

import (
	"context"
	"fmt"
)

// Email templates

const newRecommendationsTemplate = `CUDly - New Commitment Recommendations Available
================================================

We've identified potential savings across your cloud accounts.

Summary:
--------
Estimated Monthly Savings: ${{printf "%.2f" .TotalSavings}}
{{if gt .TotalUpfrontCost 0.0}}Total Upfront Cost: ${{printf "%.2f" .TotalUpfrontCost}}{{end}}

Top Recommendations:
{{range .Recommendations}}
- {{.Count}}x {{.ResourceType}}{{if .Engine}} ({{.Engine}}){{end}} in {{.Region}}
  Service: {{.Service}} | Est. Savings: ${{printf "%.2f" .MonthlySavings}}/month
{{end}}

Review and configure your purchases:
{{.DashboardURL}}

This is an automated message from CUDly.
`

const scheduledPurchaseTemplate = `CUDly - Scheduled Purchase in {{.DaysUntilPurchase}} Days
=========================================

Based on your Purchase Plan "{{.PlanName}}", the following commitments will be purchased on {{.PurchaseDate}}:

{{range .Recommendations}}
- {{.Count}}x {{.ResourceType}}{{if .Engine}} ({{.Engine}}){{end}} in {{.Region}}
  Service: {{.Service}} | Est. Savings: ${{printf "%.2f" .MonthlySavings}}/month
{{end}}

Summary:
--------
Estimated Monthly Savings: ${{printf "%.2f" .TotalSavings}}
{{if gt .TotalUpfrontCost 0.0}}Total Upfront Cost: ${{printf "%.2f" .TotalUpfrontCost}}{{end}}

Actions:
--------
[Review & Edit] {{.DashboardURL}}?action=edit&token={{urlquery .ApprovalToken}}

[Pause Plan] {{.DashboardURL}}?action=pause&token={{urlquery .ApprovalToken}}

[Cancel This Purchase] {{.DashboardURL}}?action=cancel&token={{urlquery .ApprovalToken}}

You have {{.DaysUntilPurchase}} days to modify or cancel before automatic execution.

This is an automated message from CUDly.
`

const purchaseConfirmationTemplate = `CUDly - Purchases Completed Successfully
========================================

Your commitment purchases have been completed:

{{range .Recommendations}}
- {{.Count}}x {{.ResourceType}}{{if .Engine}} ({{.Engine}}){{end}} in {{.Region}}
  Service: {{.Service}} | Est. Savings: ${{printf "%.2f" .MonthlySavings}}/month
{{end}}

Summary:
--------
Total Monthly Savings: ${{printf "%.2f" .TotalSavings}}
{{if gt .TotalUpfrontCost 0.0}}Total Upfront Cost: ${{printf "%.2f" .TotalUpfrontCost}}{{end}}

View purchase history in the dashboard:
{{.DashboardURL}}/history

This is an automated message from CUDly.
`

const purchaseFailedTemplate = `CUDly - Purchase Failed
=======================

Some purchases could not be completed. Please review and retry manually.

Failed Purchases:
{{range .Recommendations}}
- {{.Count}}x {{.ResourceType}}{{if .Engine}} ({{.Engine}}){{end}} in {{.Region}}
  Service: {{.Service}}
{{end}}

Review failed purchases:
{{.DashboardURL}}/history

This is an automated message from CUDly.
`

const passwordResetTemplate = `CUDly - Password Reset Request
==============================

Hello {{.Email}},

We received a request to reset your password for CUDly.

Click the link below to set a new password:
{{.ResetURL}}

This link will expire in 1 hour.

If you didn't request a password reset, you can safely ignore this email.
Your password will remain unchanged.

This is an automated message from CUDly.
`

const welcomeUserTemplate = `Welcome to CUDly
================

Hello {{.Email}},

Your CUDly account has been created.

You can log in at:
{{.DashboardURL}}

Your role: {{.Role}}

If you have any questions, please contact your administrator.

This is an automated message from CUDly.
`

const riExchangePendingApprovalTemplate = `CUDly - RI Exchange Approval Required
======================================

CUDly has identified convertible RI exchanges that need your approval.

Proposed Exchanges:
{{range .Exchanges}}
- Source: {{.SourceRIID}} ({{.SourceInstanceType}}, {{printf "%.1f" .UtilizationPct}}% utilized)
  Target: {{.TargetInstanceType}} x{{.TargetCount}}
  Payment Due: ${{.PaymentDue}}
  [Approve] {{$.DashboardURL}}/api/ri-exchange/approve/{{.RecordID}}?token={{urlquery .ApprovalToken}}
  [Reject]  {{$.DashboardURL}}/api/ri-exchange/reject/{{.RecordID}}?token={{urlquery .ApprovalToken}}
{{end}}
Total Payment: ${{.TotalPayment}}
{{if .Skipped}}
Skipped (could not process):
{{range .Skipped}}
- {{.SourceRIID}} ({{.SourceInstanceType}}): {{.Reason}}
{{end}}{{end}}
Please approve within 6 hours (before the next analysis run).

Review exchange history:
{{.DashboardURL}}/#ri-exchange

This is an automated message from CUDly.
`

const riExchangeCompletedTemplate = `CUDly - RI Exchanges Completed
==============================

The following RI exchanges have been {{if eq .Mode "auto"}}automatically {{end}}completed:

{{range .Exchanges}}{{if eq .Error ""}}
- Source: {{.SourceRIID}} ({{.SourceInstanceType}})
  Target: {{.TargetInstanceType}} x{{.TargetCount}}
  Payment: ${{.PaymentDue}}
  Exchange ID: {{.ExchangeID}}
{{end}}{{end}}
Total Payment: ${{.TotalPayment}}
{{if .Skipped}}
Skipped (could not process):
{{range .Skipped}}
- {{.SourceRIID}} ({{.SourceInstanceType}}): {{.Reason}}
{{end}}{{end}}
View exchange history:
{{.DashboardURL}}/#ri-exchange

This is an automated message from CUDly.
`

// SendNewRecommendationsNotification sends an email about new recommendations
func (s *Sender) SendNewRecommendationsNotification(ctx context.Context, data NotificationData) error {
	body, err := RenderNewRecommendationsEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render new recommendations email: %w", err)
	}

	subject := fmt.Sprintf("CUDly - New Recommendations: $%.0f/month potential savings", data.TotalSavings)
	return s.SendNotification(ctx, subject, body)
}

// SendScheduledPurchaseNotification sends a notification about upcoming automated purchase
func (s *Sender) SendScheduledPurchaseNotification(ctx context.Context, data NotificationData) error {
	body, err := RenderScheduledPurchaseEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render scheduled purchase email: %w", err)
	}

	subject := fmt.Sprintf("CUDly - Scheduled Purchase in %d Days: %s", data.DaysUntilPurchase, data.PlanName)
	return s.SendNotification(ctx, subject, body)
}

// SendPurchaseConfirmation sends a confirmation after successful purchases
func (s *Sender) SendPurchaseConfirmation(ctx context.Context, data NotificationData) error {
	body, err := RenderPurchaseConfirmationEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render purchase confirmation email: %w", err)
	}

	subject := fmt.Sprintf("CUDly - Purchases Completed: $%.0f/month in savings", data.TotalSavings)
	return s.SendNotification(ctx, subject, body)
}

// SendPurchaseFailedNotification sends a notification when purchases fail
func (s *Sender) SendPurchaseFailedNotification(ctx context.Context, data NotificationData) error {
	body, err := RenderPurchaseFailedEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render purchase failed email: %w", err)
	}

	subject := "CUDly - Purchase Failed - Action Required"
	return s.SendNotification(ctx, subject, body)
}

// PasswordResetData holds data for password reset emails
type PasswordResetData struct {
	Email    string
	ResetURL string
}

// SendPasswordResetEmail sends a password reset email
func (s *Sender) SendPasswordResetEmail(ctx context.Context, email, resetURL string) error {
	body, err := RenderPasswordResetEmail(email, resetURL)
	if err != nil {
		return fmt.Errorf("failed to render password reset email: %w", err)
	}

	return s.SendToEmail(ctx, email, "CUDly - Password Reset Request", body)
}

// WelcomeUserData holds data for welcome emails
type WelcomeUserData struct {
	Email        string
	DashboardURL string
	Role         string
}

// SendWelcomeEmail sends a welcome email to a new user
func (s *Sender) SendWelcomeEmail(ctx context.Context, email, dashboardURL, role string) error {
	body, err := RenderWelcomeEmail(email, dashboardURL, role)
	if err != nil {
		return fmt.Errorf("failed to render welcome email: %w", err)
	}

	return s.SendToEmail(ctx, email, "Welcome to CUDly", body)
}

// SendRIExchangePendingApproval sends an email with RI exchange approval links
func (s *Sender) SendRIExchangePendingApproval(ctx context.Context, data RIExchangeNotificationData) error {
	body, err := RenderRIExchangePendingApprovalEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render ri exchange pending approval email: %w", err)
	}

	subject := fmt.Sprintf("CUDly - RI Exchange Approval Required (%d exchanges)", len(data.Exchanges))
	return s.SendNotification(ctx, subject, body)
}

// SendRIExchangeCompleted sends a notification about completed RI exchanges
func (s *Sender) SendRIExchangeCompleted(ctx context.Context, data RIExchangeNotificationData) error {
	body, err := RenderRIExchangeCompletedEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render ri exchange completed email: %w", err)
	}

	subject := fmt.Sprintf("CUDly - RI Exchanges Completed (%d exchanges)", len(data.Exchanges))
	return s.SendNotification(ctx, subject, body)
}

const purchaseApprovalRequestTemplate = `CUDly - Purchase Approval Required
====================================

A direct purchase of {{len .Recommendations}} commitment(s) has been submitted and requires approval.
{{if .AuthorizedApprovers}}
Authorised approver(s):
{{range .AuthorizedApprovers}}  - {{.}}
{{end}}
Only the inbox(es) listed above can approve or cancel this purchase.
Other recipients are CC'd for visibility only — clicking the links
below from any other account will fail the authorisation check.
{{end}}
Summary:
--------
Total Upfront Cost: ${{printf "%.2f" .TotalUpfrontCost}}
Estimated Monthly Savings: ${{printf "%.2f" .TotalSavings}}
{{if .RequestedByEmail}}
Requested by: {{if .RequestedByName}}{{.RequestedByName}} <{{.RequestedByEmail}}>{{else}}{{.RequestedByEmail}}{{end}}{{if .RequestedAt}} at {{.RequestedAt}}{{end}}
{{end}}
Commitments:
{{range .Recommendations}}
- {{.Count}}x {{.ResourceType}}{{if .Engine}} ({{.Engine}}){{end}} in {{.Region}}
  Service: {{.Service}}{{if .AccountLabel}} | Account: {{.AccountLabel}}{{end}}{{if .Term}} | Term: {{.Term}}yr{{end}}{{if .Payment}} | Payment: {{.Payment}}{{end}}
  Upfront: ${{printf "%.2f" .UpfrontCost}} | Est. Savings: ${{printf "%.2f" .MonthlySavings}}/month
{{end}}
Approve: {{.DashboardURL}}/purchases/approve/{{.ExecutionID}}?token={{urlquery .ApprovalToken}}

Cancel:  {{.DashboardURL}}/purchases/cancel/{{.ExecutionID}}?token={{urlquery .ApprovalToken}}
{{if .CancellationWindowNote}}
{{.CancellationWindowNote}}
{{else}}
Note: cloud commitments are charged to your account once you approve.
Cancellation windows after approval are limited and provider-dependent —
see your cloud provider's billing console (e.g. AWS Account & Billing →
Refund) for the current policy on the resource type you're approving.
{{end}}
Clicking a link will require you to sign in if you aren't already; the
action is then recorded against your logged-in account.

This is an automated message from CUDly. Do not share these links.
`

// purchaseApprovalRequestHTMLTemplate renders the same approval request
// as the plain-text template above with inline-styled CTAs and a richer
// summary table. CSS classes are NOT honoured by most email clients
// (Outlook, mobile Gmail) — every visual rule lives in inline `style=""`
// attributes on the elements themselves. Issue #287.
const purchaseApprovalRequestHTMLTemplate = `<!DOCTYPE html>
<html><head><meta charset="UTF-8"><title>CUDly - Purchase Approval Required</title></head>
<body style="margin:0;padding:0;background:#f4f6f8;font-family:Arial,Helvetica,sans-serif;color:#1a202c;">
<table role="presentation" cellpadding="0" cellspacing="0" width="100%" style="background:#f4f6f8;padding:24px 0;">
<tr><td align="center">
<table role="presentation" cellpadding="0" cellspacing="0" width="600" style="background:#ffffff;border-radius:8px;box-shadow:0 1px 3px rgba(0,0,0,0.06);">
<tr><td style="padding:32px 32px 16px 32px;">
<h1 style="margin:0;font-size:22px;color:#0f172a;">Purchase Approval Required</h1>
<p style="margin:8px 0 0 0;color:#475569;font-size:14px;">A direct purchase of <strong>{{len .Recommendations}}</strong> commitment(s) has been submitted and requires approval.</p>
</td></tr>

{{if .AuthorizedApprovers}}
<tr><td style="padding:0 32px 16px 32px;">
<div style="background:#fef9c3;border-left:4px solid #facc15;padding:12px 16px;font-size:13px;color:#713f12;border-radius:4px;">
<strong>Authorised approver(s):</strong>
<ul style="margin:6px 0 0 18px;padding:0;">
{{range .AuthorizedApprovers}}<li>{{.}}</li>
{{end}}</ul>
<p style="margin:6px 0 0 0;">Only the inbox(es) above can approve or cancel this purchase. Other recipients are CC'd for visibility — clicking from any other account will fail the authorisation check.</p>
</div>
</td></tr>
{{end}}

<tr><td style="padding:0 32px 8px 32px;">
<table role="presentation" cellpadding="0" cellspacing="0" width="100%" style="border-collapse:collapse;font-size:14px;">
<tr><td style="padding:6px 0;color:#64748b;width:180px;">Total Upfront Cost</td><td style="padding:6px 0;color:#0f172a;font-weight:600;">${{printf "%.2f" .TotalUpfrontCost}}</td></tr>
<tr><td style="padding:6px 0;color:#64748b;">Estimated Monthly Savings</td><td style="padding:6px 0;color:#16a34a;font-weight:700;font-size:18px;">${{printf "%.2f" .TotalSavings}}</td></tr>
{{if .RequestedByEmail}}<tr><td style="padding:6px 0;color:#64748b;">Requested by</td><td style="padding:6px 0;color:#0f172a;">{{if .RequestedByName}}{{.RequestedByName}} &lt;{{.RequestedByEmail}}&gt;{{else}}{{.RequestedByEmail}}{{end}}{{if .RequestedAt}} <span style="color:#94a3b8;">at {{.RequestedAt}}</span>{{end}}</td></tr>
{{end}}
</table>
</td></tr>

<tr><td style="padding:0 32px 16px 32px;">
<h2 style="margin:16px 0 8px 0;font-size:15px;color:#334155;text-transform:uppercase;letter-spacing:0.04em;">Commitments</h2>
<table role="presentation" cellpadding="0" cellspacing="0" width="100%" style="border-collapse:collapse;font-size:13px;border:1px solid #e2e8f0;border-radius:6px;overflow:hidden;">
<thead><tr style="background:#f1f5f9;">
<th align="left" style="padding:10px 12px;color:#475569;font-weight:600;">Service / SKU</th>
<th align="left" style="padding:10px 12px;color:#475569;font-weight:600;">Region</th>
<th align="right" style="padding:10px 12px;color:#475569;font-weight:600;">Term · Payment</th>
<th align="right" style="padding:10px 12px;color:#475569;font-weight:600;">Upfront</th>
<th align="right" style="padding:10px 12px;color:#475569;font-weight:600;">Savings/mo</th>
</tr></thead>
<tbody>
{{range .Recommendations}}<tr style="border-top:1px solid #e2e8f0;">
<td style="padding:10px 12px;color:#0f172a;">{{.Count}}× {{.ResourceType}}{{if .Engine}} ({{.Engine}}){{end}}<div style="color:#94a3b8;font-size:11px;margin-top:2px;">{{.Service}}{{if .AccountLabel}} · {{.AccountLabel}}{{end}}</div></td>
<td style="padding:10px 12px;color:#0f172a;">{{.Region}}</td>
<td align="right" style="padding:10px 12px;color:#475569;">{{if .Term}}{{.Term}}yr{{end}}{{if .Payment}} · {{.Payment}}{{end}}</td>
<td align="right" style="padding:10px 12px;color:#0f172a;">${{printf "%.2f" .UpfrontCost}}</td>
<td align="right" style="padding:10px 12px;color:#16a34a;font-weight:600;">${{printf "%.2f" .MonthlySavings}}</td>
</tr>
{{end}}</tbody></table>
</td></tr>

<tr><td align="center" style="padding:8px 32px 4px 32px;">
<table role="presentation" cellpadding="0" cellspacing="0"><tr>
<td style="padding-right:12px;"><a href="{{.DashboardURL}}/purchases/approve/{{.ExecutionID}}?token={{urlquery .ApprovalToken}}" style="display:inline-block;padding:12px 28px;background:#16a34a;color:#ffffff;text-decoration:none;font-weight:600;font-size:14px;border-radius:6px;">Approve this purchase</a></td>
<td><a href="{{.DashboardURL}}/purchases/cancel/{{.ExecutionID}}?token={{urlquery .ApprovalToken}}" style="display:inline-block;padding:12px 28px;background:#ffffff;color:#dc2626;text-decoration:none;font-weight:600;font-size:14px;border-radius:6px;border:1px solid #dc2626;">Cancel this purchase</a></td>
</tr></table>
</td></tr>

<tr><td style="padding:8px 32px 24px 32px;">
<p style="margin:8px 0 0 0;color:#64748b;font-size:12px;line-height:1.5;">
{{if .CancellationWindowNote}}{{.CancellationWindowNote}}{{else}}Cloud commitments are charged once you approve. Cancellation windows after approval are limited and provider-dependent — see your cloud provider's billing console (e.g. AWS Account &amp; Billing → Refund) for the current policy on the resource type you're approving.{{end}}
</p>
<p style="margin:8px 0 0 0;color:#94a3b8;font-size:11px;">Clicking a link will require you to sign in if you aren't already; the action is then recorded against your logged-in account.</p>
</td></tr>

<tr><td style="padding:16px 32px;background:#f8fafc;border-top:1px solid #e2e8f0;border-radius:0 0 8px 8px;">
<p style="margin:0;color:#94a3b8;font-size:11px;">This is an automated message from CUDly. Do not share these links.</p>
</td></tr>

</table>
</td></tr></table>
</body></html>`

// SendPurchaseApprovalRequest sends an email asking the user to approve a direct
// purchase. Routes through SES SendEmail (not the SNS alerts topic) because the
// approval URL carries a one-time token scoped to the submitter — broadcasting
// that to every SNS subscriber would leak the authorisation. Returns
// ErrNoRecipient when data.RecipientEmail is empty and ErrNoFromEmail when
// FROM_EMAIL is unconfigured, so the caller can surface a precise reason in
// the API response instead of the prior silent no-op.
func (s *Sender) SendPurchaseApprovalRequest(ctx context.Context, data NotificationData) error {
	if data.RecipientEmail == "" {
		return ErrNoRecipient
	}
	// Both empty and malformed FROM_EMAIL (e.g. "noreply@" when the
	// subdomain_zone_name tfvar is unset) map to ErrNoFromEmail so the
	// handler can report "FROM_EMAIL not configured" — the prior behaviour
	// handed the bad string to SES and surfaced a BadRequestException stack
	// trace ("Missing domain") to the user.
	if !isValidFromEmail(s.fromEmail) {
		return ErrNoFromEmail
	}
	textBody, err := RenderPurchaseApprovalRequestEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render purchase approval request email (text): %w", err)
	}
	// Issue #287: render an HTML half too and ship the email as
	// multipart/alternative. HTML render failure is non-fatal — fall back
	// to single-part text so a template bug in the HTML half doesn't
	// silently swallow approval emails.
	htmlBody, htmlErr := RenderPurchaseApprovalRequestEmailHTML(data)
	if htmlErr != nil {
		htmlBody = ""
	}

	subject := fmt.Sprintf("CUDly - Purchase Approval Required (%d commitment(s))", len(data.Recommendations))
	return s.SendToEmailWithCCMultipart(ctx, data.RecipientEmail, data.CCEmails, subject, textBody, htmlBody)
}

// ---------------------------------------------------------------------------
// Account registration email templates
// ---------------------------------------------------------------------------

// RegistrationNotificationData is used to render the admin notification when a
// new account registers via the federation IaC.
type RegistrationNotificationData struct {
	AccountName  string
	Provider     string
	ExternalID   string
	ContactEmail string
	DashboardURL string
	// RecipientEmail is the primary (To) inbox — the first admin email,
	// or the global notification email if no admin has an email
	// configured. Leave empty to fall back to the SNS broadcast path.
	RecipientEmail string
	// CCEmails carry the remaining admin emails plus the global
	// notification email, deduped against RecipientEmail.
	CCEmails []string
	// AdminApprovers is the full set of admin emails that can approve or
	// reject this registration — rendered verbatim in the message body
	// so CC'd recipients know the action isn't theirs to take. The
	// account's own ContactEmail is intentionally NOT on this list
	// because the submitter can't self-approve their own registration.
	AdminApprovers []string
}

// RegistrationDecisionData is used to render the registrant notification when
// their registration is approved or rejected.
type RegistrationDecisionData struct {
	AccountName     string
	Provider        string
	ExternalID      string
	Decision        string // "approved" or "rejected"
	RejectionReason string
}

const registrationReceivedTemplate = `CUDly - New Account Registration
==================================

A new target account has requested to join your CUDly deployment.
{{if .AdminApprovers}}
Authorised reviewer(s):
{{range .AdminApprovers}}  - {{.}}
{{end}}
Only CUDly administrators listed above can approve or reject this
registration. Other recipients are CC'd for visibility only.
{{end}}
Account Details:
  Name:        {{.AccountName}}
  Provider:    {{.Provider}}
  External ID: {{.ExternalID}}
  Contact:     {{.ContactEmail}}

Review and approve/reject in the dashboard:
{{.DashboardURL}}

This is an automated message from CUDly.
`

const registrationDecisionTemplate = `CUDly - Account Registration {{.Decision}}
==================================

Your registration for account "{{.AccountName}}" ({{.Provider}} / {{.ExternalID}}) has been {{.Decision}}.
{{if .RejectionReason}}
Reason: {{.RejectionReason}}
{{end}}{{if eq .Decision "approved"}}
Next steps:
Your CUDly administrator will configure cross-account credentials.
You may be asked to deploy additional IaC templates to complete federation setup.
{{end}}
This is an automated message from CUDly.
`

// SendRegistrationReceivedNotification sends an email notifying CUDly
// administrators that a new account registration has been submitted. When
// data.RecipientEmail is set (caller resolved admin + global-notify
// recipients) the send routes through the targeted SES path so To / Cc
// semantics match the approver/visibility distinction embedded in the
// body. When RecipientEmail is empty the send falls back to the legacy
// SNS broadcast path so deployments that never configured admin users
// still get notified.
func (s *Sender) SendRegistrationReceivedNotification(ctx context.Context, data RegistrationNotificationData) error {
	body, err := RenderRegistrationReceivedEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render registration received email: %w", err)
	}
	subject := fmt.Sprintf("CUDly - New Account Registration: %s (%s)", data.AccountName, data.Provider)
	if data.RecipientEmail == "" {
		return s.SendNotification(ctx, subject, body)
	}
	return s.SendToEmailWithCC(ctx, data.RecipientEmail, data.CCEmails, subject, body)
}

// SendRegistrationDecisionNotification sends an email to the registrant when
// their registration is approved or rejected.
func (s *Sender) SendRegistrationDecisionNotification(ctx context.Context, toEmail string, data RegistrationDecisionData) error {
	body, err := RenderRegistrationDecisionEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render registration decision email: %w", err)
	}
	subject := fmt.Sprintf("CUDly - Account Registration %s", data.Decision)
	return s.SendToEmail(ctx, toEmail, subject, body)
}
