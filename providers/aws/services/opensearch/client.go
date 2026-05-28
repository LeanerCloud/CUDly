// Package opensearch provides AWS OpenSearch Reserved Instances client
package opensearch

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/aws/aws-sdk-go-v2/service/opensearch/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/retry"
	"github.com/LeanerCloud/CUDly/providers/aws/internal/purchasecfg"
)

// OpenSearchAPI defines the interface for OpenSearch operations (enables mocking)
type OpenSearchAPI interface {
	PurchaseReservedInstanceOffering(ctx context.Context, params *opensearch.PurchaseReservedInstanceOfferingInput, optFns ...func(*opensearch.Options)) (*opensearch.PurchaseReservedInstanceOfferingOutput, error)
	DescribeReservedInstanceOfferings(ctx context.Context, params *opensearch.DescribeReservedInstanceOfferingsInput, optFns ...func(*opensearch.Options)) (*opensearch.DescribeReservedInstanceOfferingsOutput, error)
	DescribeReservedInstances(ctx context.Context, params *opensearch.DescribeReservedInstancesInput, optFns ...func(*opensearch.Options)) (*opensearch.DescribeReservedInstancesOutput, error)
	AddTags(ctx context.Context, params *opensearch.AddTagsInput, optFns ...func(*opensearch.Options)) (*opensearch.AddTagsOutput, error)
}

// STSAPI is the subset of STS this client calls to resolve the caller's
// account ID for ARN construction.
type STSAPI interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// Client handles AWS OpenSearch Reserved Instances
type Client struct {
	client    OpenSearchAPI
	stsClient STSAPI
	region    string

	accountOnce sync.Once
	accountID   string
	accountErr  error
}

// NewClient creates a new OpenSearch client with purchase-path retry/timeout
// settings. See purchasecfg for rationale.
func NewClient(cfg aws.Config) *Client {
	pcfg := purchasecfg.NewConfig(cfg)
	return &Client{
		client:    opensearch.NewFromConfig(pcfg),
		stsClient: sts.NewFromConfig(pcfg),
		region:    cfg.Region,
	}
}

// SetOpenSearchAPI sets a custom OpenSearch API client (for testing)
func (c *Client) SetOpenSearchAPI(api OpenSearchAPI) {
	c.client = api
}

// SetSTSAPI sets a custom STS client (for testing)
func (c *Client) SetSTSAPI(api STSAPI) {
	c.stsClient = api
}

// GetServiceType returns the service type
func (c *Client) GetServiceType() common.ServiceType {
	return common.ServiceSearch
}

// GetRegion returns the region
func (c *Client) GetRegion() string {
	return c.region
}

// GetRecommendations returns empty as OpenSearch uses centralized Cost Explorer recommendations
func (c *Client) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	return []common.Recommendation{}, nil
}

// GetExistingCommitments retrieves existing OpenSearch Reserved Instances
func (c *Client) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)
	var nextToken *string

	for {
		input := &opensearch.DescribeReservedInstancesInput{
			NextToken:  nextToken,
			MaxResults: 100,
		}

		response, err := c.client.DescribeReservedInstances(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe reserved instances: %w", err)
		}

		for _, ri := range response.ReservedInstances {
			state := aws.ToString(ri.State)
			if state != "active" && state != "payment-pending" {
				continue
			}

			termMonths := getTermMonthsFromDuration(ri.Duration)

			commitment := common.Commitment{
				Provider:       common.ProviderAWS,
				CommitmentID:   aws.ToString(ri.ReservedInstanceId),
				CommitmentType: common.CommitmentReservedInstance,
				Service:        common.ServiceSearch,
				Region:         c.region,
				ResourceType:   string(ri.InstanceType),
				Count:          int(ri.InstanceCount),
				State:          state,
				StartDate:      aws.ToTime(ri.StartTime),
				EndDate:        aws.ToTime(ri.StartTime).AddDate(0, termMonths, 0),
			}

			commitments = append(commitments, commitment)
		}

		if response.NextToken == nil || aws.ToString(response.NextToken) == "" {
			break
		}
		nextToken = response.NextToken
	}

	return commitments, nil
}

