package server

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/pkg/exchange"
	awsprovider "github.com/LeanerCloud/CUDly/providers/aws"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
)

// handleRIExchangeReshape runs the automated RI exchange analysis and execution.
func (app *Application) handleRIExchangeReshape(ctx context.Context) (*exchange.AutoExchangeResult, error) {
	log.Println("Starting RI exchange reshape analysis...")

	cfg, err := app.Config.GetGlobalConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	if !cfg.RIExchangeEnabled {
		log.Println("RI exchange automation is disabled, skipping")
		return nil, nil
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	accountID := resolveAccountID(ctx, awsCfg)

	ec2Client := awsprovider.NewEC2ClientDirect(awsCfg)
	instances, err := ec2Client.ListConvertibleReservedInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list convertible RIs: %w", err)
	}

	recsClient := awsprovider.NewRecommendationsClientDirect(awsCfg)
	utilData, err := recsClient.GetRIUtilization(ctx, cfg.RIExchangeLookbackDays)
	if err != nil {
		return nil, fmt.Errorf("failed to get RI utilization: %w", err)
	}

	riInfos, utilInfos, riMetadata := convertForAutoExchange(instances, utilData)

	exchangeClient := exchange.NewExchangeClient(awsCfg)
	lookupFn := func(ctx context.Context, instanceType, productDesc, tenancy, scope string, duration int64) (string, error) {
		return ec2Client.FindConvertibleOffering(ctx, ec2svc.FindConvertibleOfferingParams{
			InstanceType:       instanceType,
			ProductDescription: productDesc,
			Tenancy:            tenancy,
			Scope:              scope,
			Duration:           duration,
		})
	}

	store := newConfigExchangeStoreAdapter(app.Config)

	result, err := exchange.RunAutoExchange(ctx, exchange.RunAutoExchangeParams{
		Store:          store,
		ExchangeClient: exchangeClient,
		LookupOffering: lookupFn,
		RIs:            riInfos,
		Utilization:    utilInfos,
		Config: exchange.RIExchangeConfig{
			Mode:                     cfg.RIExchangeMode,
			UtilizationThreshold:     cfg.RIExchangeUtilizationThreshold,
			MaxPaymentPerExchangeUSD: cfg.RIExchangeMaxPerExchangeUSD,
			MaxPaymentDailyUSD:       cfg.RIExchangeMaxDailyUSD,
			LookbackDays:             cfg.RIExchangeLookbackDays,
		},
		AccountID:    accountID,
		Region:       awsCfg.Region,
		DashboardURL: app.appConfig.DashboardURL,
		RIMetadata:   riMetadata,
	})
	if err != nil {
		return nil, fmt.Errorf("auto exchange failed: %w", err)
	}

	app.sendExchangeNotification(ctx, result)

	log.Printf("RI exchange reshape complete: mode=%s completed=%d pending=%d failed=%d skipped=%d",
		result.Mode, len(result.Completed), len(result.Pending), len(result.Failed), len(result.Skipped))
	return result, nil
}

// resolveAccountID fetches the AWS account ID via STS. Returns "unknown" on failure
// since account_id is stored for audit trail only and is not used to scope queries.
func resolveAccountID(ctx context.Context, awsCfg aws.Config) string {
	stsClient := sts.NewFromConfig(awsCfg)
	identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		log.Printf("Warning: failed to get AWS account ID via STS: %v (using 'unknown')", err)
		return "unknown"
	}
	if identity.Account != nil {
		return *identity.Account
	}
	return "unknown"
}

// sendExchangeNotification sends email notifications based on the exchange result.
// Email errors are logged but don't fail the task.
func (app *Application) sendExchangeNotification(ctx context.Context, result *exchange.AutoExchangeResult) {
	if app.Email == nil {
		return
	}

	if !exchangeHasResults(result) {
		return
	}

	data := buildExchangeNotificationData(result, app.appConfig.DashboardURL)

	var err error
	if result.Mode == "manual" && len(result.Pending) > 0 {
		err = app.Email.SendRIExchangePendingApproval(ctx, data)
	} else if len(result.Completed)+len(result.Failed) > 0 {
		err = app.Email.SendRIExchangeCompleted(ctx, data)
	}

	if err != nil {
		log.Printf("Warning: failed to send RI exchange notification email: %v", err)
	}
}

func exchangeHasResults(result *exchange.AutoExchangeResult) bool {
	return len(result.Completed)+len(result.Pending)+len(result.Failed)+len(result.Skipped) > 0
}

// buildExchangeNotificationData converts AutoExchangeResult into email notification data.
func buildExchangeNotificationData(result *exchange.AutoExchangeResult, dashboardURL string) email.RIExchangeNotificationData {
	data := email.RIExchangeNotificationData{
		DashboardURL: dashboardURL,
		Mode:         result.Mode,
	}

	allOutcomes := make([]exchange.ExchangeOutcome, 0, len(result.Completed)+len(result.Pending)+len(result.Failed))
	allOutcomes = append(allOutcomes, result.Completed...)
	allOutcomes = append(allOutcomes, result.Pending...)
	allOutcomes = append(allOutcomes, result.Failed...)

	for _, o := range allOutcomes {
		data.Exchanges = append(data.Exchanges, email.RIExchangeItem{
			RecordID:           o.RecordID,
			ApprovalToken:      o.ApprovalToken,
			SourceRIID:         o.SourceRIID,
			SourceInstanceType: o.SourceInstanceType,
			TargetInstanceType: o.TargetInstanceType,
			TargetCount:        int(o.TargetCount),
			PaymentDue:         o.PaymentDue,
			ExchangeID:         o.ExchangeID,
			UtilizationPct:     o.UtilizationPct,
			Error:              o.Error,
		})
	}

	for _, s := range result.Skipped {
		data.Skipped = append(data.Skipped, email.SkippedExchange{
			SourceRIID:         s.SourceRIID,
			SourceInstanceType: s.SourceInstanceType,
			Reason:             s.Reason,
		})
	}

	return data
}

