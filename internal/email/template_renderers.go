package email

import (
	"bytes"
	"fmt"
	"text/template"
)

// RenderPasswordResetEmail renders the password reset email template
func RenderPasswordResetEmail(email, resetURL string) (string, error) {
	tmpl, err := template.New("reset").Parse(passwordResetTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	data := PasswordResetData{
		Email:    email,
		ResetURL: resetURL,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// WelcomeEmailData holds data for welcome emails
type WelcomeEmailData struct {
	Email        string
	DashboardURL string
	Role         string
}

// RenderWelcomeEmail renders the welcome email template
func RenderWelcomeEmail(dashboardURL, role string) (string, error) {
	tmpl, err := template.New("welcome").Parse(welcomeUserTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	data := WelcomeEmailData{
		DashboardURL: dashboardURL,
		Role:         role,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// RenderNewRecommendationsEmail renders the new recommendations email template
func RenderNewRecommendationsEmail(data NotificationData) (string, error) {
	tmpl, err := template.New("recommendations").Parse(newRecommendationsTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// RenderScheduledPurchaseEmail renders the scheduled purchase email template
func RenderScheduledPurchaseEmail(data NotificationData) (string, error) {
	tmpl, err := template.New("scheduled").Parse(scheduledPurchaseTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// RenderPurchaseConfirmationEmail renders the purchase confirmation email template
func RenderPurchaseConfirmationEmail(data NotificationData) (string, error) {
	tmpl, err := template.New("confirmation").Parse(purchaseConfirmationTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// RenderPurchaseFailedEmail renders the purchase failed email template
func RenderPurchaseFailedEmail(data NotificationData) (string, error) {
	tmpl, err := template.New("failed").Parse(purchaseFailedTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}
