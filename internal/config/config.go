package config

import (
	"fmt"
	"time"
)

// PaymentOption represents the payment option for Reserved Instances
type PaymentOption string

const (
	PaymentOptionAllUpfront     PaymentOption = "all-upfront"
	PaymentOptionPartialUpfront PaymentOption = "partial-upfront"
	PaymentOptionNoUpfront      PaymentOption = "no-upfront"
)

// AZConfig represents the availability zone configuration
type AZConfig string

const (
	AZConfigSingleAZ AZConfig = "single-az"
	AZConfigMultiAZ  AZConfig = "multi-az"
)

// TermDuration represents the term duration for Reserved Instances
type TermDuration int32

const (
	TermDuration1Year TermDuration = 12
	TermDuration3Year TermDuration = 36
)

// RIConfig represents a Reserved Instance configuration
type RIConfig struct {
	Region         string        `json:"region"`
	InstanceType   string        `json:"instance_type"`
	Engine         string        `json:"engine"`
	AZConfig       AZConfig      `json:"az_config"`
	PaymentOption  PaymentOption `json:"payment_option"`
	Term           TermDuration  `json:"term"`
	Count          int32         `json:"count"`
	Description    string        `json:"description"`
	EstimatedCost  float64       `json:"estimated_cost,omitempty"`
	SavingsPercent float64       `json:"savings_percent,omitempty"`
}

// Validate checks if the RIConfig is valid
func (r *RIConfig) Validate() error {
	if r.Region == "" {
		return fmt.Errorf("region is required")
	}
	if r.InstanceType == "" {
		return fmt.Errorf("instance type is required")
	}
	if r.Engine == "" {
		return fmt.Errorf("engine is required")
	}
	if r.Count <= 0 {
		return fmt.Errorf("count must be greater than 0")
	}
	if r.AZConfig != AZConfigSingleAZ && r.AZConfig != AZConfigMultiAZ {
		return fmt.Errorf("AZ config must be 'single-az' or 'multi-az'")
	}
	if r.PaymentOption != PaymentOptionAllUpfront &&
		r.PaymentOption != PaymentOptionPartialUpfront &&
		r.PaymentOption != PaymentOptionNoUpfront {
		return fmt.Errorf("invalid payment option: %s", r.PaymentOption)
	}
	if r.Term != TermDuration1Year && r.Term != TermDuration3Year {
		return fmt.Errorf("invalid term duration: %d", r.Term)
	}
	return nil
}

// GetDurationString returns the duration as a string for AWS API
func (r *RIConfig) GetDurationString() string {
	switch r.Term {
	case TermDuration1Year:
		return "1yr"
	case TermDuration3Year:
		return "3yr"
	default:
		return ""
	}
}

// GetMultiAZ returns true if the configuration is for Multi-AZ
func (r *RIConfig) GetMultiAZ() bool {
	return r.AZConfig == AZConfigMultiAZ
}

// GenerateDescription creates a human-readable description
func (r *RIConfig) GenerateDescription() string {
	azConfig := "Single-AZ"
	if r.GetMultiAZ() {
		azConfig = "Multi-AZ"
	}
	return fmt.Sprintf("%s %s %s", r.Engine, r.InstanceType, azConfig)
}

// PurchaseResult represents the result of a purchase operation
type PurchaseResult struct {
	Config        RIConfig  `json:"config"`
	Success       bool      `json:"success"`
	PurchaseID    string    `json:"purchase_id,omitempty"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	Timestamp     time.Time `json:"timestamp"`
	ActualCost    float64   `json:"actual_cost,omitempty"`
	ReservationID string    `json:"reservation_id,omitempty"`
}

// GetStatusString returns a human-readable status
func (p *PurchaseResult) GetStatusString() string {
	if p.Success {
		return "SUCCESS"
	}
	return "FAILED"
}

// GetMessage returns the appropriate message based on success/failure
func (p *PurchaseResult) GetMessage() string {
	if p.Success {
		if p.PurchaseID != "" {
			return fmt.Sprintf("Purchase ID: %s", p.PurchaseID)
		}
		return "Purchase successful"
	}
	return p.ErrorMessage
}

// Default configurations for common use cases
var (
	DefaultPaymentOption = PaymentOptionPartialUpfront
	DefaultTerm          = TermDuration3Year
	DefaultRegion        = "eu-central-1"
)

// CreateDefaultConfig creates a default configuration with the given parameters
func CreateDefaultConfig(engine, instanceType string, count int32) *RIConfig {
	config := &RIConfig{
		Region:        DefaultRegion,
		InstanceType:  instanceType,
		Engine:        engine,
		AZConfig:      AZConfigSingleAZ,
		PaymentOption: DefaultPaymentOption,
		Term:          DefaultTerm,
		Count:         count,
	}
	config.Description = config.GenerateDescription()
	return config
}

// SupportedEngines lists all supported RDS engines
var SupportedEngines = []string{
	"aurora-mysql",
	"aurora-postgresql",
	"mysql",
	"postgres",
	"mariadb",
	"oracle-ee",
	"oracle-se2",
	"sqlserver-ee",
	"sqlserver-se",
	"sqlserver-ex",
	"sqlserver-web",
}

// SupportedInstanceTypes lists commonly used RDS instance types
var SupportedInstanceTypes = []string{
	"db.t4g.micro",
	"db.t4g.small",
	"db.t4g.medium",
	"db.t4g.large",
	"db.r6g.large",
	"db.r6g.xlarge",
	"db.r6g.2xlarge",
	"db.r6g.4xlarge",
	"db.r6i.large",
	"db.r6i.xlarge",
	"db.r6i.2xlarge",
	"db.r6i.4xlarge",
}

// IsEngineSupported checks if the given engine is supported
func IsEngineSupported(engine string) bool {
	for _, supported := range SupportedEngines {
		if supported == engine {
			return true
		}
	}
	return false
}

// IsInstanceTypeSupported checks if the given instance type is supported
func IsInstanceTypeSupported(instanceType string) bool {
	for _, supported := range SupportedInstanceTypes {
		if supported == instanceType {
			return true
		}
	}
	return false
}
