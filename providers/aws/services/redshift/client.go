// Package redshift provides AWS Redshift Reserved Nodes client
package redshift

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
	redshifttypes "github.com/aws/aws-sdk-go-v2/service/redshift/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/retry"
	"github.com/LeanerCloud/CUDly/providers/aws/internal/purchasecfg"
)

// RedshiftAPI defines the interface for Redshift operations (enables mocking)
type RedshiftAPI interface {
	PurchaseReservedNodeOffering(ctx context.Context, params *redshift.PurchaseReservedNodeOfferingInput, optFns ...func(*redshift.Options)) (*redshift.PurchaseReservedNodeOfferingOutput, error)
	DescribeReservedNodeOfferings(ctx context.Context, params *redshift.DescribeReservedNodeOfferingsInput, optFns ...func(*redshift.Options)) (*redshift.DescribeReservedNodeOfferingsOutput, error)
	DescribeReservedNodes(ctx context.Context, params *redshift.DescribeReservedNodesInput, optFns ...func(*redshift.Options)) (*redshift.DescribeReservedNodesOutput, error)
	CreateTags(ctx context.Context, params *redshift.CreateTagsInput, optFns ...func(*redshift.Options)) (*redshift.CreateTagsOutput, error)
	DescribeTags(ctx context.Context, params *redshift.DescribeTagsInput, optFns ...func(*redshift.Options)) (*redshift.DescribeTagsOutput, error)
}

// STSAPI is the subset of STS this client calls to resolve the caller's
// account ID for ARN construction. Declared at package level so tests can
// substitute a fake.
type STSAPI interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// Client handles AWS Redshift Reserved Nodes
type Client struct {
	client    RedshiftAPI
	stsClient STSAPI
	region    string

	// accountID is resolved lazily on first tag call via STS. Guarded by
	// accountOnce so concurrent purchases don't each call STS.
	accountOnce sync.Once
	accountID   string
	accountErr  error
}

// NewClient creates a new Redshift client with purchase-path retry/timeout
// settings. See purchasecfg for rationale.
func NewClient(cfg aws.Config) *Client {
	pcfg := purchasecfg.NewConfig(cfg)
	return &Client{
		client:    redshift.NewFromConfig(pcfg),
		stsClient: sts.NewFromConfig(pcfg),
		region:    cfg.Region,
	}
}

// SetRedshiftAPI sets a custom Redshift API client (for testing)
func (c *Client) SetRedshiftAPI(api RedshiftAPI) {
	c.client = api
}

// SetSTSAPI sets a custom STS client (for testing)
func (c *Client) SetSTSAPI(api STSAPI) {
	c.stsClient = api
}

// GetServiceType returns the service type
func (c *Client) GetServiceType() common.ServiceType {
	return common.ServiceDataWarehouse
}

// GetRegion returns the region
func (c *Client) GetRegion() string {
	return c.region
}

// GetRecommendations returns empty as Redshift uses centralized Cost Explorer recommendations
func (c *Client) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	return []common.Recommendation{}, nil
}

// GetExistingCommitments retrieves existing Redshift Reserved Nodes
func (c *Client) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)
	var marker *string

	for {
		input := &redshift.DescribeReservedNodesInput{
			Marker:     marker,
			MaxRecords: aws.Int32(100),
		}

		response, err := c.client.DescribeReservedNodes(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe reserved nodes: %w", err)
		}

		for _, node := range response.ReservedNodes {
			state := aws.ToString(node.State)
			if state != "active" && state != "payment-pending" {
				continue
			}

			termMonths := getTermMonthsFromDuration(aws.ToInt32(node.Duration))

			commitment := common.Commitment{
				Provider:       common.ProviderAWS,
				CommitmentID:   aws.ToString(node.ReservedNodeId),
				CommitmentType: common.CommitmentReservedInstance,
				Service:        common.ServiceDataWarehouse,
				Region:         c.region,
				ResourceType:   aws.ToString(node.NodeType),
				Count:          int(aws.ToInt32(node.NodeCount)),
				State:          state,
				StartDate:      aws.ToTime(node.StartTime),
				EndDate:        aws.ToTime(node.StartTime).AddDate(0, termMonths, 0),
			}

			commitments = append(commitments, commitment)
		}

		if response.Marker == nil || aws.ToString(response.Marker) == "" {
			break
		}
		marker = response.Marker
	}

	return commitments, nil
}