// PurchaseCommitment purchases an OpenSearch Reserved Instance.
//
// PurchaseReservedInstanceOfferingInput has no Tags field -- tagging happens
// post-purchase via opensearch:AddTags with a reserved-instance ARN
// (arn:aws:es:<region>:<account>:reserved-instance/<uuid>). AWS hasn't
// explicitly documented reserved-instance as a supported resource type for
// AddTags (only domain/data-source/application), so the call may return a
// validation error -- in which case retry.ErrPermanent short-circuits and the
// failure is logged without blocking the purchase. If AWS ever adds support,
// this will start working with no code change.
// On tagging failure a structured line is emitted (OPENSEARCH_TAG_FAILED) so
// operator dashboards can alert on untagged RIs; see
// runbooks/opensearch-untagged-ri.md for the manual remediation steps.
func (c *Client) PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	offeringID, err := c.findOfferingID(ctx, rec, opts.ExecutionID)
	if err != nil {
		result.Error = fmt.Errorf("failed to find offering: %w", err)
		return result, result.Error
	}

	// When an idempotency token is supplied (issue #641) the ReservationName
	// is derived deterministically from it — ReservationName is unique per
	// account+region, so a re-drive sends the identical name and OpenSearch
	// rejects the duplicate server-side (ResourceAlreadyExistsException).
	// On the no-token CLI path (issue #687) compose a rich, self-describing
	// identifier matching the Azure DisplayName format so operators can
	// identify the reservation in the AWS console without cross-referencing
	// CUDly's purchase audit log.
	reservationName := common.IdempotentReservationID("opensearch-id-", opts.IdempotencyToken)
	if reservationName == "" {
		reservationName = common.BuildReservationName(common.ReservationNameFields{
			Service:      "opensearch",
			Region:       rec.Region,
			ResourceType: rec.ResourceType,
			Count:        rec.Count,
			Term:         rec.Term,
			Payment:      rec.PaymentOption,
			Now:          time.Now(),
		}, "opensearch-reserved-")
	}

	// Idempotency dedupe guard (issue #641): short-circuit if a reservation with
	// the derived name already exists; fail loud on lookup error.
	if existingID, shortCircuit, guardErr := c.idempotencyGuard(ctx, opts.IdempotencyToken, reservationName); guardErr != nil {
		result.Error = guardErr
		return result, result.Error
	} else if shortCircuit {
		result.Success = true
		result.CommitmentID = existingID
		return result, nil
	}

	input := &opensearch.PurchaseReservedInstanceOfferingInput{
		ReservedInstanceOfferingId: aws.String(offeringID),
		ReservationName:            aws.String(reservationName),
		InstanceCount:              aws.Int32(int32(rec.Count)), // #nosec G115 -- Count from CE recommendation; AWS RI purchase limits keep this far below math.MaxInt32
	}

	response, err := c.client.PurchaseReservedInstanceOffering(ctx, input)
	if err != nil {
		if existingID, recovered := c.recoverAlreadyExists(ctx, opts.IdempotencyToken, reservationName, err); recovered {
			result.Success = true
			result.CommitmentID = existingID
			return result, nil
		}
		result.Error = fmt.Errorf("failed to purchase OpenSearch RI: %w", err)
		return result, result.Error
	}

	if response.ReservedInstanceId != nil {
		result.Success = true
		result.CommitmentID = aws.ToString(response.ReservedInstanceId)
	} else {
		result.Error = fmt.Errorf("purchase response was empty")
		return result, result.Error
	}

	if err := c.tagReservedInstance(ctx, result.CommitmentID, rec, opts.Source); err != nil {
		// Structured line for operator dashboards / alerting. The RI is
		// purchased; only the tag is missing. Follow runbooks/opensearch-untagged-ri.md.
		log.Printf("OPENSEARCH_TAG_FAILED commitment_id=%s error=%v", result.CommitmentID, err)
	}

	return result, nil
}

