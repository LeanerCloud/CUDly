package config

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrRegistrationConflict is returned when a registration status transition fails
// because another request already changed the status (concurrent modification).
var ErrRegistrationConflict = errors.New("registration status conflict: already processed")

// CreateAccountRegistration inserts a new registration request. Returns an error
// wrapping "duplicate" when the partial unique index rejects a second pending
// registration for the same provider+external_id.
func (s *PostgresStore) CreateAccountRegistration(ctx context.Context, reg *AccountRegistration) error {
	if reg.ID == "" {
		reg.ID = uuid.New().String()
	}
	now := time.Now()
	reg.CreatedAt = now
	reg.UpdatedAt = now

	query := `
		INSERT INTO account_registrations (
			id, reference_token, status,
			provider, external_id, account_name, contact_email, description,
			source_provider,
			aws_role_arn, aws_auth_mode, aws_external_id,
			azure_subscription_id, azure_tenant_id, azure_client_id, azure_auth_mode,
			gcp_project_id, gcp_client_email, gcp_auth_mode,
			reg_credential_type, reg_credential_payload,
			created_at, updated_at
		) VALUES (
			$1, $2, $3,
			$4, $5, $6, $7, $8,
			$9,
			$10, $11, $12,
			$13, $14, $15, $16,
			$17, $18, $19,
			$20, $21,
			$22, $23
		)
	`

	_, err := s.db.Exec(ctx, query,
		reg.ID, reg.ReferenceToken, reg.Status,
		reg.Provider, reg.ExternalID, reg.AccountName, reg.ContactEmail, nullStringFromString(reg.Description),
		nullStringFromString(reg.SourceProvider),
		nullStringFromString(reg.AWSRoleARN), nullStringFromString(reg.AWSAuthMode), nullStringFromString(reg.AWSExternalID),
		nullStringFromString(reg.AzureSubscriptionID), nullStringFromString(reg.AzureTenantID),
		nullStringFromString(reg.AzureClientID), nullStringFromString(reg.AzureAuthMode),
		nullStringFromString(reg.GCPProjectID), nullStringFromString(reg.GCPClientEmail),
		nullStringFromString(reg.GCPAuthMode),
		nullStringFromString(reg.RegCredentialType), nullStringFromString(reg.RegCredentialPayload),
		reg.CreatedAt, reg.UpdatedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "idx_account_registrations_pending_unique") ||
			strings.Contains(err.Error(), "duplicate key") {
			return fmt.Errorf("duplicate: a pending registration already exists for this account")
		}
		return fmt.Errorf("failed to create account registration: %w", err)
	}
	return nil
}

// GetAccountRegistration returns a single registration by UUID.
func (s *PostgresStore) GetAccountRegistration(ctx context.Context, id string) (*AccountRegistration, error) {
	query := `SELECT ` + registrationColumns() + ` FROM account_registrations WHERE id = $1`
	return s.scanRegistration(ctx, query, id)
}

// GetAccountRegistrationByToken returns a registration by its reference_token.
func (s *PostgresStore) GetAccountRegistrationByToken(ctx context.Context, token string) (*AccountRegistration, error) {
	query := `SELECT ` + registrationColumns() + ` FROM account_registrations WHERE reference_token = $1`
	return s.scanRegistration(ctx, query, token)
}

// ListAccountRegistrations returns registrations matching the filter.
func (s *PostgresStore) ListAccountRegistrations(ctx context.Context, filter AccountRegistrationFilter) ([]AccountRegistration, error) {
	var conditions []string
	var args []interface{}
	idx := 1

	if filter.Status != nil {
		conditions = append(conditions, fmt.Sprintf("status = $%d", idx))
		args = append(args, *filter.Status)
		idx++
	}
	if filter.Provider != nil {
		conditions = append(conditions, fmt.Sprintf("provider = $%d", idx))
		args = append(args, *filter.Provider)
		idx++
	}
	if filter.Search != "" {
		conditions = append(conditions, fmt.Sprintf(
			"(account_name ILIKE $%d OR external_id ILIKE $%d OR contact_email ILIKE $%d)",
			idx, idx, idx,
		))
		args = append(args, "%"+filter.Search+"%")
		idx++
	}

	query := `SELECT ` + registrationColumns() + ` FROM account_registrations`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC"

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list account registrations: %w", err)
	}
	defer rows.Close()

	var regs []AccountRegistration
	for rows.Next() {
		reg, err := scanRegistrationRow(rows)
		if err != nil {
			return nil, err
		}
		regs = append(regs, *reg)
	}
	return regs, rows.Err()
}

// UpdateAccountRegistration updates the mutable workflow fields of a registration.
func (s *PostgresStore) UpdateAccountRegistration(ctx context.Context, reg *AccountRegistration) error {
	reg.UpdatedAt = time.Now()

	query := `
		UPDATE account_registrations SET
			status           = $2,
			rejection_reason = $3,
			cloud_account_id = $4,
			reviewed_by      = $5,
			reviewed_at      = $6,
			updated_at       = $7
		WHERE id = $1
	`

	var reviewedAt sql.NullTime
	if reg.ReviewedAt != nil {
		reviewedAt = sql.NullTime{Time: *reg.ReviewedAt, Valid: true}
	}

	_, err := s.db.Exec(ctx, query,
		reg.ID,
		reg.Status,
		nullStringFromString(reg.RejectionReason),
		nullStringFromPtr(reg.CloudAccountID),
		nullStringFromPtr(reg.ReviewedBy),
		reviewedAt,
		reg.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to update account registration: %w", err)
	}
	return nil
}