// PurchaseCommitment purchases a Redshift Reserved Node.
//
// PurchaseReservedNodeOfferingInput has no Tags field — tagging happens
// post-purchase via redshift:CreateTags, which requires a full ARN
// (arn:aws:redshift:<region>:<account>:reservednode:<id>). The account ID
// is resolved lazily on first tag call via sts:GetCallerIdentity and cached
// for the lifetime of the client. Tagging failure is logged but does NOT
// fail the purchase — the reserved node is already bought.
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

	// Idempotency dedupe guard (issue #641). Redshift's
	// PurchaseReservedNodeOfferingInput has NO customer-supplied ID and NO
	// ClientToken, the ReservedNode resource has no native AlreadyExists fault,
	// and DescribeReservedNodes offers no tag filter — so this is an EC2-style
	// tag-guard: before buying, list active reserved nodes and check (via
	// DescribeTags on each node's ARN) whether one already carries the
	// idempotency token tag; if so, this is a re-drive that already succeeded —
	// short-circuit. A lookup error must NOT fall through to a purchase.
	//
	// CAVEAT (documented residual window): the guard's correctness depends on
	// the post-purchase CreateTags below actually persisting on a reserved-node
	// ARN, which AWS has not confirmed it supports. If tagging is silently
	// unsupported the guard cannot recognise the prior purchase and a re-drive
	// could double-buy — the same irreducible "purchase-then-tag-fails" window
	// EC2 has, but potentially permanent here. This residual is backstopped by
	// the recovery sweep's safe-fail + operator-confirm (issue #635), which is
	// why #641 does not by itself unblock Redshift auto-re-drive.
	if opts.IdempotencyToken != "" {
		existingID, found, lookupErr := c.findNodeByIdempotencyToken(ctx, opts.IdempotencyToken)
		if lookupErr != nil {
			result.Error = fmt.Errorf("idempotency lookup failed before Redshift purchase (refusing to purchase to avoid a possible double-buy): %w", lookupErr)
			return result, result.Error
		}
		if found {
			log.Printf("Redshift reserved node for idempotency token %s already exists (%s); skipping purchase (issue #641 re-drive)", common.MaskToken(opts.IdempotencyToken), existingID)
			result.Success = true
			result.CommitmentID = existingID
			return result, nil
		}
	}

	input := &redshift.PurchaseReservedNodeOfferingInput{
		ReservedNodeOfferingId: aws.String(offeringID),
		NodeCount:              aws.Int32(int32(rec.Count)), // #nosec G115 -- Count from CE recommendation; AWS RI purchase limits keep this far below math.MaxInt32
	}

	response, err := c.client.PurchaseReservedNodeOffering(ctx, input)
	if err != nil {
		result.Error = fmt.Errorf("failed to purchase Redshift Reserved Node: %w", err)
		return result, result.Error
	}

	if response.ReservedNode != nil {
		result.Success = true
		result.CommitmentID = aws.ToString(response.ReservedNode.ReservedNodeId)
		if response.ReservedNode.FixedPrice != nil {
			result.Cost = *response.ReservedNode.FixedPrice
		}
	} else {
		result.Error = fmt.Errorf("purchase response was empty")
		return result, result.Error
	}

	if err := c.tagReservedNode(ctx, result.CommitmentID, rec, opts.Source, opts.IdempotencyToken); err != nil {
		log.Printf("WARNING: failed to tag Redshift reserved node %s after purchase (node is bought; tag missing — idempotency guard degrades for this node, issue #641): %v", result.CommitmentID, err)
	}

	return result, nil
}