// findReservationByName looks for an active or payment-pending OpenSearch
// reserved instance whose ReservationName matches the given name (issue #641),
// so a re-driven purchase can short-circuit. DescribeReservedInstances has no
// name filter, so it pages through all reservations and matches client-side.
// Retired/expired reservations are excluded (same state filter as
// GetExistingCommitments).
func (c *Client) findReservationByName(ctx context.Context, name string) (string, bool, error) {
	var nextToken *string
	for {
		response, err := c.client.DescribeReservedInstances(ctx, &opensearch.DescribeReservedInstancesInput{
			NextToken:  nextToken,
			MaxResults: 100,
		})
		if err != nil {
			return "", false, fmt.Errorf("failed to describe reserved instances for idempotency check: %w", err)
		}
		for _, ri := range response.ReservedInstances {
			if aws.ToString(ri.ReservationName) != name {
				continue
			}
			state := aws.ToString(ri.State)
			if state != "active" && state != "payment-pending" {
				continue
			}
			if ri.ReservedInstanceId != nil {
				return aws.ToString(ri.ReservedInstanceId), true, nil
			}
		}
		if response.NextToken == nil || aws.ToString(response.NextToken) == "" {
			break
		}
		nextToken = response.NextToken
	}
	return "", false, nil
}

// idempotencyGuard short-circuits a re-drive (issue #641): when token is set, it
// reports (existingID, true, nil) if a reservation with reservationName already
// exists, ("", false, nil) for a first-time purchase, or a fail-loud error on
// lookup failure. With an empty token it is a no-op.
func (c *Client) idempotencyGuard(ctx context.Context, token, reservationName string) (string, bool, error) {
	if token == "" {
		return "", false, nil
	}
	existingID, found, lookupErr := c.findReservationByName(ctx, reservationName)
	if lookupErr != nil {
		return "", false, fmt.Errorf("idempotency lookup failed before OpenSearch RI purchase (refusing to purchase to avoid a possible double-buy): %w", lookupErr)
	}
	if found {
		log.Printf("OpenSearch RI for idempotency token %s already exists (%s); skipping purchase (issue #641 re-drive)", common.MaskToken(token), existingID)
		return existingID, true, nil
	}
	return "", false, nil
}

// recoverAlreadyExists handles the native server-side dedupe backstop (issue
// #641): if the by-name guard missed the existing reservation but OpenSearch
// rejected the duplicate name with ResourceAlreadyExistsException, it re-Describes
// by name and returns (existingID, true) so the re-drive recovers it.
func (c *Client) recoverAlreadyExists(ctx context.Context, token, reservationName string, purchaseErr error) (string, bool) {
	if token == "" {
		return "", false
	}
	var already *types.ResourceAlreadyExistsException
	if !errors.As(purchaseErr, &already) {
		return "", false
	}
	existingID, found, lookupErr := c.findReservationByName(ctx, reservationName)
	if lookupErr == nil && found {
		log.Printf("OpenSearch RI %s already existed at purchase time; treating as idempotent re-drive (issue #641)", existingID)
		return existingID, true
	}
	return "", false
}

// resolveAccountID fetches the caller's AWS account ID via STS and caches it.
func (c *Client) resolveAccountID(ctx context.Context) (string, error) {
	c.accountOnce.Do(func() {
		if c.stsClient == nil {
			return
		}
		out, err := c.stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
		if err != nil {
			c.accountErr = err
			return
		}
		if out != nil && out.Account != nil {
			c.accountID = *out.Account
		}
	})
	return c.accountID, c.accountErr
}