// convertForAutoExchange converts provider-specific types to exchange package types,
// including RI metadata needed for offering lookup.
func convertForAutoExchange(instances []ec2svc.ConvertibleRI, utilData []recommendations.RIUtilization) ([]exchange.RIInfo, []exchange.UtilizationInfo, map[string]exchange.RIMetadataInfo) {
	riInfos := make([]exchange.RIInfo, len(instances))
	riMetadata := make(map[string]exchange.RIMetadataInfo, len(instances))

	for i, inst := range instances {
		riInfos[i] = exchange.RIInfo{
			ID:                  inst.ReservedInstanceID,
			InstanceType:        inst.InstanceType,
			InstanceCount:       inst.InstanceCount,
			OfferingClass:       "convertible",
			NormalizationFactor: inst.NormalizationFactor,
		}
		riMetadata[inst.ReservedInstanceID] = exchange.RIMetadataInfo{
			ProductDescription: inst.ProductDescription,
			InstanceTenancy:    inst.InstanceTenancy,
			Scope:              inst.Scope,
			Duration:           inst.Duration,
		}
	}

	utilInfos := make([]exchange.UtilizationInfo, len(utilData))
	for i, u := range utilData {
		utilInfos[i] = exchange.UtilizationInfo{
			RIID:               u.ReservedInstanceID,
			UtilizationPercent: u.UtilizationPercent,
		}
	}

	return riInfos, utilInfos, riMetadata
}

// configExchangeStoreAdapter adapts config.StoreInterface to exchange.RIExchangeStore.
// It converts between config.RIExchangeRecord and exchange.ExchangeRecord.
type configExchangeStoreAdapter struct {
	store config.StoreInterface
}

func newConfigExchangeStoreAdapter(store config.StoreInterface) *configExchangeStoreAdapter {
	return &configExchangeStoreAdapter{store: store}
}

func (a *configExchangeStoreAdapter) SaveRIExchangeRecord(ctx context.Context, record *exchange.ExchangeRecord) error {
	cfgRecord := exchangeToConfigRecord(record)
	return a.store.SaveRIExchangeRecord(ctx, cfgRecord)
}

func (a *configExchangeStoreAdapter) CancelAllPendingExchanges(ctx context.Context) (int64, error) {
	return a.store.CancelAllPendingExchanges(ctx)
}

func (a *configExchangeStoreAdapter) GetStaleProcessingExchanges(ctx context.Context, olderThan time.Duration) ([]exchange.ExchangeRecord, error) {
	cfgRecords, err := a.store.GetStaleProcessingExchanges(ctx, olderThan)
	if err != nil {
		return nil, err
	}
	result := make([]exchange.ExchangeRecord, len(cfgRecords))
	for i, r := range cfgRecords {
		result[i] = configToExchangeRecord(&r)
	}
	return result, nil
}

func (a *configExchangeStoreAdapter) GetRIExchangeDailySpend(ctx context.Context, date time.Time) (string, error) {
	return a.store.GetRIExchangeDailySpend(ctx, date)
}

func (a *configExchangeStoreAdapter) CompleteRIExchange(ctx context.Context, id string, exchangeID string) error {
	return a.store.CompleteRIExchange(ctx, id, exchangeID)
}

func (a *configExchangeStoreAdapter) FailRIExchange(ctx context.Context, id string, errorMsg string) error {
	return a.store.FailRIExchange(ctx, id, errorMsg)
}

func exchangeToConfigRecord(r *exchange.ExchangeRecord) *config.RIExchangeRecord {
	return &config.RIExchangeRecord{
		ID:                 r.ID,
		AccountID:          r.AccountID,
		ExchangeID:         r.ExchangeID,
		Region:             r.Region,
		SourceRIIDs:        r.SourceRIIDs,
		SourceInstanceType: r.SourceInstanceType,
		SourceCount:        r.SourceCount,
		TargetOfferingID:   r.TargetOfferingID,
		TargetInstanceType: r.TargetInstanceType,
		TargetCount:        r.TargetCount,
		PaymentDue:         r.PaymentDue,
		Status:             r.Status,
		ApprovalToken:      r.ApprovalToken,
		Error:              r.Error,
		Mode:               r.Mode,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
		CompletedAt:        r.CompletedAt,
		ExpiresAt:          r.ExpiresAt,
	}
}

func configToExchangeRecord(r *config.RIExchangeRecord) exchange.ExchangeRecord {
	return exchange.ExchangeRecord{
		ID:                 r.ID,
		AccountID:          r.AccountID,
		ExchangeID:         r.ExchangeID,
		Region:             r.Region,
		SourceRIIDs:        r.SourceRIIDs,
		SourceInstanceType: r.SourceInstanceType,
		SourceCount:        r.SourceCount,
		TargetOfferingID:   r.TargetOfferingID,
		TargetInstanceType: r.TargetInstanceType,
		TargetCount:        r.TargetCount,
		PaymentDue:         r.PaymentDue,
		Status:             r.Status,
		ApprovalToken:      r.ApprovalToken,
		Error:              r.Error,
		Mode:               r.Mode,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
		CompletedAt:        r.CompletedAt,
		ExpiresAt:          r.ExpiresAt,
	}
}