// findNodeByIdempotencyToken looks for an active or payment-pending Redshift
// reserved node tagged with the given idempotency token (issue #641). Redshift
// has no tag filter on DescribeReservedNodes and no reserved-node tag-search,
// so it lists active nodes and calls DescribeTags per node ARN to read tags
// client-side. Returns the node ID and true on the first match. Retired/expired
// nodes are excluded (same state filter as GetExistingCommitments). A DescribeTags
// error short-circuits as a lookup failure so the caller fails loud rather than
// risk a double-buy.
func (c *Client) findNodeByIdempotencyToken(ctx context.Context, token string) (string, bool, error) {
	accountID, err := c.resolveAccountID(ctx)
	if err != nil {
		return "", false, fmt.Errorf("resolve account ID for idempotency check: %w", err)
	}
	if accountID == "" {
		// Without an account ID we cannot build the ARN DescribeTags needs, so
		// the tag-guard cannot run. Fail loud: the caller must not silently buy.
		return "", false, fmt.Errorf("account ID unavailable for Redshift idempotency check (no STS client)")
	}

	var marker *string
	for {
		response, err := c.client.DescribeReservedNodes(ctx, &redshift.DescribeReservedNodesInput{
			Marker:     marker,
			MaxRecords: aws.Int32(100),
		})
		if err != nil {
			return "", false, fmt.Errorf("failed to describe reserved nodes for idempotency check: %w", err)
		}
		if nodeID, found, err := c.scanNodesForToken(ctx, response.ReservedNodes, accountID, token); err != nil || found {
			return nodeID, found, err
		}
		if response.Marker == nil || aws.ToString(response.Marker) == "" {
			break
		}
		marker = response.Marker
	}
	return "", false, nil
}

// scanNodesForToken checks each active/payment-pending node for the idempotency
// token tag (issue #641), returning the first match. A DescribeTags error
// short-circuits as a lookup failure.
func (c *Client) scanNodesForToken(ctx context.Context, nodes []redshifttypes.ReservedNode, accountID, token string) (string, bool, error) {
	for _, node := range nodes {
		state := aws.ToString(node.State)
		if state != "active" && state != "payment-pending" {
			continue
		}
		nodeID := aws.ToString(node.ReservedNodeId)
		if nodeID == "" {
			continue
		}
		arn := fmt.Sprintf("arn:aws:redshift:%s:%s:reservednode:%s", c.region, accountID, nodeID)
		tagged, err := c.nodeHasIdempotencyTag(ctx, arn, token)
		if err != nil {
			return "", false, fmt.Errorf("failed to read tags for reserved node %s: %w", nodeID, err)
		}
		if tagged {
			return nodeID, true, nil
		}
	}
	return "", false, nil
}

// nodeHasIdempotencyTag reports whether the reserved node at the given ARN
// carries the idempotency token tag (issue #641).
func (c *Client) nodeHasIdempotencyTag(ctx context.Context, arn, token string) (bool, error) {
	out, err := c.client.DescribeTags(ctx, &redshift.DescribeTagsInput{
		ResourceName: aws.String(arn),
		TagKeys:      []string{common.IdempotencyTagKey},
		TagValues:    []string{token},
	})
	if err != nil {
		return false, err
	}
	for _, tr := range out.TaggedResources {
		if tr.Tag != nil &&
			aws.ToString(tr.Tag.Key) == common.IdempotencyTagKey &&
			aws.ToString(tr.Tag.Value) == token {
			return true, nil
		}
	}
	return false, nil
}

// resolveAccountID fetches the caller's AWS account ID via STS and caches it.
// Returns ("", nil) — i.e. an empty string with no error — when the STS
// client is nil (e.g. a test client that skipped SetSTSAPI). Callers must
// treat empty as "can't tag" and skip the tag call.
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