// tagReservedInstance constructs the reserved-instance ARN and calls
// opensearch:AddTags. Short-circuits when source is empty (opt-out) or when
// the account ID can't be resolved. AddTags support for reserved-instance
// ARNs isn't guaranteed by AWS; failures are wrapped in retry.ErrPermanent
// so the first attempt is final and the outer retry budget isn't burned on
// a call AWS will never accept today.
func (c *Client) tagReservedInstance(ctx context.Context, riID string, rec common.Recommendation, source string) error {
	if source == "" {
		return nil
	}
	accountID, err := c.resolveAccountID(ctx)
	if err != nil {
		return fmt.Errorf("resolve account ID: %w", err)
	}
	if accountID == "" {
		return fmt.Errorf("account ID unavailable (no STS client)")
	}

	arn := fmt.Sprintf("arn:aws:es:%s:%s:reserved-instance/%s", c.region, accountID, riID)
	tagList := []types.Tag{
		{Key: aws.String("Purpose"), Value: aws.String("Reserved Instance Purchase")},
		{Key: aws.String("ResourceType"), Value: aws.String(rec.ResourceType)},
		{Key: aws.String("Region"), Value: aws.String(rec.Region)},
		{Key: aws.String("PurchaseDate"), Value: aws.String(time.Now().Format("2006-01-02"))},
		{Key: aws.String("Tool"), Value: aws.String("CUDly")},
		{Key: aws.String(common.PurchaseTagKey), Value: aws.String(source)},
	}

	cfg := retry.Config{MaxAttempts: 2, BaseDelay: time.Second, MaxDelay: 2 * time.Second}
	return retry.Do(ctx, cfg, func(perAttemptCtx context.Context, _ int) error {
		_, err := c.client.AddTags(perAttemptCtx, &opensearch.AddTagsInput{
			ARN:     aws.String(arn),
			TagList: tagList,
		})
		if err == nil {
			return nil
		}
		return fmt.Errorf("%w: %w", retry.ErrPermanent, err)
	})
}

// maxOfferingPages is the maximum number of DescribeReservedInstanceOfferings
// pages to walk before giving up. At MaxResults=100 per page this caps the
// search at 500 offerings. Exceeding the cap returns a diagnostic error instead
// of timing out the Lambda budget (issue #688).
//
// NOTE: DescribeReservedInstanceOfferings has no filter fields -- instance type,
// payment option, and duration must be matched client-side. The cap is therefore
// the primary guard against indefinite pagination on sparse offerings.
const maxOfferingPages = 5

// findOfferingID finds the appropriate Reserved Instance offering ID.
// The OpenSearch API does not support server-side filters on the offerings list,
// so all matching is done client-side (issue #688).
// execID is the purchase execution UUID for log correlation; pass "" when
// calling outside of a purchase flow (ValidateOffering, GetOfferingDetails).
func (c *Client) findOfferingID(ctx context.Context, rec common.Recommendation, execID string) (string, error) {
	requiredMonths, err := requiredMonthsForTerm(rec.Term)
	if err != nil {
		return "", err
	}
	tag := purchasecfg.ResolveTag(execID)
	t0 := time.Now()
	log.Printf("purchase[%s]: OpenSearch findOfferingID starting (instanceType=%s term=%s payment=%s)",
		tag, rec.ResourceType, rec.Term, rec.PaymentOption)

	var nextToken *string
	page := 0
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		page++
		if page > maxOfferingPages {
			return "", fmt.Errorf("pagination cap reached after %d pages for OpenSearch %s %s (issue #688)",
				maxOfferingPages, rec.ResourceType, rec.PaymentOption)
		}

		input := &opensearch.DescribeReservedInstanceOfferingsInput{
			MaxResults: 100,
			NextToken:  nextToken,
		}

		pageStart := time.Now()
		result, err := c.client.DescribeReservedInstanceOfferings(ctx, input)
		if err != nil {
			log.Printf("purchase[%s]: OpenSearch findOfferingID page %d failed after %s (total %s): %v",
				tag, page, time.Since(pageStart), time.Since(t0), err)
			return "", fmt.Errorf("failed to describe offerings: %w", err)
		}
		log.Printf("purchase[%s]: OpenSearch findOfferingID page %d: %d offerings in %s",
			tag, page, len(result.ReservedInstanceOfferings), time.Since(pageStart))

		if id, scanErr := c.scanOpenSearchOfferingPage(result.ReservedInstanceOfferings, rec, requiredMonths); scanErr != nil {
			return "", scanErr
		} else if id != "" {
			log.Printf("purchase[%s]: OpenSearch findOfferingID found match on page %d after %s total",
				tag, page, time.Since(t0))
			return id, nil
		}

		if result.NextToken == nil || aws.ToString(result.NextToken) == "" {
			break
		}
		nextToken = result.NextToken
	}

	log.Printf("purchase[%s]: OpenSearch findOfferingID exhausted %d page(s) in %s -- no match",
		tag, page, time.Since(t0))
	return "", fmt.Errorf("no offerings found for OpenSearch %s %s after %d page(s) (issue #688)",
		rec.ResourceType, rec.PaymentOption, page)
}

