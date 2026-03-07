package email

import (
	"bytes"
	"fmt"
	"net/url"
	"text/template"
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

// RenderWelcomeEmail renders the welcome email template
func RenderWelcomeEmail(email, dashboardURL, role string) (string, error) {
	return renderTemplate("welcome", welcomeUserTemplate, WelcomeUserData{
		Email:        email,
		DashboardURL: dashboardURL,
		Role:         role,
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