// tagReservedNode constructs the reserved-node ARN and calls redshift:CreateTags.
// Retries up to 4 attempts (1s/2s/4s backoff) on validation errors that can
// indicate the node isn't yet visible to the tagging API. Returns nil when
// there is nothing to tag (no source AND no idempotency token) OR when the
// account ID can't be resolved — both mean "don't tag", logged by the caller.
//
// The idempotency token tag (issue #641) is load-bearing for the pre-purchase
// findNodeByIdempotencyToken guard: if it is not written, a re-drive cannot
// recognise this node as already-purchased.
func (c *Client) tagReservedNode(ctx context.Context, nodeID string, rec common.Recommendation, source, idempotencyToken string) error {
	if source == "" && idempotencyToken == "" {
		return nil
	}
	accountID, err := c.resolveAccountID(ctx)
	if err != nil {
		return fmt.Errorf("resolve account ID: %w", err)
	}
	if accountID == "" {
		return fmt.Errorf("account ID unavailable (no STS client)")
	}

	arn := fmt.Sprintf("arn:aws:redshift:%s:%s:reservednode:%s", c.region, accountID, nodeID)
	// Rich self-describing tag set (issue #687): the Redshift purchase API
	// does not accept a customer-supplied node ID, so the only way to make
	// the reserved node identifiable from the AWS console alone (without
	// cross-referencing CUDly) is to encode the same descriptors that the
	// other AWS service clients embed in their reservation name. The Name tag
	// uses BuildReservationName to produce the same rich format so operators
	// can identify the node without cross-referencing CUDly's purchase audit log.
	displayName := common.BuildReservationName(common.ReservationNameFields{
		Service:      "redshift",
		Region:       rec.Region,
		ResourceType: rec.ResourceType,
		Count:        rec.Count,
		Term:         rec.Term,
		Payment:      rec.PaymentOption,
		Now:          time.Now(),
	}, "redshift-reserved-")
	tags := []redshifttypes.Tag{
		{Key: aws.String("Name"), Value: aws.String(displayName)},
		{Key: aws.String("Purpose"), Value: aws.String("Reserved Node Purchase")},
		{Key: aws.String("NodeType"), Value: aws.String(rec.ResourceType)},
		{Key: aws.String("Region"), Value: aws.String(rec.Region)},
		{Key: aws.String("Count"), Value: aws.String(fmt.Sprintf("%d", rec.Count))},
		{Key: aws.String("Term"), Value: aws.String(rec.Term)},
		{Key: aws.String("PaymentOption"), Value: aws.String(rec.PaymentOption)},
		{Key: aws.String("PurchaseDate"), Value: aws.String(time.Now().Format("2006-01-02"))},
		{Key: aws.String("Tool"), Value: aws.String("CUDly")},
	}
	if source != "" {
		tags = append(tags, redshifttypes.Tag{Key: aws.String(common.PurchaseTagKey), Value: aws.String(source)})
	}
	if idempotencyToken != "" {
		tags = append(tags, redshifttypes.Tag{Key: aws.String(common.IdempotencyTagKey), Value: aws.String(idempotencyToken)})
	}

	cfg := retry.Config{MaxAttempts: 4, BaseDelay: time.Second, MaxDelay: 4 * time.Second}
	return retry.Do(ctx, cfg, func(perAttemptCtx context.Context, _ int) error {
		_, err := c.client.CreateTags(perAttemptCtx, &redshift.CreateTagsInput{
			ResourceName: aws.String(arn),
			Tags:         tags,
		})
		if err == nil {
			return nil
		}
		// All Redshift CreateTags errors that aren't transient (e.g. auth,
		// invalid ARN) are permanent. No documented "resource not yet
		// visible" retry condition for reserved nodes, so a single pass
		// is usually enough; the retry budget exists to absorb network
		// flakes not state-machine delays.
		return fmt.Errorf("%w: %w", retry.ErrPermanent, err)
	})
}

// maxOfferingPages is the maximum number of DescribeReservedNodeOfferings
// pages to walk before giving up. At MaxRecords=100 per page this caps the
// search at 500 offerings. Exceeding the cap returns a diagnostic error instead
// of timing out the Lambda budget (issue #688).
//
// NOTE: DescribeReservedNodeOfferings has no NodeType or payment-option filter
// fields -- all matching must be done client-side. The cap is the primary guard
// against indefinite pagination on sparse offerings.
const maxOfferingPages = 5

// findOfferingID finds the appropriate Reserved Node offering ID.
// Redshift's DescribeReservedNodeOfferings has no server-side node-type or
// payment-option filter, so matching is done client-side with a pagination cap
// to prevent indefinite runtime (issue #688).
// execID is the purchase execution UUID for log correlation; pass "" when
// calling outside of a purchase flow (ValidateOffering, GetOfferingDetails).
func (c *Client) findOfferingID(ctx context.Context, rec common.Recommendation, execID string) (string, error) {
	requiredMonths, err := requiredMonthsForTerm(rec.Term)
	if err != nil {
		return "", err
	}
	tag := purchasecfg.ResolveTag(execID)
	t0 := time.Now()
	log.Printf("purchase[%s]: Redshift findOfferingID starting (nodeType=%s term=%s payment=%s)",
		tag, rec.ResourceType, rec.Term, rec.PaymentOption)

	var marker *string
	page := 0
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		page++
		if page > maxOfferingPages {
			return "", fmt.Errorf("pagination cap reached after %d pages for Redshift %s (issue #688)",
				maxOfferingPages, rec.ResourceType)
		}

		input := &redshift.DescribeReservedNodeOfferingsInput{
			MaxRecords: aws.Int32(100),
			Marker:     marker,
		}

		pageStart := time.Now()
		result, err := c.client.DescribeReservedNodeOfferings(ctx, input)
		if err != nil {
			log.Printf("purchase[%s]: Redshift findOfferingID page %d failed after %s (total %s): %v",
				tag, page, time.Since(pageStart), time.Since(t0), err)
			return "", fmt.Errorf("failed to describe offerings: %w", err)
		}
		log.Printf("purchase[%s]: Redshift findOfferingID page %d: %d offerings in %s",
			tag, page, len(result.ReservedNodeOfferings), time.Since(pageStart))

		if id, scanErr := c.scanRedshiftOfferingPage(result.ReservedNodeOfferings, rec, requiredMonths); scanErr != nil {
			return "", scanErr
		} else if id != "" {
			log.Printf("purchase[%s]: Redshift findOfferingID found match on page %d after %s total",
				tag, page, time.Since(t0))
			return id, nil
		}

		if result.Marker == nil || aws.ToString(result.Marker) == "" {
			break
		}
		marker = result.Marker
	}

	log.Printf("purchase[%s]: Redshift findOfferingID exhausted %d page(s) in %s -- no match",
		tag, page, time.Since(t0))
	return "", fmt.Errorf("no offerings found for Redshift %s after %d page(s) (issue #688)",
		rec.ResourceType, page)
}