// scanOpenSearchOfferingPage finds a matching offering in a single page of results.
// Returns ("", nil) when no match is found on the page so the caller can continue paginating.
// Returns an error when an offering matches on instance type and duration but the payment
// option differs -- this surfaces API filter mismatches rather than silently skipping them.
func (c *Client) scanOpenSearchOfferingPage(offerings []types.ReservedInstanceOffering, rec common.Recommendation, requiredMonths int) (string, error) {
	wantPayment := normalizeOpenSearchPaymentOption(rec.PaymentOption)
	for _, offering := range offerings {
		if string(offering.InstanceType) != rec.ResourceType {
			continue
		}
		if !c.matchesDuration(offering.Duration, requiredMonths) {
			continue
		}
		gotPayment := string(offering.PaymentOption)
		if gotPayment != wantPayment {
			return "", fmt.Errorf("OpenSearch offering %s has payment option %q, want %q (rec: %s %s)",
				aws.ToString(offering.ReservedInstanceOfferingId), gotPayment, wantPayment,
				rec.ResourceType, rec.PaymentOption)
		}
		return aws.ToString(offering.ReservedInstanceOfferingId), nil
	}
	return "", nil
}

// normalizeOpenSearchPaymentOption converts a rec payment-option slug to the
// AWS OpenSearch PaymentOption string (matches types.ReservedInstancePaymentOption).
func normalizeOpenSearchPaymentOption(option string) string {
	switch option {
	case "all-upfront":
		return string(types.ReservedInstancePaymentOptionAllUpfront)
	case "partial-upfront":
		return string(types.ReservedInstancePaymentOptionPartialUpfront)
	case "no-upfront":
		return string(types.ReservedInstancePaymentOptionNoUpfront)
	default:
		return option
	}
}

// matchesPaymentOption checks if the offering payment option matches
func (c *Client) matchesPaymentOption(offeringOption types.ReservedInstancePaymentOption, required string) bool {
	switch required {
	case "all-upfront":
		return offeringOption == types.ReservedInstancePaymentOptionAllUpfront
	case "partial-upfront":
		return offeringOption == types.ReservedInstancePaymentOptionPartialUpfront
	case "no-upfront":
		return offeringOption == types.ReservedInstancePaymentOptionNoUpfront
	default:
		return false
	}
}

// requiredMonthsForTerm converts a reservation term string to the offering
// duration in months. Returns an error on any unrecognized or empty input so
// callers fail loud rather than silently matching (and buying) a 1-year
// offering when another commitment length was intended.
func requiredMonthsForTerm(term string) (int, error) {
	switch term {
	case "3yr", "3":
		return 36, nil
	case "1yr", "1":
		return 12, nil
	default:
		return 0, fmt.Errorf("unsupported OpenSearch reservation term %q: must be one of 1yr, 1, 3yr, 3", term)
	}
}