// TransitionRegistrationStatus atomically updates a registration's workflow fields
// only if the current status matches fromStatus. Returns ErrRegistrationConflict
// when 0 rows are affected (another request already changed the status).
func (s *PostgresStore) TransitionRegistrationStatus(ctx context.Context, reg *AccountRegistration, fromStatus string) error {
	reg.UpdatedAt = time.Now()

	query := `
		UPDATE account_registrations SET
			status           = $2,
			rejection_reason = $3,
			cloud_account_id = $4,
			reviewed_by      = $5,
			reviewed_at      = $6,
			updated_at       = $7
		WHERE id = $1 AND status = $8
	`

	var reviewedAt sql.NullTime
	if reg.ReviewedAt != nil {
		reviewedAt = sql.NullTime{Time: *reg.ReviewedAt, Valid: true}
	}

	result, err := s.db.Exec(ctx, query,
		reg.ID,
		reg.Status,
		nullStringFromString(reg.RejectionReason),
		nullStringFromPtr(reg.CloudAccountID),
		nullStringFromPtr(reg.ReviewedBy),
		reviewedAt,
		reg.UpdatedAt,
		fromStatus,
	)
	if err != nil {
		return fmt.Errorf("failed to transition account registration status: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrRegistrationConflict
	}
	return nil
}

// DeleteAccountRegistration removes a registration record by ID.
func (s *PostgresStore) DeleteAccountRegistration(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM account_registrations WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete account registration: %w", err)
	}
	return nil
}

// registrationColumns returns the column list used by SELECT queries.
func registrationColumns() string {
	return `id, reference_token, status,
		provider, external_id, account_name, contact_email, description,
		source_provider,
		aws_role_arn, aws_auth_mode, aws_external_id,
		azure_subscription_id, azure_tenant_id, azure_client_id, azure_auth_mode,
		gcp_project_id, gcp_client_email, gcp_auth_mode,
		reg_credential_type, reg_credential_payload,
		rejection_reason, cloud_account_id, reviewed_by, reviewed_at,
		created_at, updated_at`
}

// scannable is satisfied by both pgx.Row and pgx.Rows.
type scannable interface {
	Scan(dest ...interface{}) error
}

func scanRegistrationRow(row scannable) (*AccountRegistration, error) {
	var reg AccountRegistration
	var description, sourceProvider sql.NullString
	var awsRoleARN, awsAuthMode, awsExternalID sql.NullString
	var azureSubID, azureTenantID, azureClientID, azureAuthMode sql.NullString
	var gcpProjectID, gcpClientEmail, gcpAuthMode sql.NullString
	var regCredType, regCredPayload sql.NullString
	var rejectionReason sql.NullString
	var cloudAccountID, reviewedBy sql.NullString
	var reviewedAt sql.NullTime

	err := row.Scan(
		&reg.ID, &reg.ReferenceToken, &reg.Status,
		&reg.Provider, &reg.ExternalID, &reg.AccountName, &reg.ContactEmail, &description,
		&sourceProvider,
		&awsRoleARN, &awsAuthMode, &awsExternalID,
		&azureSubID, &azureTenantID, &azureClientID, &azureAuthMode,
		&gcpProjectID, &gcpClientEmail, &gcpAuthMode,
		&regCredType, &regCredPayload,
		&rejectionReason, &cloudAccountID, &reviewedBy, &reviewedAt,
		&reg.CreatedAt, &reg.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	reg.Description = description.String
	reg.SourceProvider = sourceProvider.String
	reg.AWSRoleARN = awsRoleARN.String
	reg.AWSAuthMode = awsAuthMode.String
	reg.AWSExternalID = awsExternalID.String
	reg.AzureSubscriptionID = azureSubID.String
	reg.AzureTenantID = azureTenantID.String
	reg.AzureClientID = azureClientID.String
	reg.AzureAuthMode = azureAuthMode.String
	reg.GCPProjectID = gcpProjectID.String
	reg.GCPClientEmail = gcpClientEmail.String
	reg.GCPAuthMode = gcpAuthMode.String
	reg.RegCredentialType = regCredType.String
	reg.RegCredentialPayload = regCredPayload.String
	reg.HasCredentials = regCredType.Valid && regCredType.String != ""
	reg.RejectionReason = rejectionReason.String
	if cloudAccountID.Valid {
		reg.CloudAccountID = &cloudAccountID.String
	}
	if reviewedBy.Valid {
		reg.ReviewedBy = &reviewedBy.String
	}
	if reviewedAt.Valid {
		reg.ReviewedAt = &reviewedAt.Time
	}

	return &reg, nil
}

func (s *PostgresStore) scanRegistration(ctx context.Context, query string, arg interface{}) (*AccountRegistration, error) {
	row := s.db.QueryRow(ctx, query, arg)
	reg, err := scanRegistrationRow(row)
	if err != nil {
		if err == sql.ErrNoRows || strings.Contains(err.Error(), "no rows") {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get account registration: %w", err)
	}
	return reg, nil
}

func nullStringFromPtr(s *string) sql.NullString {
	if s == nil || *s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}