// scanRedshiftOfferingPage finds a matching offering in a single page of results.
// Returns ("", nil) when no match is found on the page so the caller can continue paginating.
// Returns an error when an offering matches on node type and duration but carries an
// unrecognised ReservedNodeOfferingType -- this surfaces unexpected enum values rather
// than silently skipping them and potentially committing to the wrong offering.
//
// In addition to node type and duration, the requested payment option is matched
// against the offering's price shape (08-H2). Redshift does not encode the
// payment option in the Regular/Upgradable ReservedNodeOfferingType enum; it is
// expressed through the offering's FixedPrice (upfront) and recurring charge,
// so an offering whose price shape does not match the operator's chosen payment
// option is skipped rather than purchased on the wrong terms.
func (c *Client) scanRedshiftOfferingPage(offerings []redshifttypes.ReservedNodeOffering, rec common.Recommendation, requiredMonths int) (string, error) {
	for _, offering := range offerings {
		if offering.NodeType == nil || *offering.NodeType != rec.ResourceType {
			continue
		}
		if !c.matchesDuration(offering.Duration, requiredMonths) {
			continue
		}
		offeringTypeStr := string(offering.ReservedNodeOfferingType)
		if !c.matchesOfferingType(offeringTypeStr) {
			return "", fmt.Errorf("Redshift offering %s has unexpected type %q (rec: %s)",
				aws.ToString(offering.ReservedNodeOfferingId), offeringTypeStr, rec.ResourceType)
		}
		if !matchesPaymentOption(offering, rec.PaymentOption) {
			continue
		}
		return aws.ToString(offering.ReservedNodeOfferingId), nil
	}
	return "", nil
}

// offeringRecurringRate returns the offering's recurring charge: the hourly
// RecurringCharges entry when present, otherwise the UsagePrice. Redshift
// no-upfront/partial-upfront offerings carry a recurring charge while
// all-upfront offerings do not.
func offeringRecurringRate(offering redshifttypes.ReservedNodeOffering) float64 {
	for _, charge := range offering.RecurringCharges {
		if charge.RecurringChargeAmount != nil && aws.ToString(charge.RecurringChargeFrequency) == "Hourly" {
			return *charge.RecurringChargeAmount
		}
	}
	return aws.ToFloat64(offering.UsagePrice)
}

// matchesPaymentOption reports whether a Redshift reserved-node offering's price
// shape matches the requested payment option (08-H2). The payment option is not
// carried in the Regular/Upgradable offering-type enum, so it is derived from
// the upfront (FixedPrice) and recurring components:
//
//   - all-upfront:     upfront > 0, recurring == 0
//   - no-upfront:      upfront == 0, recurring > 0
//   - partial-upfront: upfront > 0, recurring > 0
//
// An empty/unknown requested option matches nothing (the caller skips the
// offering and ultimately errors with "no offerings found") so a malformed
// recommendation never buys on an arbitrarily-chosen payment option. AWS RI
// pricing has no fractional cents below this threshold, so a strict > 0 test is
// safe against float noise.
func matchesPaymentOption(offering redshifttypes.ReservedNodeOffering, paymentOption string) bool {
	upfront := aws.ToFloat64(offering.FixedPrice)
	recurring := offeringRecurringRate(offering)
	hasUpfront := upfront > 0
	hasRecurring := recurring > 0
	switch paymentOption {
	case "all-upfront":
		return hasUpfront && !hasRecurring
	case "no-upfront":
		return !hasUpfront && hasRecurring
	case "partial-upfront":
		return hasUpfront && hasRecurring
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
		return 0, fmt.Errorf("unsupported Redshift reservation term %q: must be one of 1yr, 1, 3yr, 3", term)
	}
}