// matchesDuration checks if the offering duration matches the required term
// length in months (as produced by requiredMonthsForTerm).
func (c *Client) matchesDuration(offeringDuration int32, requiredMonths int) bool {
	offeringMonths := offeringDuration / 2592000 // 30 days in seconds
	return int(offeringMonths) >= requiredMonths-1 && int(offeringMonths) <= requiredMonths+1
}

// ValidateOffering checks if an offering exists without purchasing
func (c *Client) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
	_, err := c.findOfferingID(ctx, rec, "")
	return err
}

// GetOfferingDetails retrieves offering details
func (c *Client) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	offeringID, err := c.findOfferingID(ctx, rec, "")
	if err != nil {
		return nil, err
	}

	input := &opensearch.DescribeReservedInstanceOfferingsInput{
		ReservedInstanceOfferingId: aws.String(offeringID),
		MaxResults:                 1,
	}

	result, err := c.client.DescribeReservedInstanceOfferings(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get offering details: %w", err)
	}

	if len(result.ReservedInstanceOfferings) == 0 {
		return nil, fmt.Errorf("offering not found: %s", offeringID)
	}

	offering := result.ReservedInstanceOfferings[0]

	details := &common.OfferingDetails{
		OfferingID:    aws.ToString(offering.ReservedInstanceOfferingId),
		ResourceType:  string(offering.InstanceType),
		Term:          fmt.Sprintf("%d", offering.Duration),
		PaymentOption: string(offering.PaymentOption),
		UpfrontCost:   aws.ToFloat64(offering.FixedPrice),
		RecurringCost: aws.ToFloat64(offering.UsagePrice),
		Currency:      aws.ToString(offering.CurrencyCode),
	}

	return details, nil
}

// GetValidResourceTypes returns valid OpenSearch instance types (static list)
func (c *Client) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	return []string{
		"t2.small.search",
		"t2.medium.search",
		"t3.small.search",
		"t3.medium.search",
		"m5.large.search",
		"m5.xlarge.search",
		"m5.2xlarge.search",
		"m5.4xlarge.search",
		"m5.12xlarge.search",
		"m6g.large.search",
		"m6g.xlarge.search",
		"m6g.2xlarge.search",
		"m6g.4xlarge.search",
		"m6g.8xlarge.search",
		"m6g.12xlarge.search",
		"c5.large.search",
		"c5.xlarge.search",
		"c5.2xlarge.search",
		"c5.4xlarge.search",
		"c5.9xlarge.search",
		"c5.18xlarge.search",
		"c6g.large.search",
		"c6g.xlarge.search",
		"c6g.2xlarge.search",
		"c6g.4xlarge.search",
		"c6g.8xlarge.search",
		"c6g.12xlarge.search",
		"r5.large.search",
		"r5.xlarge.search",
		"r5.2xlarge.search",
		"r5.4xlarge.search",
		"r5.12xlarge.search",
		"r6g.large.search",
		"r6g.xlarge.search",
		"r6g.2xlarge.search",
		"r6g.4xlarge.search",
		"r6g.8xlarge.search",
		"r6g.12xlarge.search",
		"r6gd.large.search",
		"r6gd.xlarge.search",
		"r6gd.2xlarge.search",
		"r6gd.4xlarge.search",
		"r6gd.8xlarge.search",
		"r6gd.12xlarge.search",
		"i3.large.search",
		"i3.xlarge.search",
		"i3.2xlarge.search",
		"i3.4xlarge.search",
		"i3.8xlarge.search",
		"i3.16xlarge.search",
	}, nil
}

// getTermMonthsFromDuration converts duration in seconds to months
func getTermMonthsFromDuration(duration int32) int {
	offeringMonths := duration / 2592000
	if offeringMonths >= 30 {
		return 36
	}
	return 12
}
