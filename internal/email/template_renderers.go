package email

import (
	"bytes"
	"fmt"
	"html/template"
	"net/url"
)

// templateFuncs provides common functions available in email templates.
var templateFuncs = template.FuncMap{
	"urlquery": url.QueryEscape,
}

// renderTemplate parses and executes a named template with the given data.
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

// RenderPasswordResetEmail renders the password reset email template
func RenderPasswordResetEmail(email, resetURL string) (string, error) {
	return renderTemplate("reset", passwordResetTemplate, PasswordResetData{
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

// RenderWelcomeEmail renders the welcome email template
func RenderWelcomeEmail(email, dashboardURL, role string) (string, error) {
	return renderTemplate("welcome", welcomeUserTemplate, WelcomeUserData{
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

// RenderUserInviteEmail renders the user-invite email template.
func RenderUserInviteEmail(email, setupURL string) (string, error) {
	return renderTemplate("user-invite", userInviteTemplate, UserInviteData{
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

// RenderNewRecommendationsEmail renders the new recommendations email template
func RenderNewRecommendationsEmail(data NotificationData) (string, error) {
	return renderTemplate("recommendations", newRecommendationsTemplate, data)
}

// RenderScheduledPurchaseEmail renders the scheduled purchase email template
func RenderScheduledPurchaseEmail(data NotificationData) (string, error) {
	return renderTemplate("scheduled", scheduledPurchaseTemplate, data)
}

// RenderPurchaseConfirmationEmail renders the purchase confirmation email template
func RenderPurchaseConfirmationEmail(data NotificationData) (string, error) {
	return renderTemplate("confirmation", purchaseConfirmationTemplate, data)
}

// RenderPurchaseFailedEmail renders the purchase failed email template
func RenderPurchaseFailedEmail(data NotificationData) (string, error) {
	return renderTemplate("failed", purchaseFailedTemplate, data)
}

// RenderRIExchangePendingApprovalEmail renders the RI exchange pending approval email template
func RenderRIExchangePendingApprovalEmail(data RIExchangeNotificationData) (string, error) {
	return renderTemplate("ri-exchange-pending", riExchangePendingApprovalTemplate, data)
}

// RenderRIExchangeCompletedEmail renders the RI exchange completed email template
func RenderRIExchangeCompletedEmail(data RIExchangeNotificationData) (string, error) {
	return renderTemplate("ri-exchange-completed", riExchangeCompletedTemplate, data)
}

// RenderPurchaseApprovalRequestEmail renders the plain-text purchase
// approval request email template. Issue #287: this is the multipart
// `text/plain` half — pair with RenderPurchaseApprovalRequestEmailHTML
// for the styled HTML half.
func RenderPurchaseApprovalRequestEmail(data NotificationData) (string, error) {
	return renderTemplate("purchase-approval-request", purchaseApprovalRequestTemplate, data)
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

// RenderRegistrationReceivedEmail renders the admin notification for a new registration.
func RenderRegistrationReceivedEmail(data RegistrationNotificationData) (string, error) {
	return renderTemplate("registration-received", registrationReceivedTemplate, data)
}

// RenderRegistrationDecisionEmail renders the registrant notification for approval/rejection.
func RenderRegistrationDecisionEmail(data RegistrationDecisionData) (string, error) {
	return renderTemplate("registration-decision", registrationDecisionTemplate, data)
}
