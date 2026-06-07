package email

import (
	"bytes"
	"fmt"
	"html/template"
	"net/url"
	texttemplate "text/template"
)

// templateFuncs provides common functions available in email templates.
// Both html/template and text/template engines accept the same FuncMap shape;
// the urlquery func behaves identically in both contexts.
var templateFuncs = template.FuncMap{
	"urlquery": url.QueryEscape,
}

// textTemplateFuncs mirrors templateFuncs for text/template so plain-text
// renderers share the same urlquery behaviour without crossing package types.
var textTemplateFuncs = texttemplate.FuncMap{
	"urlquery": url.QueryEscape,
}

// renderTemplate parses and executes a named template with html/template.
// Use this for HTML email bodies -- html/template performs context-aware
// escaping that prevents XSS in rendered HTML. Never use it for plain-text
// bodies: it would HTML-escape ampersands and angle-brackets in data fields
// (e.g. names, email addresses), corrupting the plain-text output.
func renderTemplate(name, tmplText string, data any) (string, error) {
	tmpl, err := template.New(name).Funcs(templateFuncs).Parse(tmplText)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// renderTextTemplate parses and executes a named template with text/template.
// Use this for plain-text email bodies -- text/template emits data verbatim
// so values like "O'Brien" and "a&b@x.com" are preserved unchanged.
func renderTextTemplate(name, tmplText string, data any) (string, error) {
	tmpl, err := texttemplate.New(name).Funcs(textTemplateFuncs).Parse(tmplText)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// RenderPasswordResetEmail renders the plain-text password reset email template.
func RenderPasswordResetEmail(email, resetURL string) (string, error) {
	return renderTextTemplate("reset", passwordResetTemplate, PasswordResetData{
		Email:    email,
		ResetURL: resetURL,
	})
}

// RenderPasswordResetEmailHTML renders the HTML half of the password reset
// email — pair with RenderPasswordResetEmail for multipart/alternative
// delivery. Issue #355.
func RenderPasswordResetEmailHTML(email, resetURL string) (string, error) {
	return renderTemplate("reset-html", passwordResetHTMLTemplate, PasswordResetData{
		Email:    email,
		ResetURL: resetURL,
	})
}

// RenderWelcomeEmail renders the plain-text welcome email template.
func RenderWelcomeEmail(email, dashboardURL, role string) (string, error) {
	return renderTextTemplate("welcome", welcomeUserTemplate, WelcomeUserData{
		Email:        email,
		DashboardURL: dashboardURL,
		Role:         role,
	})
}

// RenderWelcomeEmailHTML renders the HTML half of the welcome email.
// Issue #355.
func RenderWelcomeEmailHTML(email, dashboardURL, role string) (string, error) {
	return renderTemplate("welcome-html", welcomeUserHTMLTemplate, WelcomeUserData{
		Email:        email,
		DashboardURL: dashboardURL,
		Role:         role,
	})
}

// RenderUserInviteEmail renders the plain-text user-invite email template.
func RenderUserInviteEmail(email, setupURL string) (string, error) {
	return renderTextTemplate("user-invite", userInviteTemplate, UserInviteData{
		Email:    email,
		SetupURL: setupURL,
	})
}

// RenderUserInviteEmailHTML renders the HTML half of the user-invite email.
// Issue #355.
func RenderUserInviteEmailHTML(email, setupURL string) (string, error) {
	return renderTemplate("user-invite-html", userInviteHTMLTemplate, UserInviteData{
		Email:    email,
		SetupURL: setupURL,
	})
}

// RenderNewRecommendationsEmail renders the plain-text new recommendations email template.
func RenderNewRecommendationsEmail(data NotificationData) (string, error) {
	return renderTextTemplate("recommendations", newRecommendationsTemplate, data)
}

// RenderScheduledPurchaseEmail renders the plain-text scheduled purchase email template.
func RenderScheduledPurchaseEmail(data NotificationData) (string, error) {
	return renderTextTemplate("scheduled", scheduledPurchaseTemplate, data)
}

// RenderPurchaseConfirmationEmail renders the plain-text purchase confirmation email template.
func RenderPurchaseConfirmationEmail(data NotificationData) (string, error) {
	return renderTextTemplate("confirmation", purchaseConfirmationTemplate, data)
}

// RenderPurchaseFailedEmail renders the plain-text purchase failed email template.
func RenderPurchaseFailedEmail(data NotificationData) (string, error) {
	return renderTextTemplate("failed", purchaseFailedTemplate, data)
}

// RenderRIExchangePendingApprovalEmail renders the plain-text RI exchange pending approval email template.
func RenderRIExchangePendingApprovalEmail(data RIExchangeNotificationData) (string, error) {
	return renderTextTemplate("ri-exchange-pending", riExchangePendingApprovalTemplate, data)
}

// RenderRIExchangeCompletedEmail renders the plain-text RI exchange completed email template.
func RenderRIExchangeCompletedEmail(data RIExchangeNotificationData) (string, error) {
	return renderTextTemplate("ri-exchange-completed", riExchangeCompletedTemplate, data)
}

// RenderPurchaseApprovalRequestEmail renders the plain-text purchase
// approval request email template. Issue #287: this is the multipart
// text/plain half -- pair with RenderPurchaseApprovalRequestEmailHTML
// for the styled HTML half.
func RenderPurchaseApprovalRequestEmail(data NotificationData) (string, error) {
	return renderTextTemplate("purchase-approval-request", purchaseApprovalRequestTemplate, data)
}

// RenderPurchaseApprovalRequestEmailHTML renders the HTML half of the
// purchase approval request email. Inline-styled per email-client
// constraints (Outlook etc. don't load external stylesheets reliably).
// The plain-text half (RenderPurchaseApprovalRequestEmail) carries the
// same content; receiving clients pick whichever they support via the
// multipart/alternative wrapper assembled by the sender.
func RenderPurchaseApprovalRequestEmailHTML(data NotificationData) (string, error) {
	return renderTemplate("purchase-approval-request-html", purchaseApprovalRequestHTMLTemplate, data)
}

// RenderRegistrationReceivedEmail renders the plain-text admin notification for a new registration.
func RenderRegistrationReceivedEmail(data RegistrationNotificationData) (string, error) {
	return renderTextTemplate("registration-received", registrationReceivedTemplate, data)
}

// RenderRegistrationDecisionEmail renders the plain-text registrant notification for approval/rejection.
func RenderRegistrationDecisionEmail(data RegistrationDecisionData) (string, error) {
	return renderTextTemplate("registration-decision", registrationDecisionTemplate, data)
}