// matchesDuration checks if the offering duration matches the required term
// length in months (as produced by requiredMonthsForTerm).
func (c *Client) matchesDuration(offeringDuration *int32, requiredMonths int) bool {
	if offeringDuration == nil {
		return false
	}

	offeringMonths := *offeringDuration / 2592000
	return int(offeringMonths) == requiredMonths
}

// matchesOfferingType checks if the offering type is a valid Redshift reserved
// node offering type. Redshift uses "Regular" and "Upgradable" as offering-type
// identifiers; this is orthogonal to the payment option, which is matched
// separately by matchesPaymentOption from the offering's price shape (08-H2).
func (c *Client) matchesOfferingType(offeringType string) bool {
	return offeringType == "Regular" || offeringType == "Upgradable"
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

	input := &redshift.DescribeReservedNodeOfferingsInput{
		ReservedNodeOfferingId: aws.String(offeringID),
		MaxRecords:             aws.Int32(1),
	}

	result, err := c.client.DescribeReservedNodeOfferings(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get offering details: %w", err)
	}

	if len(result.ReservedNodeOfferings) == 0 {
		return nil, fmt.Errorf("offering not found: %s", offeringID)
	}

	offering := result.ReservedNodeOfferings[0]

	details := &common.OfferingDetails{
		OfferingID:   aws.ToString(offering.ReservedNodeOfferingId),
		ResourceType: aws.ToString(offering.NodeType),
		Term:         fmt.Sprintf("%d", aws.ToInt32(offering.Duration)),
		// Report the derived payment option (08-H2): the offering's price shape,
		// not the Regular/Upgradable ReservedNodeOfferingType enum, which is not
		// a payment option. Lets the caller reconcile the bought terms.
		PaymentOption: derivePaymentOption(offering),
		UpfrontCost:   aws.ToFloat64(offering.FixedPrice),
		RecurringCost: offeringRecurringRate(offering),
		Currency:      aws.ToString(offering.CurrencyCode),
	}

	return details, nil
}

// derivePaymentOption infers the offering's payment option from its price shape
// (08-H2), returning the canonical CUDly payment-option string. Returns "unknown"
// when the shape matches no known option (e.g. an offering with neither upfront
// nor recurring charge) so callers never mistake it for a deliberate choice.
func derivePaymentOption(offering redshifttypes.ReservedNodeOffering) string {
	hasUpfront := aws.ToFloat64(offering.FixedPrice) > 0
	hasRecurring := offeringRecurringRate(offering) > 0
	switch {
	case hasUpfront && !hasRecurring:
		return "all-upfront"
	case !hasUpfront && hasRecurring:
		return "no-upfront"
	case hasUpfront && hasRecurring:
		return "partial-upfront"
	default:
		return "unknown"
	}
}

// GetValidResourceTypes returns valid Redshift node types by querying the API
func (c *Client) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	nodeTypes := make(map[string]bool)
	var marker *string

	for {
		input := &redshift.DescribeReservedNodeOfferingsInput{
			MaxRecords: aws.Int32(100),
			Marker:     marker,
		}

		result, err := c.client.DescribeReservedNodeOfferings(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe offerings: %w", err)
		}

		for _, offering := range result.ReservedNodeOfferings {
			if offering.NodeType != nil {
				nodeTypes[*offering.NodeType] = true
			}
		}

		if result.Marker == nil || aws.ToString(result.Marker) == "" {
			break
		}
		marker = result.Marker
	}

	types := make([]string, 0, len(nodeTypes))
	for nodeType := range nodeTypes {
		types = append(types, nodeType)
	}
	sort.Strings(types)
	return types, nil
}

// getTermMonthsFromDuration converts duration in seconds to months
func getTermMonthsFromDuration(duration int32) int {
	offeringMonths := duration / 2592000
	if offeringMonths >= 30 {
		return 36
	}
	return 12
}
