package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/reservations/armreservations"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/smithy-go"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/credentials"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/exchange"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	awsprovider "github.com/LeanerCloud/CUDly/providers/aws"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
	azurecompute "github.com/LeanerCloud/CUDly/providers/azure/services/compute"
)

// reshapeEC2Client is the narrow slice of the EC2 client that
// getReshapeRecommendations needs. Scoped to this handler so mocks
// stay small. The concrete *ec2svc.Client returned by
// awsprovider.NewEC2ClientDirect already implements these methods
// (Go structural typing), so the nil-factory fallback path casts it
// directly.
//
// Cross-family alternatives no longer flow through here — they're
// sourced from the cached AWS Cost Explorer purchase recommendations
// in Postgres via purchaseRecLookupFromStore (see exchange_lookup.go),
// so the EC2 client only needs to enumerate convertible RIs.
type reshapeEC2Client interface {
	ListConvertibleReservedInstances(ctx context.Context) ([]ec2svc.ConvertibleRI, error)
}

// reshapeRecsClient is the narrow slice of the recommendations
// adapter that getReshapeRecommendations needs (the utilization
// fetcher injected into the cache wrapper). Scoped identically.
type reshapeRecsClient interface {
	GetRIUtilization(ctx context.Context, lookbackDays int, region string) ([]recommendations.RIUtilization, error)
}

// buildReshapeEC2Client honors the injected factory when set, falling
// back to the direct AWS SDK constructor otherwise. Tests inject a
// stub via Handler.reshapeEC2Factory; prod leaves the field nil.
func (h *Handler) buildReshapeEC2Client(cfg aws.Config) reshapeEC2Client {
	if h.reshapeEC2Factory != nil {
		return h.reshapeEC2Factory(cfg)
	}
	return awsprovider.NewEC2ClientDirect(cfg)
}

// buildReshapeRecsClient mirrors buildReshapeEC2Client for the
// recommendations adapter.
func (h *Handler) buildReshapeRecsClient(cfg aws.Config) reshapeRecsClient {
	if h.reshapeRecsFactory != nil {
		return h.reshapeRecsFactory(cfg)
	}
	return awsprovider.NewRecommendationsClientDirect(cfg)
}

// targetOfferingsEC2Client is the narrow EC2 interface that
// listTargetOfferings needs. Scoped here so tests can inject a tiny
// stub without implementing the full ec2svc.Client surface.
type targetOfferingsEC2Client interface {
	ListConvertibleReservedInstances(ctx context.Context) ([]ec2svc.ConvertibleRI, error)
	ListTargetOfferings(ctx context.Context, params ec2svc.ListTargetOfferingsParams) ([]ec2svc.TargetOffering, error)
}

// buildTargetOfferingsEC2Client honors the injected factory when set,
// falling back to the direct AWS SDK constructor otherwise.
func (h *Handler) buildTargetOfferingsEC2Client(cfg aws.Config) targetOfferingsEC2Client {
	if h.targetOfferingsEC2Factory != nil {
		return h.targetOfferingsEC2Factory(cfg)
	}
	return awsprovider.NewEC2ClientDirect(cfg)
}

// TargetOfferingsResponse is the response for
// GET /api/ri-exchange/target-offerings.
type TargetOfferingsResponse struct {
	Offerings []ec2svc.TargetOffering `json:"offerings"`
}

// offeringIDPattern matches a standard AWS offering UUID used for
// ReservedInstancesOfferingId values. Used both as a server-side guard
// (Defect 2) and to reject any stray free-text before it reaches AWS.
var offeringIDPattern = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
)

// listTargetOfferings returns valid convertible RI exchange target
// offerings for the source RI identified by ?source_ri_id=<uuid>.
//
// The handler looks up the source RI from DescribeReservedInstances,
// extracts its ProductDescription / Tenancy / Scope / Duration /
// OfferingType, and passes those to ec2svc.ListTargetOfferings which
// calls DescribeReservedInstancesOfferings with the same typed-field
// shape used by PR #690. Instance type is intentionally omitted from
// the query so AWS returns all valid target instance types -- the full
// menu of what the user can exchange into.
//
// GET /api/ri-exchange/target-offerings?source_ri_id=<uuid>&region=<region>.
func (h *Handler) listTargetOfferings(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requirePermission(ctx, req, "view", "purchases"); err != nil {
		return nil, err
	}

	sourceRIID := req.QueryStringParameters["source_ri_id"]
	if sourceRIID == "" {
		return nil, NewClientError(400, "source_ri_id is required")
	}

	region := req.QueryStringParameters["region"]
	cfg, err := h.loadAWSConfigWithRegion(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	ec2Client := h.buildTargetOfferingsEC2Client(cfg)

	// Fetch all convertible RIs to locate the source RI's attributes.
	// DescribeReservedInstances does not support a single-ID filter
	// without the full ARN, so we enumerate and filter by ID.
	ris, err := ec2Client.ListConvertibleReservedInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list convertible RIs: %w", err)
	}

	var sourceRI *ec2svc.ConvertibleRI
	for i := range ris {
		if ris[i].ReservedInstanceID == sourceRIID {
			sourceRI = &ris[i]
			break
		}
	}
	if sourceRI == nil {
		return nil, NewClientError(404, fmt.Sprintf("source RI %q not found in region %s", sourceRIID, cfg.Region))
	}

	offerings, err := ec2Client.ListTargetOfferings(ctx, ec2svc.ListTargetOfferingsParams{
		ProductDescription: sourceRI.ProductDescription,
		Tenancy:            sourceRI.InstanceTenancy,
		Scope:              sourceRI.Scope,
		Duration:           sourceRI.Duration,
		OfferingType:       sourceRI.OfferingType,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list target offerings: %w", err)
	}

	return &TargetOfferingsResponse{Offerings: offerings}, nil
}

// azureExchangeClient is the narrow interface that listExchangeableAzureRIs,
// getAzureCompatibleOfferings, and executeAzureExchange need from the Azure
// compute client. Satisfied by *azurecompute.ComputeClient; a stub can be
// injected via Handler.azureExchangeFactory for tests.
type azureExchangeClient interface {
	ListExchangeableReservations(ctx context.Context) ([]azurecompute.ExchangeableReservation, error)

	// CalculateExchange prices a proposed exchange without committing it.
	// Both getAzureCompatibleOfferings (read-only quote) and
	// executeAzureExchange (server-side re-quote before commit) call this;
	// only executeAzureExchange ever calls ExecuteExchange, and only with
	// the SessionID this same call just returned -- see executeAzureExchange's
	// doc comment for why a client-supplied session is never trusted.
	CalculateExchange(ctx context.Context, sources []azurecompute.ExchangeableReservation, targets []azurecompute.ExchangeTarget) (*azurecompute.ExchangePreview, []azurecompute.CompatibleOffering, error)
	ExecuteExchange(ctx context.Context, sessionID string) (*azurecompute.ExchangeResult, error)
}

// buildAzureExchangeClient returns the injected factory result when one has
// been set (test path), or constructs a real Azure compute client by resolving
// per-subscription credentials via the project's credential resolver (production
// path).
//
// Credential resolution mirrors every other Azure call in the project
// (scheduler.collectAzureForAccount, purchase/execution.go resolveAzureProvider):
// look up the registered CloudAccount whose ExternalID matches subscriptionID,
// then call credentials.ResolveAzureTokenCredentialWithOpts with the Handler's
// wired OIDC signer so managed_identity / client_secret / WIF all work.
//
// Graceful empty-state rules (returns nil client, nil error):
//   - subscriptionID is empty: GetCloudAccountByExternalID looks up an exact
//     match on external_id, and no registered account has an empty
//     external_id, so this unconditionally misses and the caller returns an
//     empty reservations list.
//   - subscriptionID is provided but no matching CloudAccount is found:
//     treated as "Azure not configured for this subscription".
//
// A genuine configuration error (missing credentials, auth failure) returns
// a non-nil error that the handler maps to a 500 with a clear message.
func (h *Handler) buildAzureExchangeClient(ctx context.Context, subscriptionID string) (azureExchangeClient, error) {
	if h.azureExchangeFactory != nil {
		return h.azureExchangeFactory(subscriptionID), nil
	}

	// Look up the registered Azure CloudAccount by its subscription ID.
	// For Azure, ExternalID stores the subscription ID (see handler_accounts.go:
	// buildSelfAccountRequest sets ExternalID = si.ExternalID() = si.SubscriptionID).
	account, err := h.config.GetCloudAccountByExternalID(ctx, "azure", subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("azure: look up account for subscription %q: %w", subscriptionID, err)
	}
	if account == nil {
		// No Azure account registered for this subscription.
		// Return nil client so the caller emits a graceful empty state.
		return nil, nil
	}

	cred, err := credentials.ResolveAzureTokenCredentialWithOpts(ctx, account, h.credStore, credentials.AzureResolveOptions{
		Signer:    h.signer,
		IssuerURL: h.issuerURL,
	})
	if err != nil {
		return nil, fmt.Errorf("azure: resolve credentials for subscription %q: %w", subscriptionID, err)
	}

	// Region is left empty -- ListExchangeableReservations uses the tenant-
	// wide armreservations API which is not scoped to a region.
	return azurecompute.NewClient(cred, subscriptionID, ""), nil
}

// listExchangeableAzureRIs returns active Azure VM reservations that are
// eligible for the cross-SKU/cross-region exchange flow (InstanceFlexibility
// == On, ProvisioningState == Succeeded). Requires "view:purchases" permission.
//
// ListExchangeableReservations is tenant-wide by design (see its doc
// comment: "the Azure Capacity exchange API operates on reservation order
// IDs which span subscriptions"), and that breadth does not change just
// because ?subscription_id= was supplied -- the parameter only picks which
// registered CloudAccount's credentials authenticate the call, not which
// rows come back. Left unfiltered, every caller with "view:purchases" saw
// every reservation the deployment's Azure credential could read, including
// subscriptions they are not scoped to (issue #1656). Both POST siblings on
// this resource deliberately withhold that same breadth (requireAzureSubscriptionScope
// + requireAzureSourceOwnership, both indistinguishable-denial), so this GET
// narrows the same way:
//
//   - ?subscription_id= supplied: requireAzureSubscriptionScope gates
//     whether the session may use that subscription at all (errNotFound,
//     not 403, matching the POST siblings so a scoped caller cannot probe
//     which subscriptions exist). The listing is then narrowed to that
//     subscription's own rows via BillingScopeID -- the same discriminator
//     requireAzureSourceOwnership uses -- since a reservation billed
//     elsewhere could never be named as a valid exchange source against
//     this subscription anyway.
//   - No subscription_id: the listing is narrowed to whatever the session's
//     allowed_accounts scope covers (filterAzureReservationsByScope).
//     Unrestricted/admin sessions see everything, matching every other
//     listing endpoint in this package. In production this branch is
//     defense-in-depth rather than the live path today: buildAzureExchangeClient
//     resolves subscriptionID via an exact external_id match, so an empty
//     subscriptionID never matches a registered account and the graceful
//     empty-state branch below returns before ListExchangeableReservations
//     is ever called. It becomes the live path the moment a caller can reach
//     ListExchangeableReservations without a subscription_id -- the
//     azureExchangeFactory test seam already exercises exactly that -- which
//     is why the filter stays rather than being deleted as unreachable.
//
// Rows that cannot be attributed to a registered CloudAccount (no
// BillingScopeID, or one that matches no CloudAccount) are dropped rather
// than kept, so an unresolvable scope never degrades to "show everything".
// That also keeps the two empty-result cases indistinguishable: a scoped
// caller whose own subscriptions hold zero exchangeable reservations sees
// the same empty list as one whose rows could not be attributed.
//
// When no Azure account is configured for the requested subscription (or no
// subscription is specified and none are registered), the handler returns an
// empty reservations list rather than a 500.
func (h *Handler) listExchangeableAzureRIs(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	subscriptionID := req.QueryStringParameters["subscription_id"]

	if subscriptionID != "" {
		if scopeErr := h.requireAzureSubscriptionScope(ctx, session, subscriptionID); scopeErr != nil {
			return nil, scopeErr
		}
	}

	client, err := h.buildAzureExchangeClient(ctx, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("failed to build Azure exchange client: %w", err)
	}
	if client == nil {
		// No Azure account configured for this subscription (or no Azure accounts
		// registered at all). Return an empty list rather than a 500 so the page
		// renders a "no reservations" state instead of an opaque error banner.
		return &ExchangeableAzureRIsResponse{Reservations: []azurecompute.ExchangeableReservation{}}, nil
	}

	reservations, err := client.ListExchangeableReservations(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list exchangeable Azure reservations: %w", err)
	}

	if subscriptionID != "" {
		reservations = filterAzureReservationsBySubscription(reservations, subscriptionID)
	} else {
		reservations, err = h.filterAzureReservationsByScope(ctx, session, reservations)
		if err != nil {
			return nil, err
		}
	}

	return &ExchangeableAzureRIsResponse{Reservations: reservations}, nil
}

// filterAzureReservationsBySubscription narrows a tenant-wide reservation
// listing down to the rows billed to subscriptionID, using the same
// BillingScopeID discriminator requireAzureSourceOwnership keys on (issue
// #1527). Reservations with no BillingScopeID, or one billed to a different
// subscription, are dropped.
//
// Called only after requireAzureSubscriptionScope has cleared the caller for
// subscriptionID; a reservation billed elsewhere could never be named as a
// valid exchange source against this subscription anyway
// (requireAzureSourceOwnership refuses cross-subscription sources), so
// returning the wider tenant listing here would just be the enumeration leak
// issue #1656 describes.
func filterAzureReservationsBySubscription(reservations []azurecompute.ExchangeableReservation, subscriptionID string) []azurecompute.ExchangeableReservation {
	scope := azureBillingScopeID(subscriptionID)
	filtered := make([]azurecompute.ExchangeableReservation, 0, len(reservations))
	for i := range reservations {
		if strings.EqualFold(reservations[i].BillingScopeID, scope) {
			filtered = append(filtered, reservations[i])
		}
	}
	return filtered
}

// filterAzureReservationsByScope narrows a tenant-wide Azure reservation
// listing down to the rows billed to a subscription the session's
// allowed_accounts scope covers. Mirrors filterDashboardRecommendations
// (handler_dashboard.go): unrestricted/admin sessions pass through
// unchanged; a restricted session keeps only the rows it can resolve back to
// one of its allowed CloudAccounts.
//
// Each reservation's BillingScopeID ("/subscriptions/{subscriptionID}") is
// the same ownership signal requireAzureSourceOwnership keys on (issue
// #1527) -- the subscription actually charged, which is the correct
// discriminator even for AppliedScopeType == Shared.
//
// Fails closed: a reservation with no BillingScopeID, or one that matches no
// registered Azure CloudAccount, is dropped rather than kept. Counts, not
// the dropped reservation/billing-scope identifiers, are safe to log --
// logging the identifiers would just relocate the disclosure this filter
// exists to prevent.
func (h *Handler) filterAzureReservationsByScope(ctx context.Context, session *Session, reservations []azurecompute.ExchangeableReservation) ([]azurecompute.ExchangeableReservation, error) {
	allowed, err := h.getAccountScope(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("failed to get allowed accounts: %w", err)
	}
	if allowed.AllowsAll() {
		// client.ListExchangeableReservations may return a nil slice for zero
		// results; every other path here returns a non-nil empty slice, so
		// normalize here too rather than letting an admin session alone see
		// {"reservations": null}.
		if reservations == nil {
			reservations = []azurecompute.ExchangeableReservation{}
		}
		return reservations, nil
	}

	provider := "azure"
	accounts, err := h.config.ListCloudAccounts(ctx, config.CloudAccountFilter{Provider: &provider})
	if err != nil {
		return nil, fmt.Errorf("failed to list cloud accounts: %w", err)
	}

	filtered := filterReservationsByScopeIndex(reservations, azureScopeIndex(accounts), allowed)
	if dropped := len(reservations) - len(filtered); dropped > 0 {
		logging.Debugf("azure exchange listing: filtered %d of %d tenant-wide reservations outside session scope", dropped, len(reservations))
	}
	return filtered, nil
}

// azureScopeIndex builds a lookup from ARM billing scope (lower-cased) to the
// registered Azure CloudAccount that owns it, so filterAzureReservationsByScope
// can resolve each reservation's BillingScopeID straight to its CloudAccount
// without re-deriving azureBillingScopeID per row. Accounts with no
// ExternalID are skipped -- they cannot own any BillingScopeID.
//
// Split out of filterAzureReservationsByScope, together with
// filterReservationsByScopeIndex below, to stay under the project's gocyclo
// threshold (mirrors validateAzureOfferingsBody's precedent).
func azureScopeIndex(accounts []config.CloudAccount) map[string]config.CloudAccount {
	index := make(map[string]config.CloudAccount, len(accounts))
	for i := range accounts {
		if accounts[i].ExternalID == "" {
			continue
		}
		index[strings.ToLower(azureBillingScopeID(accounts[i].ExternalID))] = accounts[i]
	}
	return index
}

// filterReservationsByScopeIndex narrows reservations down to the rows whose
// BillingScopeID resolves, via scopeToAccount, to a CloudAccount the session
// scope allows (AccountScope.Allows). Fails closed: a reservation with no
// BillingScopeID, or one that resolves to no registered CloudAccount, is
// dropped rather than kept.
func filterReservationsByScopeIndex(reservations []azurecompute.ExchangeableReservation, scopeToAccount map[string]config.CloudAccount, allowed auth.AccountScope) []azurecompute.ExchangeableReservation {
	filtered := make([]azurecompute.ExchangeableReservation, 0, len(reservations))
	for i := range reservations {
		r := reservations[i]
		if r.BillingScopeID == "" {
			continue
		}
		account, ok := scopeToAccount[strings.ToLower(r.BillingScopeID)]
		if !ok {
			continue
		}
		if allowed.Allows(account.ID, account.Name) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// maxAzureExchangeItems caps the number of sources/targets accepted per
// Azure exchange request, guarding against an oversized request fanning out
// into an enormous CalculateExchange payload.
const maxAzureExchangeItems = 50

// AzureExchangeSourceBody is one source reservation entry in an Azure
// compatible-offerings or execute request body.
type AzureExchangeSourceBody struct {
	ReservationID string `json:"reservation_id"`
	Quantity      int32  `json:"quantity"`
}

// AzureExchangeTargetBody is one target entry. Term is the ISO 8601
// reservation term string ("P1Y", "P3Y", ...); azureReservationTermFromString
// validates it against the SDK's typed enum rather than accepting anything
// the caller sends.
type AzureExchangeTargetBody struct {
	SKU      string `json:"sku"`
	Location string `json:"location"`
	Term     string `json:"term"`
	Quantity int32  `json:"quantity"`

	// BillingScopeID is optional and is NOT the scope that gets charged:
	// the handler always derives that from the request's authorized
	// subscription_id (azureBillingScopeID), matching every other Azure
	// reservation purchase path in this repo. When supplied it must match
	// the derived scope, so a caller cannot direct the charge at a
	// different subscription than the one their permission constraints
	// were evaluated against.
	BillingScopeID string `json:"billing_scope_id,omitempty"`
}

// AzureCompatibleOfferingsRequestBody is the request body for the
// compatible-offerings endpoint.
type AzureCompatibleOfferingsRequestBody struct {
	SubscriptionID string                    `json:"subscription_id"`
	Sources        []AzureExchangeSourceBody `json:"sources"`
	Targets        []AzureExchangeTargetBody `json:"targets"`
}

// AzureCompatibleOfferingsResponse is the response for the
// compatible-offerings endpoint: the priced candidate offerings plus the
// preview (including the SessionID a subsequent execute call would need,
// though execute never trusts a client-supplied session -- see
// executeAzureExchange).
type AzureCompatibleOfferingsResponse struct {
	Offerings []azurecompute.CompatibleOffering `json:"offerings"`
	Preview   *azurecompute.ExchangePreview     `json:"preview"`
}

// AzureExecuteExchangeRequestBody is the request body for the execute
// endpoint. MaxPaymentDue + Currency are mandatory safety guardrails: the
// handler refuses to execute an exchange whose fresh quote exceeds the cap
// or is denominated in a different currency.
type AzureExecuteExchangeRequestBody struct {
	SubscriptionID string                    `json:"subscription_id"`
	Sources        []AzureExchangeSourceBody `json:"sources"`
	Targets        []AzureExchangeTargetBody `json:"targets"`
	MaxPaymentDue  string                    `json:"max_payment_due"`
	Currency       string                    `json:"currency"`
}

// AzureExecuteExchangeResponse is the response from a successfully executed
// Azure exchange.
type AzureExecuteExchangeResponse struct {
	SessionID          string   `json:"session_id"`
	Status             string   `json:"status"`
	NetPayable         *float64 `json:"net_payable"`
	NetPayableCurrency string   `json:"net_payable_currency,omitempty"`
	RefundsTotal       *float64 `json:"refunds_total"`
	PurchasesTotal     *float64 `json:"purchases_total"`
}

// azureReservationTermFromString converts the HTTP-layer term string to the
// typed SDK enum, rejecting anything outside armreservations'
// PossibleReservationTermValues(). No fallback: an unrecognized term is a
// 400, never silently coerced to a default term (feedback_sdk_enum_string_literals).
func azureReservationTermFromString(s string) (armreservations.ReservationTerm, error) {
	term := armreservations.ReservationTerm(s)
	for _, t := range armreservations.PossibleReservationTermValues() {
		if t == term {
			return term, nil
		}
	}
	return "", fmt.Errorf("unsupported term %q", s)
}

// validateAzureExchangeSources checks the shared sources[] shape for both
// the offerings and execute request bodies.
func validateAzureExchangeSources(sources []AzureExchangeSourceBody) error {
	if len(sources) == 0 {
		return NewClientError(400, "sources is required")
	}
	if len(sources) > maxAzureExchangeItems {
		return NewClientError(400, fmt.Sprintf("sources exceeds the maximum of %d items", maxAzureExchangeItems))
	}
	for i, s := range sources {
		if s.ReservationID == "" {
			return NewClientError(400, fmt.Sprintf("sources[%d].reservation_id is required", i))
		}
		if s.Quantity < 1 {
			return NewClientError(400, fmt.Sprintf("sources[%d].quantity must be >= 1", i))
		}
	}
	return nil
}

// azureBillingScopeID returns the ARM billing scope that a purchase against
// subscriptionID is charged to.
//
// The billing scope is always derived from the request's subscription_id,
// never accepted from the caller. Every other Azure reservation purchase
// path in this repo does the same (ComputeClient.buildReservationBody and
// the database / cache / search / cosmosdb / synapse / managedredis
// clients all build "/subscriptions/{their own subscriptionID}"). It also
// keeps the charge inside the scope authorization actually checked: the
// execute:ri-exchange AccountIDs constraint is evaluated against the
// CloudAccount registered for subscription_id, so letting a caller name a
// different billing scope would move the money outside the account whose
// constraints were verified.
func azureBillingScopeID(subscriptionID string) string {
	return "/subscriptions/" + subscriptionID
}

// validateAzureExchangeTargets checks the shared targets[] shape for both
// the offerings and execute request bodies. subscriptionID is the already-
// validated request subscription; a target may omit billing_scope_id
// entirely, but may not name a scope other than that subscription's.
func validateAzureExchangeTargets(targets []AzureExchangeTargetBody, subscriptionID string) error {
	if len(targets) == 0 {
		return NewClientError(400, "targets is required")
	}
	if len(targets) > maxAzureExchangeItems {
		return NewClientError(400, fmt.Sprintf("targets exceeds the maximum of %d items", maxAzureExchangeItems))
	}
	scope := azureBillingScopeID(subscriptionID)
	for i, t := range targets {
		if t.SKU == "" {
			return NewClientError(400, fmt.Sprintf("targets[%d].sku is required", i))
		}
		// Blank-but-not-empty is rejected too: targetLocations trims before
		// it builds the Regions constraint, so a whitespace-only location
		// would otherwise reach the permission check as "" -- a region no
		// permission can name.
		if strings.TrimSpace(t.Location) == "" {
			return NewClientError(400, fmt.Sprintf("targets[%d].location is required", i))
		}
		if t.BillingScopeID != "" && !strings.EqualFold(t.BillingScopeID, scope) {
			return NewClientError(400, fmt.Sprintf(
				"targets[%d].billing_scope_id %q is not the billing scope of subscription %q; omit it to charge the subscription's own scope",
				i, t.BillingScopeID, subscriptionID))
		}
		if t.Quantity < 1 {
			return NewClientError(400, fmt.Sprintf("targets[%d].quantity must be >= 1", i))
		}
		if _, err := azureReservationTermFromString(t.Term); err != nil {
			return NewClientError(400, fmt.Sprintf("targets[%d].term: %v", i, err))
		}
	}
	return nil
}

// validateAzureOfferingsBody validates the compatible-offerings request
// body. Extracted so getAzureCompatibleOfferings and
// validateAzureExecuteBody share the same check without exceeding the
// gocyclo threshold (mirrors validateExecuteExchangeBody's precedent for
// the AWS execute handler).
func validateAzureOfferingsBody(body AzureCompatibleOfferingsRequestBody) error {
	if body.SubscriptionID == "" {
		return NewClientError(400, "subscription_id is required")
	}
	if err := validateAzureExchangeSources(body.Sources); err != nil {
		return err
	}
	return validateAzureExchangeTargets(body.Targets, body.SubscriptionID)
}

// validateAzureExecuteBody validates the execute request body: the shared
// offerings validation plus the mandatory spend-cap and currency guardrails.
func validateAzureExecuteBody(body AzureExecuteExchangeRequestBody) error {
	if err := validateAzureOfferingsBody(AzureCompatibleOfferingsRequestBody{
		SubscriptionID: body.SubscriptionID,
		Sources:        body.Sources,
		Targets:        body.Targets,
	}); err != nil {
		return err
	}
	if body.MaxPaymentDue == "" {
		return NewClientError(400, "max_payment_due is required as a safety guardrail")
	}
	if body.Currency == "" {
		return NewClientError(400, "currency is required")
	}
	return nil
}

// toAzureExchangeSources converts the HTTP-shaped sources into the
// provider-layer shape. Pure field mapping; validateAzureExchangeSources
// must be called first.
func toAzureExchangeSources(sources []AzureExchangeSourceBody) []azurecompute.ExchangeableReservation {
	out := make([]azurecompute.ExchangeableReservation, len(sources))
	for i, s := range sources {
		out[i] = azurecompute.ExchangeableReservation{ReservationID: s.ReservationID, Quantity: s.Quantity}
	}
	return out
}

// toAzureExchangeTargets converts the HTTP-shaped targets into the
// provider-layer shape, re-parsing the term string and deriving each
// target's billing scope from subscriptionID rather than from the request
// body (see azureBillingScopeID). validateAzureExchangeTargets must be
// called first; a term error here indicates an internal invariant break
// rather than a fresh client mistake.
func toAzureExchangeTargets(targets []AzureExchangeTargetBody, subscriptionID string) ([]azurecompute.ExchangeTarget, error) {
	out := make([]azurecompute.ExchangeTarget, len(targets))
	scope := azureBillingScopeID(subscriptionID)
	for i, t := range targets {
		term, err := azureReservationTermFromString(t.Term)
		if err != nil {
			return nil, fmt.Errorf("targets[%d]: %w", i, err)
		}
		out[i] = azurecompute.ExchangeTarget{
			SKU:            t.SKU,
			Location:       t.Location,
			Term:           term,
			Quantity:       t.Quantity,
			BillingScopeID: scope,
		}
	}
	return out, nil
}

// targetLocations returns the de-duplicated, canonically lower-cased set of
// target locations, used to populate the Regions dimension of the
// execute:ri-exchange constraint check. Callers must have already validated
// that every target has a non-blank Location.
//
// Azure treats location names case-insensitively and its own APIs return the
// lower-case form, so lower case is the canonical spelling. Normalizing
// before the dedup collapses "EastUS" -- the casing the Azure portal
// displays -- and "eastus" into the single region they actually are, so the
// constraint set names each target region exactly once rather than demanding
// a permission for two spellings of one place.
//
// auth.matchAllRegionsConstraint compares case-insensitively as well, so a
// permission stored in either casing matches either way. Normalizing here
// keeps the two layers agreeing on what a region is instead of leaving the
// constraint set's accuracy resting on the matcher's leniency.
//
// toAzureExchangeTargets sends Azure the raw, un-normalized Location. That
// is not a gap: Azure resolves either casing to the same region, so the
// region authorized here is the region the exchange lands in.
func targetLocations(targets []AzureExchangeTargetBody) []string {
	locations := make([]string, len(targets))
	for i, t := range targets {
		locations[i] = t.Location
	}
	return normalizeRegions(locations)
}

// normalizeRegions canonically lower-cases and trims region names and
// de-duplicates them, preserving first-seen order. Shared by targetLocations
// and exchangeRegions so both sides of an exchange are spelled the same way
// before they reach auth.matchAllRegionsConstraint.
func normalizeRegions(regions []string) []string {
	seen := make(map[string]bool, len(regions))
	out := make([]string, 0, len(regions))
	for _, r := range regions {
		region := strings.ToLower(strings.TrimSpace(r))
		if !seen[region] {
			seen[region] = true
			out = append(out, region)
		}
	}
	return out
}

// unknownRegionConstraint is the Regions entry substituted for a source
// reservation whose region Azure did not report (ExchangeableReservation.Region
// "may be empty for reservations with AppliedScopeType == Shared"), or that is
// absent from the tenant listing altogether.
//
// It is deliberately not a region name: no Azure location is spelled this way,
// so a permission carrying ANY Regions constraint cannot name it and the
// exchange is denied. A permission with NO Regions constraint is unaffected
// (auth.matchAllRegionsConstraint treats an empty permission list as "no
// restriction"), so callers who were never region-scoped are not penalized for
// a reservation Azure described incompletely.
//
// The alternative -- dropping an unreported region from the set -- would make
// "Azure did not tell us where this is" mean "unconstrained", which is exactly
// the fail-open shape of the empty-region defect fixed in PR #1495. Same
// posture as unattributedAccountConstraint on the AccountIDs dimension.
const unknownRegionConstraint = "unknown-region"

// exchangeRegions returns every region an Azure exchange touches: each
// target's location AND each source reservation's own region, normalized and
// de-duplicated into one set.
//
// Both halves belong in the Regions dimension because an exchange mutates
// both. The sources are handed back to Azure and their commitment value is
// consumed; the targets are acquired. Constraining only the targets (the
// pre-fix behavior) let a caller permitted solely in eastus name a
// westeurope reservation as the source of an eastus-targeted exchange: the
// Regions dimension saw only "eastus", requireAzureSourceOwnership keys on the
// reservation's BillingScopeID rather than its region, and no other gate
// consults a source's region at all. The westeurope commitment -- which the
// caller was never authorized to touch -- was consumed and relocated,
// irreversibly.
//
// Source regions come from the tenant listing (owned) rather than the request
// body, because the request names only reservation IDs; Azure is the authority
// on where a reservation lives. A source missing from the listing, or one
// whose Region Azure left empty, contributes unknownRegionConstraint rather
// than nothing -- see that constant for why.
//
// On the execute path requireAzureSourceOwnership has already refused any
// source absent from the listing by the time this runs, so the sentinel there
// means specifically "owned, but Azure reported no region". The missing-source
// branch is kept as a fail-closed default for any future caller that reaches
// this function without that guarantee.
func exchangeRegions(targets []AzureExchangeTargetBody, sources []AzureExchangeSourceBody, owned []azurecompute.ExchangeableReservation) []string {
	regionByID := make(map[string]string, len(owned))
	for i := range owned {
		regionByID[strings.ToLower(owned[i].ReservationID)] = owned[i].Region
	}

	regions := targetLocations(targets)
	for _, s := range sources {
		region := strings.ToLower(strings.TrimSpace(regionByID[strings.ToLower(s.ReservationID)]))
		if region == "" {
			region = unknownRegionConstraint
		}
		regions = append(regions, region)
	}
	return normalizeRegions(regions)
}

// requireAzureSubscriptionScope enforces the session's allowed_accounts
// scope (issue #1030) against the CloudAccount registered for
// subscriptionID, the same per-account gate the sibling /ri-exchange
// endpoints apply. Without it, subscription_id is a caller-controlled
// pointer at any subscription in the tenant: a user scoped to one account
// could price, and with an otherwise-unconstrained execute:ri-exchange
// permission execute, an exchange against another account's subscription.
// The per-permission Constraints check does not cover this -- it only
// consults the permission's own AccountIDs, never the user's
// allowed_accounts.
//
// Returns errNotFound (404, not 403) when a scoped session names a
// subscription outside its scope, including one with no registered account
// at all, matching requireAccountAccess: a user must not be able to probe
// which subscriptions exist outside their scope.
//
// Unrestricted / admin sessions short-circuit before the account fetch,
// mirroring requireExecutionAccess.
func (h *Handler) requireAzureSubscriptionScope(ctx context.Context, session *Session, subscriptionID string) error {
	allowed, err := h.getAccountScope(ctx, session)
	if err != nil {
		return fmt.Errorf("failed to get allowed accounts: %w", err)
	}
	if allowed.AllowsAll() {
		return nil
	}
	account, err := h.config.GetCloudAccountByExternalID(ctx, "azure", subscriptionID)
	if err != nil {
		return fmt.Errorf("failed to resolve cloud account scope: %w", err)
	}
	if account == nil || !allowed.Allows(account.ID, account.Name) {
		return errNotFound
	}
	return nil
}

// requireAzureSourceOwnership refuses any source reservation that is not
// paid for by the authorized subscription (issue #1527).
//
// Every other gate on these endpoints constrains the DESTINATION of an
// exchange: requireAzureSubscriptionScope and the execute:ri-exchange
// AccountIDs constraint both key off subscription_id, and
// toAzureExchangeTargets forces each target's billing scope to that same
// subscription. The sources were unconstrained. That matters because Azure
// reservation orders are tenant-scoped, not subscription-scoped --
// ListExchangeableReservations enumerates the whole tenant precisely
// because "the Azure Capacity exchange API operates on reservation order
// IDs which span subscriptions". So a caller authorized for subscription A
// could name subscription B's reservation IDs and hand B's commitments
// back, with the replacement purchased into A's billing scope. Azure RBAC
// on the reservation order was the only backstop.
//
// The check uses each reservation's own BillingScopeID -- the subscription
// Azure charges for it, and the scope an exchange refunds it to. That is
// the correct discriminator even for AppliedScopeType == Shared, which
// governs which subscriptions receive the discount rather than which one
// paid; an AppliedScopes-based check would pass for nearly every
// reservation and be security theater.
//
// Fails closed on every uncertainty: a reservation absent from the listing,
// or one Azure reports without a billing scope, is refused rather than
// allowed. Denials deliberately do not distinguish "does not exist" from
// "belongs to someone else", so this cannot be used to enumerate another
// subscription's reservation IDs (same posture as
// requireAzureSubscriptionScope).
func requireAzureSourceOwnership(owned []azurecompute.ExchangeableReservation, sources []AzureExchangeSourceBody, subscriptionID string) error {
	scope := azureBillingScopeID(subscriptionID)
	byID := make(map[string]string, len(owned))
	for i := range owned {
		byID[strings.ToLower(owned[i].ReservationID)] = owned[i].BillingScopeID
	}
	for i, s := range sources {
		billingScope, found := byID[strings.ToLower(s.ReservationID)]
		if !found || billingScope == "" || !strings.EqualFold(billingScope, scope) {
			return NewClientError(403, fmt.Sprintf(
				"sources[%d].reservation_id is not a reservation billed to subscription %q; an exchange may only hand back reservations that subscription paid for",
				i, subscriptionID))
		}
	}
	return nil
}

// checkAzureSourceOwnership fetches the caller's visible reservations and
// applies requireAzureSourceOwnership. Split from the pure check so the
// authorization rule itself is testable without a client, and so both the
// pricing and execute endpoints share one code path.
func checkAzureSourceOwnership(ctx context.Context, client azureExchangeClient, sources []AzureExchangeSourceBody, subscriptionID string) error {
	owned, err := listOwnedAzureReservations(ctx, client)
	if err != nil {
		return err
	}
	return requireAzureSourceOwnership(owned, sources, subscriptionID)
}

// listOwnedAzureReservations fetches the tenant-wide reservation listing that
// both source-side gates consult: requireAzureSourceOwnership (which
// subscription paid for each source) and exchangeRegions (where each source
// lives). Extracted so the execute path can fetch it once and feed both,
// rather than listing twice and risking the two gates disagreeing.
//
// Fails closed: without the listing we can establish neither ownership nor
// region, and permitting the exchange would restore the very gaps those
// checks exist to close. 502 rather than 500 because the upstream dependency,
// not this service, is what failed.
func listOwnedAzureReservations(ctx context.Context, client azureExchangeClient) ([]azurecompute.ExchangeableReservation, error) {
	owned, err := client.ListExchangeableReservations(ctx)
	if err != nil {
		logging.Errorf("azure exchange source ownership lookup failed: %v", err)
		return nil, NewClientError(502, "could not verify which subscription owns the requested reservations; refusing to proceed")
	}
	return owned, nil
}

// getAzureCompatibleOfferings prices a proposed Azure RI exchange and
// returns the compatible offerings Azure is willing to accept plus the cost
// preview, without committing anything. Requires "view:purchases" permission
// plus allowed_accounts scope over the requested subscription, mirroring the
// AWS quote endpoint.
//
// POST /api/ri-exchange/azure-instances/compatible-offerings.
func (h *Handler) getAzureCompatibleOfferings(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	var body AzureCompatibleOfferingsRequestBody
	if err = json.Unmarshal([]byte(req.Body), &body); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}
	if validateErr := validateAzureOfferingsBody(body); validateErr != nil {
		return nil, validateErr
	}
	if scopeErr := h.requireAzureSubscriptionScope(ctx, session, body.SubscriptionID); scopeErr != nil {
		return nil, scopeErr
	}

	client, err := h.buildAzureExchangeClient(ctx, body.SubscriptionID)
	if err != nil {
		return nil, fmt.Errorf("failed to build Azure exchange client: %w", err)
	}
	if client == nil {
		return nil, NewClientError(404, fmt.Sprintf("no Azure account registered for subscription %q", body.SubscriptionID))
	}

	if ownErr := checkAzureSourceOwnership(ctx, client, body.Sources, body.SubscriptionID); ownErr != nil {
		return nil, ownErr
	}

	targets, err := toAzureExchangeTargets(body.Targets, body.SubscriptionID)
	if err != nil {
		return nil, err
	}

	preview, offerings, err := client.CalculateExchange(ctx, toAzureExchangeSources(body.Sources), targets)
	if err != nil {
		logging.Errorf("azure compatible offerings failed: %v", err)
		return nil, mapAzureExchangeError("failed to find compatible offerings", err)
	}

	return &AzureCompatibleOfferingsResponse{Offerings: offerings, Preview: preview}, nil
}

// azureMaxPurchaseAmountCurrency is the currency the execute:ri-exchange
// permission's MaxPurchaseAmount constraint is denominated in, matching the
// AWS execute:ri-exchange precedent (which takes max_payment_due_usd). There
// is no FX conversion available here, so a non-USD exchange's raw amount can
// never be safely compared against a USD-denominated cap -- see
// checkAzureExecuteConstraints.
const azureMaxPurchaseAmountCurrency = "USD"

// authorizeAzureExchangeExecution builds the Azure exchange client for the
// request's subscription, refuses sources the subscription does not own
// (issue #1527), and enforces the per-permission Constraints configured on
// execute:ri-exchange (SEC-01, issue #1141). Extracted from
// executeAzureExchange to keep that function under the gocyclo limit.
//
// Order matters, and all three gates run before any pricing or commit call:
//
//   - The tenant listing is fetched before the constraint check because the
//     Regions dimension cannot be assembled without knowing where the sources
//     live (exchangeRegions). An unavailable listing therefore refuses with
//     502 ahead of any constraint denial. The caller has already cleared
//     requirePermission("execute", "ri-exchange") and the allowed_accounts
//     scope for this subscription by then, so the read-only listing call is
//     within what they are authorized to trigger.
//
//   - Ownership is checked before the constraint check so the two cannot form
//     an enumeration oracle. If the constraint check ran first, a caller
//     scoped to subscription A and permitted only in eastus would get
//     distinguishable answers for a reservation id they do not own: an id
//     that exists in eastus (owned by subscription B) would clear the Regions
//     dimension and be refused by the ownership gate, while an id that does
//     not exist -- or lives in an unpermitted region -- would be refused by
//     the constraint check with a different message. That difference confirms
//     "this reservation id exists, in one of my permitted regions, in a
//     subscription I am not scoped to". requireAzureSourceOwnership
//     deliberately makes its own denials indistinguishable; running it first
//     keeps the whole path that way.
//
//     It also sharpens exchangeRegions: every source reaching it is
//     known-owned, so unknownRegionConstraint means only "owned, but Azure
//     reported no region" rather than doubling as "not yours" or "not real".
func (h *Handler) authorizeAzureExchangeExecution(ctx context.Context, session *Session, body AzureExecuteExchangeRequestBody, maxRat *big.Rat) (azureExchangeClient, error) {
	// Scope check MUST precede building the client (mirrors
	// getAzureCompatibleOfferings): otherwise an unregistered subscription
	// (distinguishable 404: "no Azure account registered...") and a
	// registered-but-out-of-scope one (generic errNotFound) would leak an
	// enumeration signal to a scoped caller about which subscriptions exist,
	// and credentials for an out-of-scope account could be resolved before
	// the denial.
	if scopeErr := h.requireAzureSubscriptionScope(ctx, session, body.SubscriptionID); scopeErr != nil {
		return nil, scopeErr
	}

	client, err := h.buildAzureExchangeClient(ctx, body.SubscriptionID)
	if err != nil {
		return nil, fmt.Errorf("failed to build Azure exchange client: %w", err)
	}
	if client == nil {
		return nil, NewClientError(404, fmt.Sprintf("no Azure account registered for subscription %q", body.SubscriptionID))
	}

	owned, err := listOwnedAzureReservations(ctx, client)
	if err != nil {
		return nil, err
	}

	if ownErr := requireAzureSourceOwnership(owned, body.Sources, body.SubscriptionID); ownErr != nil {
		return nil, ownErr
	}

	accountID, err := h.resolveAzureExchangeAccountID(ctx, body.SubscriptionID)
	if err != nil {
		return nil, err
	}

	if err := h.checkAzureExecuteConstraints(ctx, session, body, accountID, maxRat, exchangeRegions(body.Targets, body.Sources, owned)); err != nil {
		return nil, err
	}
	return client, nil
}

// resolveAzureExchangeAccountID looks up the CloudAccount registered for
// subscriptionID and returns its ID, or unattributedAccountConstraint when
// no account is registered (so an AccountIDs-constrained permission still
// fails closed against an unattributed request). Extracted from
// authorizeAzureExchangeExecution to keep that function under the gocyclo
// limit.
func (h *Handler) resolveAzureExchangeAccountID(ctx context.Context, subscriptionID string) (string, error) {
	account, err := h.config.GetCloudAccountByExternalID(ctx, "azure", subscriptionID)
	if err != nil {
		return "", fmt.Errorf("failed to resolve cloud account scope: %w", err)
	}
	if account != nil {
		return account.ID, nil
	}
	return unattributedAccountConstraint, nil
}

// checkAzureExecuteConstraints enforces the execute:ri-exchange permission
// Constraints (SEC-01, issue #1141): AccountIDs from the resolved
// CloudAccount, Providers/Services fixed to azure/compute, Regions covering
// every region the exchange touches (see exchangeRegions -- both the target
// locations and the source reservations' own regions), and MaxPurchaseAmount
// from the caller's cap.
//
// MaxPurchaseAmount is USD-denominated (azureMaxPurchaseAmountCurrency) with
// no FX conversion available. A non-USD request's raw amount is therefore
// never compared directly against the cap -- doing so would let a large
// non-USD amount (e.g. 1000 KWD, worth far more than 1000 USD) clear a cap
// meant to bound USD spend. Instead, a non-USD request is checked with an
// unmatchable sentinel amount (math.MaxFloat64): this denies the request if
// the granting permission carries ANY MaxPurchaseAmount constraint (fail
// closed on a cap this code cannot safely evaluate) while still allowing it
// through when the permission has no amount constraint at all -- callers
// without a spend cap are not penalized for using a non-USD subscription.
//
// When the sentinel check fails, a second call with the amount dimension
// neutralized (MaxPurchaseAmount: 0, which matchPurchaseAmountConstraint
// always treats as satisfied) disambiguates the cause: if that second call
// still fails, some other dimension (account/provider/service/region) is
// the real reason and its error is returned unchanged; otherwise the amount
// constraint was specifically the blocker and a currency-specific 403 is
// returned instead of the generic constraint-denied message.
func (h *Handler) checkAzureExecuteConstraints(ctx context.Context, session *Session, body AzureExecuteExchangeRequestBody, accountID string, maxRat *big.Rat, regions []string) error {
	base := auth.PermissionConstraints{
		AccountIDs: []string{accountID},
		Providers:  []string{string(common.ProviderAzure)},
		Services:   []string{string(common.ServiceCompute)},
		Regions:    regions,
	}

	isUSD := strings.EqualFold(body.Currency, azureMaxPurchaseAmountCurrency)
	attempt := base
	if isUSD {
		maxPayment, _ := maxRat.Float64()
		attempt.MaxPurchaseAmount = maxPayment
	} else {
		attempt.MaxPurchaseAmount = math.MaxFloat64
	}

	err := h.requirePermissionConstraints(ctx, session, "ri-exchange", []auth.PermissionConstraints{attempt})
	if err == nil || isUSD {
		return err
	}

	// Non-USD and denied: isolate whether the amount dimension was
	// specifically the cause.
	withoutAmount := base
	withoutAmount.MaxPurchaseAmount = 0
	if otherErr := h.requirePermissionConstraints(ctx, session, "ri-exchange", []auth.PermissionConstraints{withoutAmount}); otherErr != nil {
		return otherErr
	}
	return NewClientError(403, fmt.Sprintf(
		"your execute:ri-exchange permission has a spend-cap (MaxPurchaseAmount) constraint, which is USD-denominated and cannot be safely enforced against a %s exchange; use a USD-denominated request or ask an administrator to remove the constraint",
		body.Currency))
}

// checkAzureExchangeMoneyGuardrails enforces the money-path guardrails
// against a freshly-obtained CalculateExchange preview, before its
// SessionID is allowed to reach ExecuteExchange: non-empty policy errors
// block execution, a nil NetPayable is refused rather than treated as free,
// a currency mismatch blocks execution, and NetPayable exceeding the cap
// blocks execution. Extracted from executeAzureExchange to keep that
// function under the gocyclo limit.
//
// A nil preview is itself refused rather than dereferenced: the current
// azureExchangeClient.CalculateExchange contract never returns (nil, nil,
// nil), but a future implementation of the interface making that mistake
// must not panic this handler.
func checkAzureExchangeMoneyGuardrails(preview *azurecompute.ExchangePreview, maxRat *big.Rat, currency string) error {
	if preview == nil {
		return fmt.Errorf("internal error: CalculateExchange returned a nil preview")
	}
	if len(preview.PolicyErrors) > 0 {
		return NewClientError(422, fmt.Sprintf("Azure rejected this exchange: %s", strings.Join(preview.PolicyErrors, "; ")))
	}
	if preview.NetPayable == nil {
		return NewClientError(422, "Azure did not return a net payable amount; refusing to execute")
	}
	// Case-insensitive to match the isUSD test in checkAzureExecuteConstraints:
	// a request of "usd" must not clear the USD-denominated cap check there
	// and then be rejected here as a mismatch against Azure's "USD".
	if !strings.EqualFold(preview.NetPayableCurrency, currency) {
		return NewClientError(422, fmt.Sprintf("quoted currency %q does not match requested currency %q", preview.NetPayableCurrency, currency))
	}
	netPayableRat := new(big.Rat).SetFloat64(*preview.NetPayable)
	if netPayableRat == nil {
		return fmt.Errorf("internal error: quoted net payable %v is not a finite number", *preview.NetPayable)
	}
	if netPayableRat.Cmp(maxRat) > 0 {
		return NewClientError(422, fmt.Sprintf("quoted net payable %s %s exceeds max_payment_due %s %s",
			netPayableRat.FloatString(2), currency, maxRat.FloatString(2), currency))
	}
	return nil
}

// parseAzureExecuteRequest decodes and validates an execute request body and
// parses its spend cap into an exact rational.
//
// Extracted from executeAzureExchange purely to keep that function within
// the project's gocyclo limit once the issue #1527 source-ownership gate was
// added; it makes no decisions of its own beyond returning the same errors
// inline code did.
func parseAzureExecuteRequest(rawBody string) (AzureExecuteExchangeRequestBody, *big.Rat, error) {
	var body AzureExecuteExchangeRequestBody
	if err := json.Unmarshal([]byte(rawBody), &body); err != nil {
		return body, nil, NewClientError(400, "invalid request body")
	}
	if err := validateAzureExecuteBody(body); err != nil {
		return body, nil, err
	}
	maxRat, err := exchange.ParseDecimalRat(body.MaxPaymentDue)
	if err != nil {
		return body, nil, NewClientError(400, fmt.Sprintf("invalid max_payment_due: %v", err))
	}
	return body, maxRat, nil
}

// executeAzureExchange executes an Azure RI exchange with mandatory
// spend-cap and currency guardrails. Requires "execute:ri-exchange"
// (deliberately separate from execute:purchases), mirroring the AWS
// executeExchange handler: RI exchanges are financially irreversible once
// submitted.
//
// Unlike a design that executes a client-supplied session_id, this handler
// never trusts the caller's own pricing: it re-runs CalculateExchange itself
// against the caller's sources/targets, validates the FRESH quote against
// every guardrail in checkAzureExchangeMoneyGuardrails, and only then calls
// ExecuteExchange with the SessionID *that fresh call returned*. A
// client-supplied or stale session would bypass every guardrail below, so
// the server always re-quotes immediately before committing.
//
// POST /api/ri-exchange/azure-instances/exchange.
func (h *Handler) executeAzureExchange(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	session, err := h.requirePermission(ctx, req, "execute", "ri-exchange")
	if err != nil {
		return nil, err
	}

	body, maxRat, err := parseAzureExecuteRequest(req.Body)
	if err != nil {
		return nil, err
	}

	// authorizeAzureExchangeExecution applies every gate: allowed_accounts
	// scope, source ownership (issue #1527) and the execute:ri-exchange
	// Constraints, all before the pricing call below.
	client, err := h.authorizeAzureExchangeExecution(ctx, session, body, maxRat)
	if err != nil {
		return nil, err
	}

	targets, err := toAzureExchangeTargets(body.Targets, body.SubscriptionID)
	if err != nil {
		return nil, err
	}

	preview, _, err := client.CalculateExchange(ctx, toAzureExchangeSources(body.Sources), targets)
	if err != nil {
		logging.Errorf("azure exchange re-quote failed: %v", err)
		return nil, mapAzureExchangeError("failed to price the exchange before execution", err)
	}

	err = checkAzureExchangeMoneyGuardrails(preview, maxRat, body.Currency)
	if err != nil {
		return nil, err
	}

	result, err := client.ExecuteExchange(ctx, preview.SessionID)
	if err != nil {
		logging.Errorf("azure exchange execution failed: %v", err)
		return nil, mapAzureExchangeError("exchange execution failed", err)
	}

	logging.Infof("azure ri-exchange executed: subscription=%s session=%s status=%s", body.SubscriptionID, result.SessionID, result.Status)

	return &AzureExecuteExchangeResponse{
		SessionID:          result.SessionID,
		Status:             result.Status,
		NetPayable:         result.NetPayable,
		NetPayableCurrency: result.NetPayableCurrency,
		RefundsTotal:       preview.RefundsTotal,
		PurchasesTotal:     preview.PurchasesTotal,
	}, nil
}

// mapAzureExchangeError converts an error from an Azure RI exchange
// client-layer call to a ClientError with the appropriate HTTP status.
// Azure 4xx client faults (via isAzureClientError) produce a 400 with the
// Azure error message preserved; any other error produces a 500 using the
// opMsg fallback -- the same contract mapAWSExchangeError applies to the
// AWS exchange endpoints.
func mapAzureExchangeError(opMsg string, err error) error {
	if isAzureClientError(err) {
		return NewClientError(400, err.Error())
	}
	return NewClientError(500, opMsg)
}

// getBaseAWSConfig returns the cached base AWS config, loading it once via sync.Once.
func (h *Handler) getBaseAWSConfig(ctx context.Context) (aws.Config, error) {
	h.awsCfgOnce.Do(func() {
		h.awsCfg, h.awsCfgErr = awsconfig.LoadDefaultConfig(ctx)
	})
	return h.awsCfg, h.awsCfgErr
}

// loadAWSConfigWithRegion returns the cached base config, optionally overriding the region.
func (h *Handler) loadAWSConfigWithRegion(ctx context.Context, region string) (aws.Config, error) {
	cfg, err := h.getBaseAWSConfig(ctx)
	if err != nil {
		return aws.Config{}, err
	}
	if region != "" {
		cfg.Region = region
	}
	return cfg, nil
}

// reshapeCloudAccountInScope checks whether the session's allowed_accounts
// permit access to the deployment's registered AWS cloud account. It returns
// (true, nil) when the session is unrestricted or the cloud account matches,
// (false, nil) when the cloud account is outside the session's scope (caller
// should return an empty response), and (false, err) on any lookup failure.
// Used by listConvertibleRIs, getRIUtilization, and getReshapeRecommendations
// to eliminate duplicated account-scoping blocks.
func (h *Handler) reshapeCloudAccountInScope(ctx context.Context, session *Session) (bool, error) {
	allowed, aErr := h.getAccountScope(ctx, session)
	if aErr != nil {
		return false, fmt.Errorf("failed to get allowed accounts: %w", aErr)
	}
	if allowed.AllowsAll() {
		return true, nil
	}
	cloudAccountID, aErr := h.resolveReshapeCloudAccountID(ctx)
	if aErr != nil {
		return false, fmt.Errorf("failed to resolve cloud account scope: %w", aErr)
	}
	nameByID := h.resolveAccountNamesByID(ctx)
	return allowed.Allows(cloudAccountID, nameByID[cloudAccountID]), nil
}

// resolveReshapeCloudAccountID returns the cloud account ID for the running
// deployment, using the test-injected reshapeAccountResolver when set and
// falling back to the production resolveAWSCloudAccountID (STS-backed).
func (h *Handler) resolveReshapeCloudAccountID(ctx context.Context) (string, error) {
	if h.reshapeAccountResolver != nil {
		return h.reshapeAccountResolver(ctx)
	}
	return h.resolveAWSCloudAccountID(ctx)
}

// checkListRIsAccountIDParam enforces the ?account_id= chip filter for
// listConvertibleRIs. Returns (true, nil) when the running AWS account matches
// the requested account (or when no account_id param is given), (false, nil)
// when it does not match (caller should return an empty list), and
// (false, err) on STS failure.
func (h *Handler) checkListRIsAccountIDParam(ctx context.Context, params map[string]string) (bool, error) {
	accountID := params["account_id"]
	if accountID == "" {
		return true, nil
	}
	resolve := h.resolveAWSAccountID
	if h.riInstancesAccountResolver != nil {
		resolve = h.riInstancesAccountResolver
	}
	runningAccountID, err := resolve(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to resolve running AWS account for RI scope: %w", err)
	}
	return runningAccountID == accountID, nil
}

// listConvertibleRIs returns all active convertible Reserved Instances for
// the running AWS account.
//
// The optional ?account_id= query parameter narrows the listing to a single
// AWS account so the page honors the Main Header global account filter
// (issue #871). Convertible RIs are read from the deployment's ambient AWS
// credentials, which resolve to exactly one account number; when the chip
// selects a different account, none of these RIs belong to it, so we return
// an empty list rather than the unscoped fleet. A real STS failure fails
// closed (returns an error) instead of silently leaking the ambient account's
// RIs under another account's filter.
func (h *Handler) listConvertibleRIs(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	// Apply the session's allowed_accounts scope: a restricted user must
	// only see RIs for the deployment's registered AWS account.
	if inScope, sErr := h.reshapeCloudAccountInScope(ctx, session); sErr != nil {
		return nil, sErr
	} else if !inScope {
		return &ConvertibleRIsResponse{Instances: []ec2svc.ConvertibleRI{}}, nil
	}

	if inScope, sErr := h.checkListRIsAccountIDParam(ctx, req.QueryStringParameters); sErr != nil {
		return nil, sErr
	} else if !inScope {
		return &ConvertibleRIsResponse{Instances: []ec2svc.ConvertibleRI{}}, nil
	}

	region := req.QueryStringParameters["region"]
	cfg, err := h.loadAWSConfigWithRegion(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	ec2Client := awsprovider.NewEC2ClientDirect(cfg)
	instances, err := ec2Client.ListConvertibleReservedInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list convertible RIs: %w", err)
	}

	return &ConvertibleRIsResponse{Instances: instances}, nil
}

// getRIUtilization returns per-RI utilization from Cost Explorer.
func (h *Handler) getRIUtilization(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	// Apply the session's allowed_accounts scope.
	if inScope, sErr := h.reshapeCloudAccountInScope(ctx, session); sErr != nil {
		return nil, sErr
	} else if !inScope {
		return &RIUtilizationResponse{Utilization: []recommendations.RIUtilization{}}, nil
	}

	lookbackDays, err := parseLookbackDaysParam(req.QueryStringParameters)
	if err != nil {
		return nil, err
	}

	region := req.QueryStringParameters["region"]
	cfg, err := h.loadAWSConfigWithRegion(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}
	// Adopt the resolved region when the caller omits ?region= so the CE
	// utilization filter and the cache key both scope to the region the
	// AWS client is actually talking to (mirrors getReshapeRecommendations).
	if region == "" {
		region = cfg.Region
	}

	recsAdapter := awsprovider.NewRecommendationsClientDirect(cfg)
	fetch := func(fetchCtx context.Context, days int) ([]recommendations.RIUtilization, error) {
		return recsAdapter.GetRIUtilization(fetchCtx, days, region)
	}
	utilization, err := h.getRIUtilizationCache().getOrFetch(ctx, region, lookbackDays, riUtilizationCacheTTL, riUtilizationCacheStaleTTL, fetch)
	if err != nil {
		return nil, fmt.Errorf("failed to get RI utilization: %w", err)
	}

	return &RIUtilizationResponse{Utilization: utilization}, nil
}

// parseLookbackDaysParam parses and validates the "lookback_days" query parameter.
// Returns 30 as default when the parameter is absent.
func parseLookbackDaysParam(params map[string]string) (int, error) {
	days := params["lookback_days"]
	if days == "" {
		return 30, nil
	}
	d, err := strconv.Atoi(days)
	if err != nil || d < 1 || d > 365 {
		return 0, NewClientError(400, "lookback_days must be between 1 and 365")
	}
	return d, nil
}

// parseThresholdParam parses and validates the "threshold" query parameter.
func parseThresholdParam(params map[string]string) (float64, error) {
	t := params["threshold"]
	if t == "" {
		return 95.0, nil
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 100 {
		return 0, NewClientError(400, "threshold must be a number between 0 and 100")
	}
	return f, nil
}

// monthlyCostFromConvertibleRI computes the per-instance per-month
// effective cost from an RI's pricing fields, matching the same
// formula effectiveMonthlyCost uses for offerings:
//
//	monthly = (FixedPrice / hours_per_term + UsagePrice + recurring_hourly) × 730
//
// 730 is AWS's canonical hours-per-month constant. Returns zero when
// Duration is zero (defensive — would otherwise divide by zero).
//
// Used to populate exchange.RIInfo.MonthlyCost so the cross-family
// dollar-units pre-filter can compare against per-target offering
// costs computed with the same formula.
func monthlyCostFromConvertibleRI(ri ec2svc.ConvertibleRI) float64 {
	if ri.Duration <= 0 {
		return 0
	}
	hoursPerTerm := float64(ri.Duration) / 3600
	if hoursPerTerm <= 0 {
		return 0
	}
	return ((ri.FixedPrice / hoursPerTerm) + ri.UsagePrice + ri.RecurringHourlyAmount) * 730
}

// convertToExchangeTypes converts provider-specific types to the exchange package types.
func convertToExchangeTypes(instances []ec2svc.ConvertibleRI, utilData []recommendations.RIUtilization) ([]exchange.RIInfo, []exchange.UtilizationInfo) {
	riInfos := make([]exchange.RIInfo, len(instances))
	for i := range instances {
		inst := instances[i]
		riInfos[i] = exchange.RIInfo{
			ID:                  inst.ReservedInstanceID,
			InstanceType:        inst.InstanceType,
			InstanceCount:       inst.InstanceCount,
			OfferingClass:       "convertible",
			NormalizationFactor: inst.NormalizationFactor,
			MonthlyCost:         monthlyCostFromConvertibleRI(inst),
			CurrencyCode:        inst.CurrencyCode,
			// Plumb the AWS-reported RI duration straight through —
			// reshape's term-match guard rejects alternatives whose
			// TermSeconds differs from the source so a 3y RI never
			// surfaces as an alternative to a 1y commitment.
			TermSeconds: inst.Duration,
		}
	}

	utilInfos := make([]exchange.UtilizationInfo, len(utilData))
	for i, u := range utilData {
		utilInfos[i] = exchange.UtilizationInfo{
			RIID:               u.ReservedInstanceID,
			UtilizationPercent: u.UtilizationPercent,
		}
	}

	return riInfos, utilInfos
}

// reshapeRequestParams groups parsed query parameters for getReshapeRecommendations.
type reshapeRequestParams struct {
	region       string
	threshold    float64
	lookbackDays int
}

// parseReshapeParams parses the threshold, lookback_days, and region query
// parameters for getReshapeRecommendations in a single call, reducing the
// number of error-check branches in the handler to keep it within the
// gocyclo limit.
func parseReshapeParams(params map[string]string) (reshapeRequestParams, error) {
	threshold, err := parseThresholdParam(params)
	if err != nil {
		return reshapeRequestParams{}, err
	}
	lookbackDays, err := parseLookbackDaysParam(params)
	if err != nil {
		return reshapeRequestParams{}, err
	}
	return reshapeRequestParams{
		threshold:    threshold,
		lookbackDays: lookbackDays,
		region:       params["region"],
	}, nil
}

// getReshapeRecommendations orchestrates fetching convertible RIs + utilization
// and returns reshape recommendations.
func (h *Handler) getReshapeRecommendations(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	// Apply the session's allowed_accounts scope.
	if inScope, sErr := h.reshapeCloudAccountInScope(ctx, session); sErr != nil {
		return nil, sErr
	} else if !inScope {
		return &ReshapeRecommendationsResponse{Recommendations: []exchange.ReshapeRecommendation{}}, nil
	}

	p, err := parseReshapeParams(req.QueryStringParameters)
	if err != nil {
		return nil, err
	}

	cfg, err := h.loadAWSConfigWithRegion(ctx, p.region)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}
	// Normalize region: when the caller omits ?region=, loadAWSConfigWithRegion
	// resolves a default from the AWS SDK chain but the local string stays
	// empty — which would scope the RI utilization cache and the recs lookup
	// unscoped, leaking alternatives from other regions onto the reshape page.
	// Adopt the resolved region so every downstream consumer sees the same
	// value the AWS clients are actually talking to.
	if p.region == "" {
		p.region = cfg.Region
	}

	ec2Client := h.buildReshapeEC2Client(cfg)
	instances, err := ec2Client.ListConvertibleReservedInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list convertible RIs: %w", err)
	}

	recsAdapter := h.buildReshapeRecsClient(cfg)
	fetch := func(fetchCtx context.Context, days int) ([]recommendations.RIUtilization, error) {
		return recsAdapter.GetRIUtilization(fetchCtx, days, p.region)
	}
	utilData, err := h.getRIUtilizationCache().getOrFetch(ctx, p.region, p.lookbackDays, riUtilizationCacheTTL, riUtilizationCacheStaleTTL, fetch)
	if err != nil {
		return nil, fmt.Errorf("failed to get RI utilization: %w", err)
	}

	riInfos, utilInfos := convertToExchangeTypes(instances, utilData)
	// Cross-family alternatives are sourced from the cached AWS Cost
	// Explorer purchase recommendations table in Postgres — no per-rec
	// DescribeReservedInstancesOfferings fan-out, no hand-curated
	// peer-family allowlist. The lookup is scoped to the running AWS
	// account (when registered) so a multi-tenant deployment can't
	// surface another tenant's recs. Empty resolved account ID means
	// "no scope filter" for ambient-credentials deployments where
	// CloudAccount registration hasn't happened yet; a real ListCloudAccounts
	// error aborts the request instead of silently falling through to an
	// unscoped query that could match the wrong tenant's recs.
	cloudAccountID, err := h.resolveReshapeCloudAccountID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve cloud account scope for reshape: %w", err)
	}
	currencyCode := firstNonEmptyCurrency(instances)
	lookup := purchaseRecLookupFromStore(h.config, cloudAccountID)
	recs := exchange.AnalyzeReshapingWithRecs(ctx, riInfos, utilInfos, p.threshold, p.region, currencyCode, lookup)

	resp := &ReshapeRecommendationsResponse{Recommendations: recs}
	h.attachReshapeStaleness(ctx, resp)
	return resp, nil
}

// attachReshapeStaleness populates the RecsStaleness and RecsCollectedAt
// fields on resp from the recommendations_state table. Non-fatal: errors
// are logged and the response ships without staleness metadata so the
// reshape table itself is unaffected by a DB read-side failure.
func (h *Handler) attachReshapeStaleness(ctx context.Context, resp *ReshapeRecommendationsResponse) {
	freshness, err := h.config.GetRecommendationsFreshness(ctx)
	if err != nil {
		logging.Warnf("getReshapeRecommendations: could not check recs freshness (banner suppressed): %v", err)
		return
	}
	resp.RecsCollectedAt = freshness.LastCollectedAt
	if freshness.LastCollectedAt == nil {
		// Cold start: cache was never populated — treat as hard-stale so the
		// banner fires on a fresh deployment rather than silently hiding it.
		resp.RecsStaleness = "hard"
	} else {
		resp.RecsStaleness = classifyRecsAge(time.Since(*freshness.LastCollectedAt))
	}
}

// firstNonEmptyCurrency returns the CurrencyCode of the first RI that
// has one set, defaulting to "USD" for legacy fixtures and the common
// case. The reshape page operates on a single AWS account at a time so
// all RIs share the same currency in practice; picking the first
// populated value is sufficient and avoids a noisy mismatch panic when
// some entries are missing the field.
func firstNonEmptyCurrency(instances []ec2svc.ConvertibleRI) string {
	for _rvc := range instances {
		inst := instances[_rvc]
		if inst.CurrencyCode != "" {
			return inst.CurrencyCode
		}
	}
	return "USD"
}

// validateTargets checks each entry in targets for a non-empty, UUID-shaped
// offering_id. Extracted so both getExchangeQuote and validateExecuteExchangeBody
// share the same check without exceeding the gocyclo threshold.
func validateTargets(targets []ExchangeTargetBody) error {
	for i, t := range targets {
		if t.OfferingID == "" {
			return NewClientError(400, fmt.Sprintf("targets[%d].offering_id is required", i))
		}
		if !offeringIDPattern.MatchString(t.OfferingID) {
			return NewClientError(400, fmt.Sprintf(
				"targets[%d].offering_id %q does not look like an AWS offering UUID; "+
					"expected something like 4b2293b4-5fbc-4017-9c75-d5a9d3aa8c91 -- "+
					"did you paste an instance type by mistake?",
				i, t.OfferingID))
		}
	}
	return nil
}

// getExchangeQuote gets a quote for an RI exchange.
func (h *Handler) getExchangeQuote(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requirePermission(ctx, req, "view", "purchases"); err != nil {
		return nil, err
	}

	var body ExchangeQuoteRequestBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}
	if len(body.RIIDs) == 0 {
		return nil, NewClientError(400, "ri_ids is required")
	}
	if len(body.Targets) == 0 && body.TargetOfferingID == "" {
		return nil, NewClientError(400, "either targets[] or target_offering_id is required")
	}
	if err := validateTargets(body.Targets); err != nil {
		return nil, err
	}

	// Resolve region from the AWS SDK chain when the caller omits it,
	// matching getReshapeRecommendations. Hardcoding us-east-1 would
	// return an incorrect quote for deployments in other regions.
	cfg, err := h.loadAWSConfigWithRegion(ctx, body.Region)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}
	region := cfg.Region

	quote, err := exchange.GetExchangeQuote(ctx, exchange.ExchangeQuoteRequest{
		Region:           region,
		ReservedIDs:      body.RIIDs,
		Targets:          toExchangeTargets(body.Targets),
		TargetOfferingID: body.TargetOfferingID,
		TargetCount:      body.TargetCount,
	})
	if err != nil {
		logging.Errorf("exchange quote failed: %v", err)
		return nil, mapAWSExchangeError("exchange quote failed", err)
	}

	return quote, nil
}

// validateExecuteExchangeBody validates an unmarshalled request body
// and returns a caller-friendly 400 on the first offending field.
// Extracted from executeExchange to keep the handler below the
// cyclomatic-complexity threshold; every branch here becomes a
// separate test case so the logic stays inspectable.
func validateExecuteExchangeBody(body ExchangeExecuteRequestBody) error {
	if len(body.RIIDs) == 0 {
		return NewClientError(400, "ri_ids is required")
	}
	if len(body.Targets) == 0 && body.TargetOfferingID == "" {
		return NewClientError(400, "either targets[] or target_offering_id is required")
	}
	if err := validateTargets(body.Targets); err != nil {
		return err
	}
	if body.MaxPaymentDueUSD == "" {
		return NewClientError(400, "max_payment_due_usd is required as a safety guardrail")
	}
	// Region is required on execute: RI exchanges are region-scoped and
	// financially irreversible. Silently defaulting to us-east-1 would
	// execute the exchange in the wrong region for deployments hosted
	// elsewhere, with no way to undo the operation.
	if body.Region == "" {
		return NewClientError(400, "region is required for execute; omitting it risks exchanging RIs in the wrong region")
	}
	return nil
}

// executeExchange executes an RI exchange with a spend-cap guardrail.
// Requires execute:ri-exchange (deliberately separate from execute:purchases)
// because RI exchanges are financially irreversible once submitted to AWS.
func (h *Handler) executeExchange(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	session, err := h.requirePermission(ctx, req, "execute", "ri-exchange")
	if err != nil {
		return nil, err
	}

	var body ExchangeExecuteRequestBody
	err = json.Unmarshal([]byte(req.Body), &body)
	if err != nil {
		return nil, NewClientError(400, "invalid request body")
	}
	err = validateExecuteExchangeBody(body)
	if err != nil {
		return nil, err
	}

	maxRat, err := exchange.ParseDecimalRat(body.MaxPaymentDueUSD)
	if err != nil {
		return nil, NewClientError(400, fmt.Sprintf("invalid max_payment_due_usd: %v", err))
	}

	region := body.Region

	// Enforce the per-permission Constraints configured on the granting
	// execute:ri-exchange permission (SEC-01, issue #1141). RI exchanges
	// are AWS EC2 only and region-scoped, and operate on the RIs of the
	// deployment's own AWS account, so AccountIDs carries the registered
	// cloud account the running deployment resolves to (fail closed on a
	// resolution error; unattributedAccountConstraint when the deployment
	// maps to no registered account, so an AccountIDs-constrained
	// permission denies). The amount cap is checked against the caller's
	// max_payment_due_usd guardrail, which ExecuteExchange independently
	// enforces against the actual quoted payment due.
	maxPayment, _ := maxRat.Float64()
	cloudAccountID, err := h.resolveReshapeCloudAccountID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve cloud account scope: %w", err)
	}
	if cloudAccountID == "" {
		cloudAccountID = unattributedAccountConstraint
	}
	err = h.requirePermissionConstraints(ctx, session, "ri-exchange", []auth.PermissionConstraints{{
		AccountIDs:        []string{cloudAccountID},
		Providers:         []string{string(common.ProviderAWS)},
		Services:          []string{string(common.ServiceEC2)},
		Regions:           []string{region},
		MaxPurchaseAmount: maxPayment,
	}})
	if err != nil {
		return nil, err
	}

	exchangeID, quote, err := exchange.ExecuteExchange(ctx, exchange.ExchangeExecuteRequest{
		Region:           region,
		ReservedIDs:      body.RIIDs,
		Targets:          toExchangeTargets(body.Targets),
		TargetOfferingID: body.TargetOfferingID,
		TargetCount:      body.TargetCount,
		MaxPaymentDueUSD: maxRat,
	})
	if err != nil {
		logging.Errorf("exchange execution failed: %v", err)
		return nil, mapAWSExchangeError("exchange execution failed", err)
	}

	return &ExchangeExecuteResponse{
		ExchangeID: exchangeID,
		Quote:      quote,
	}, nil
}

// awsExchangeClientFaultCodes is the set of AWS error codes that are
// documented client faults for RI exchange operations. These map to
// 4xx responses so the caller receives the original AWS error message
// and understands it was their input that was wrong. All other AWS
// errors remain 5xx (transient / server-side).
var awsExchangeClientFaultCodes = map[string]bool{
	"InvalidOfferingId":                   true,
	"InvalidParameter":                    true,
	"ValidationError":                     true,
	"InvalidReservedInstancesId.NotFound": true,
	"InvalidInstanceID.NotFound":          true,
}

// mapAWSExchangeError converts an AWS SDK error from an RI exchange
// operation to a ClientError with the appropriate HTTP status code.
// AWS 4xx client-fault errors produce a 400 with the original AWS
// message preserved. Any other error produces a 500 (generic server
// failure) using the provided opMsg fallback.
func mapAWSExchangeError(opMsg string, err error) error {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && awsExchangeClientFaultCodes[apiErr.ErrorCode()] {
		return NewClientError(400, apiErr.ErrorMessage())
	}
	return NewClientError(500, opMsg)
}

// Response types

// ConvertibleRIsResponse holds the list of convertible RIs.
type ConvertibleRIsResponse struct {
	Instances []ec2svc.ConvertibleRI `json:"instances"`
}

// ExchangeableAzureRIsResponse holds the list of Azure VM reservations that
// are eligible for the cross-SKU/cross-region exchange flow.
type ExchangeableAzureRIsResponse struct {
	Reservations []azurecompute.ExchangeableReservation `json:"reservations"`
}

// RIUtilizationResponse holds per-RI utilization data.
type RIUtilizationResponse struct {
	Utilization []recommendations.RIUtilization `json:"utilization"`
}

// ReshapeRecommendationsResponse holds reshape recommendations.
//
// RecsStaleness is empty when the underlying Cost Explorer cache is
// fresh, "soft" when it is older than reshapeSoftStaleThreshold (12 h),
// and "hard" when it is older than reshapeHardStaleThreshold (24 h).
// RecsCollectedAt carries the raw timestamp so the frontend can build
// its own relative-time label ("last collected 23h ago").
type ReshapeRecommendationsResponse struct {
	RecsCollectedAt *time.Time                       `json:"recs_collected_at,omitempty"`
	RecsStaleness   string                           `json:"recs_staleness,omitempty"`
	Recommendations []exchange.ReshapeRecommendation `json:"recommendations"`
}

// reshapeSoftStaleThreshold is the age at which the reshape recs banner
// transitions to "soft" warning: data may be up to 12 h old.
const reshapeSoftStaleThreshold = 12 * time.Hour

// reshapeHardStaleThreshold is the age at which the reshape recs banner
// transitions to "hard" warning: data is more than 24 h old.
const reshapeHardStaleThreshold = 24 * time.Hour

// classifyRecsAge maps a data age to the staleness label surfaced in
// ReshapeRecommendationsResponse.RecsStaleness. The zero duration
// (cold-cache path: no LastCollectedAt) is treated as "hard" so the
// banner fires on a fresh deployment rather than silently hiding it.
func classifyRecsAge(age time.Duration) string {
	switch {
	case age >= reshapeHardStaleThreshold:
		return "hard"
	case age >= reshapeSoftStaleThreshold:
		return "soft"
	default:
		return ""
	}
}

// ExchangeTargetBody is one entry in an ExchangeQuote/Execute request's
// `targets` array. Mirrors pkg/exchange.TargetConfig but with JSON tags
// shaped for the HTTP surface.
type ExchangeTargetBody struct {
	OfferingID string `json:"offering_id"`
	Count      int32  `json:"count"`
}

// ExchangeQuoteRequestBody is the request body for the quote endpoint.
// Callers may supply either the new `targets` array (preferred) or the
// legacy `target_offering_id` + `target_count` singleton fields. When
// both are present, `targets` wins. Exactly one of them must be
// provided (or the handler returns 400).
type ExchangeQuoteRequestBody struct {
	TargetOfferingID string               `json:"target_offering_id,omitempty"`
	Region           string               `json:"region,omitempty"`
	RIIDs            []string             `json:"ri_ids"`
	Targets          []ExchangeTargetBody `json:"targets,omitempty"`
	TargetCount      int32                `json:"target_count,omitempty"`
}

// ExchangeExecuteRequestBody is the request body for the execute endpoint.
// Same `targets` / legacy-alias semantics as ExchangeQuoteRequestBody.
// `max_payment_due_usd` is a TOTAL cap across all targets in the
// exchange — AWS returns a single aggregated PaymentDue so spend-cap
// checking naturally becomes a total when `targets[]` has multiple
// entries.
type ExchangeExecuteRequestBody struct {
	TargetOfferingID string               `json:"target_offering_id,omitempty"`
	MaxPaymentDueUSD string               `json:"max_payment_due_usd"`
	Region           string               `json:"region,omitempty"`
	RIIDs            []string             `json:"ri_ids"`
	Targets          []ExchangeTargetBody `json:"targets,omitempty"`
	TargetCount      int32                `json:"target_count,omitempty"`
}

// toExchangeTargets converts the HTTP-shaped targets into the
// pkg/exchange shape, preserving nil (not empty) when the caller used
// the legacy singleton fields so the pkg/exchange layer knows to fall
// back to them.
func toExchangeTargets(targets []ExchangeTargetBody) []exchange.TargetConfig {
	if len(targets) == 0 {
		return nil
	}
	out := make([]exchange.TargetConfig, 0, len(targets))
	for _, t := range targets {
		out = append(out, exchange.TargetConfig{OfferingID: t.OfferingID, Count: t.Count})
	}
	return out
}

// ExchangeExecuteResponse is the response from a successful exchange execution.
type ExchangeExecuteResponse struct {
	Quote      *exchange.ExchangeQuoteSummary `json:"quote"`
	ExchangeID string                         `json:"exchange_id"`
}

// getRIExchangeConfig returns the current RI exchange automation settings.
func (h *Handler) getRIExchangeConfig(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requirePermission(ctx, req, "view", "config"); err != nil {
		return nil, err
	}

	globalCfg, err := h.config.GetGlobalConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return &RIExchangeConfigResponse{
		AutoExchangeEnabled:      globalCfg.RIExchangeEnabled,
		Mode:                     globalCfg.RIExchangeMode,
		UtilizationThreshold:     globalCfg.RIExchangeUtilizationThreshold,
		MaxPaymentPerExchangeUSD: globalCfg.RIExchangeMaxPerExchangeUSD,
		MaxPaymentDailyUSD:       globalCfg.RIExchangeMaxDailyUSD,
		LookbackDays:             globalCfg.RIExchangeLookbackDays,
	}, nil
}

// updateRIExchangeConfig updates the RI exchange automation settings.
func (h *Handler) updateRIExchangeConfig(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requirePermission(ctx, req, "update", "config"); err != nil {
		return nil, err
	}

	var body RIExchangeConfigUpdateRequest
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	if err := body.validate(); err != nil {
		return nil, err
	}

	// Route through the serialized read-modify-write so this partial update
	// (only the ri_exchange_* columns) shares the advisory lock with other
	// global_config writers. An unlocked GetGlobalConfig -> mutate ->
	// SaveGlobalConfig here would write all 21 columns and could silently
	// revert a concurrent kill-switch toggle done via UpdateGlobalConfigAtomic.
	// The closure mutates ONLY the ri_exchange_* fields; body.validate above
	// already validated the inputs, so (matching the prior behavior) we do not
	// re-run the whole-config Validate here.
	if _, err := h.config.UpdateGlobalConfigAtomic(ctx, func(existing *config.GlobalConfig) error {
		existing.RIExchangeEnabled = body.AutoExchangeEnabled
		existing.RIExchangeMode = body.Mode
		existing.RIExchangeUtilizationThreshold = body.UtilizationThreshold
		existing.RIExchangeMaxPerExchangeUSD = body.MaxPaymentPerExchangeUSD
		existing.RIExchangeMaxDailyUSD = body.MaxPaymentDailyUSD
		existing.RIExchangeLookbackDays = body.LookbackDays
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to save config: %w", err)
	}

	return &StatusResponse{Status: "updated"}, nil
}

// getRIExchangeHistory returns RI exchange records from the last 12 months.
func (h *Handler) getRIExchangeHistory(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	since := time.Now().AddDate(-1, 0, 0)
	records, err := h.config.GetRIExchangeHistory(ctx, since, 500)
	if err != nil {
		return nil, fmt.Errorf("failed to load exchange history: %w", err)
	}

	// Filter records by the session's allowed_accounts against the record's
	// AccountID. Scoped users don't see history for accounts outside their
	// scope. Admin / unrestricted sessions pass through unchanged.
	allowed, err := h.getAccountScope(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("failed to get allowed accounts: %w", err)
	}
	if !allowed.AllowsAll() {
		nameByID := h.resolveAccountNamesByID(ctx)
		filtered := records[:0]
		for _rvc := range records {
			r := records[_rvc]
			if allowed.Allows(r.AccountID, nameByID[r.AccountID]) {
				filtered = append(filtered, r)
			}
		}
		records = filtered
	}

	// Strip approval tokens — single-use secrets must not be included in
	// a read-only response that could be cached, logged, or screen-shared.
	for i := range records {
		records[i].ApprovalToken = ""
	}

	return &RIExchangeHistoryResponse{Records: records}, nil
}

// approveRIExchange handles approval of a pending RI exchange.
//
// Three-mode dispatch mirroring approvePurchase (issue #286, issue #300):
//
//  1. Session present AND RBAC-authorized (admin / approve-any / approve-own
//     match) -> session-authed approve, regardless of whether a token is also
//     in the URL. Closes issue #300.
//  2. token != "" -> legacy email-link flow. validateExchangeApproval enforces
//     the token-equality check; the permission-denied fall-through ensures a
//     logged-in user without approve-* can still use an email link they hold.
//  3. token == "" AND no qualifying session -> 403 via
//     approveRIExchangeViaSession's requireSession gate.
func (h *Handler) approveRIExchange(ctx context.Context, req *events.LambdaFunctionURLRequest, id, token string) (any, error) {
	if session := h.tryGetSession(ctx, req); session != nil {
		// Quick RBAC pre-check (no record fetch needed): does this session hold
		// ANY approve right? If yes, hand off to approveRIExchangeViaSession
		// which will re-check ownership with the actual record. If 403, fall
		// through to the token branch so email-link holders can still approve.
		switch err := h.sessionHasApproveRight(ctx, session); {
		case err == nil:
			result, sessErr := h.approveRIExchangeViaSession(ctx, req, id, session)
			if sessErr == nil {
				return result, nil
			}
			// Record-level RBAC denied (e.g. approve-own user is not the creator).
			// If a token is present, preserve legacy token flow; otherwise surface the error.
			if token == "" || !isPermissionDenied(sessErr) {
				return nil, sessErr
			}
		case isPermissionDenied(err):
			// Logged-in user without approve-* may still hold a valid email token.
		default:
			return nil, err
		}
	}

	if token != "" {
		return h.approveRIExchangeViaToken(ctx, id, token)
	}

	return h.approveRIExchangeViaSession(ctx, req, id, nil)
}

// approveRIExchangeViaToken is the legacy email-link branch of approveRIExchange.
// It validates the approval token, transitions the exchange to processing, and
// executes it. Extracted to keep approveRIExchange within cyclomatic-complexity limits.
func (h *Handler) approveRIExchangeViaToken(ctx context.Context, id, token string) (any, error) {
	record, err := h.validateExchangeApproval(ctx, id, token)
	if err != nil {
		return nil, err
	}

	// Token-based approval: no session user, so transitioned_by = NULL.
	transitioned, err := h.config.TransitionRIExchangeStatus(ctx, id, "pending", "processing", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to transition exchange status: %w", err)
	}
	if transitioned == nil {
		return nil, NewClientError(409, "exchange already processed, expired, or was canceled by a newer analysis run")
	}

	return h.executeApprovedExchange(ctx, id, record)
}

// approveRIExchangeViaSession is the session-authed branch of approveRIExchange
// (issue #300). Enforces the approve-any/approve-own RBAC matrix, then atomically
// transitions pending -> processing and executes the exchange. Stamps
// session.Email onto the approved_by column as an audit trail.
//
// The session parameter may be non-nil (already validated by the caller) or nil
// (requireSession will validate it and return 401 if absent).
func (h *Handler) approveRIExchangeViaSession(ctx context.Context, req *events.LambdaFunctionURLRequest, id string, session *Session) (any, error) {
	var err error
	if session == nil {
		session, err = h.requireSession(ctx, req)
		if err != nil {
			return nil, err
		}
	}

	record, err := h.fetchAndAuthorizeRIExchange(ctx, session, id)
	if err != nil {
		return nil, err
	}

	// Session-authed approval: stamp the session user as the actor.
	transitioned, err := h.config.TransitionRIExchangeStatus(ctx, id, "pending", "processing", resolveCreatorUserID(session))
	if err != nil {
		return nil, fmt.Errorf("failed to transition exchange status: %w", err)
	}
	if transitioned == nil {
		return nil, NewClientError(409, "exchange already processed, expired, or was canceled by a newer analysis run")
	}

	result, execErr := h.executeApprovedExchange(ctx, id, record)

	// Stamp approver attribution (best-effort: the exchange itself already
	// executed, so a stamp failure is logged but not surfaced to the caller).
	if execErr == nil {
		if stampErr := h.config.StampRIExchangeApprovedBy(ctx, id, session.Email); stampErr != nil {
			logging.Errorf("failed to stamp approved_by on exchange %s: %v", id, stampErr)
		}
	}

	return result, execErr
}

// fetchAndAuthorizeRIExchange looks up the pending exchange record by id, checks
// that it is in "pending" state, and then verifies that session is authorized to
// approve it. Extracted from approveRIExchangeViaSession to keep that function
// under the cyclomatic-complexity limit.
func (h *Handler) fetchAndAuthorizeRIExchange(ctx context.Context, session *Session, id string) (*config.RIExchangeRecord, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}

	record, err := h.config.GetRIExchangeRecord(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to look up exchange record: %w", err)
	}
	if record == nil {
		return nil, NewClientError(404, "exchange record not found")
	}

	if record.Status != "pending" {
		return nil, NewClientError(409, fmt.Sprintf("exchange %s cannot be approved (status=%s)", id, record.Status))
	}

	if err := h.authorizeSessionApproveRIExchange(ctx, session, record); err != nil {
		return nil, err
	}

	return record, nil
}

// sessionHasApproveRight returns nil when the session holds ANY approve right
// on purchases (admin / approve-any / approve-own) without checking ownership.
// Used by the three-mode dispatch in approveRIExchange to decide whether to route
// to approveRIExchangeViaSession before fetching the record.
func (h *Handler) sessionHasApproveRight(ctx context.Context, session *Session) error {
	// Stateless admin API key: full access, no user row. Administrators-group
	// users pass via the approve-any HasPermissionAPI check below.
	if session.UserID == apiKeyAdminUserID {
		return nil
	}
	if h.auth == nil {
		return NewClientError(500, "authentication service not configured")
	}
	hasAny, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionApproveAny, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if hasAny {
		return nil
	}
	hasOwn, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionApproveOwn, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if hasOwn {
		return nil
	}
	return NewClientError(403, "permission denied: requires approve-any or approve-own on purchases")
}

// authorizeSessionApproveRIExchange returns nil when the session is permitted to
// approve the given RI exchange record under the approve-any / approve-own RBAC rules
// (issue #300). Mirrors authorizeSessionApprove from handler_purchases.go.
//
// The RI exchange shares ResourcePurchases because approval is conceptually
// "approving a purchase action on a different resource type" per the issue spec,
// which prefers reusing the existing verbs to keep the matrix small.
func (h *Handler) authorizeSessionApproveRIExchange(ctx context.Context, session *Session, record *config.RIExchangeRecord) error {
	// Stateless admin API key: full access, no user row. Administrators-group
	// users pass via the approve-any HasPermissionAPI check below.
	if session.UserID == apiKeyAdminUserID {
		return nil
	}
	if h.auth == nil {
		return NewClientError(500, "authentication service not configured")
	}

	hasAny, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionApproveAny, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if hasAny {
		return nil
	}

	hasOwn, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionApproveOwn, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if !hasOwn {
		return NewClientError(403, "permission denied: requires approve-any or approve-own on purchases")
	}

	// approve-own: only allow if the session user created this exchange.
	if record.CreatedByUserID == nil || *record.CreatedByUserID != session.UserID {
		return NewClientError(403, "permission denied: cannot approve another user's pending exchange")
	}

	return nil
}

// validateExchangeApproval validates ID, token, and record state for an exchange approval.
func (h *Handler) validateExchangeApproval(ctx context.Context, id, token string) (*config.RIExchangeRecord, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}
	if token == "" {
		return nil, NewClientError(400, "approval token is required")
	}

	record, err := h.config.GetRIExchangeRecord(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to look up exchange record: %w", err)
	}
	if record == nil {
		return nil, NewClientError(404, "exchange record not found")
	}

	if record.ApprovalToken == "" {
		return nil, NewClientError(403, "this exchange record does not support approval")
	}

	if subtle.ConstantTimeCompare([]byte(token), []byte(record.ApprovalToken)) != 1 {
		return nil, NewClientError(403, "invalid approval token")
	}

	return record, nil
}

// failExchange marks an exchange as failed, logging if the DB write also fails.
func (h *Handler) failExchange(ctx context.Context, id, reason string) (any, error) {
	if failErr := h.config.FailRIExchange(ctx, id, reason); failErr != nil {
		logging.Errorf("failed to mark exchange %s as failed (DB may be unavailable): %v", id, failErr)
	}
	return map[string]any{"status": "failed", "reason": reason}, nil
}

// retryCompleteWithPayment calls CompleteRIExchangeWithPayment up to
// maxLedgerAttempts times, logging retries. Returns a non-nil error if all
// attempts fail. Extracted from executeApprovedExchange to reduce its
// cyclomatic complexity (H4 fix).
func (h *Handler) retryCompleteWithPayment(ctx context.Context, id, exchangeID, acceptedPaymentDue string) error {
	const maxAttempts = 3
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err = h.config.CompleteRIExchangeWithPayment(ctx, id, exchangeID, acceptedPaymentDue)
		if err == nil {
			return nil
		}
		if attempt < maxAttempts {
			logging.Warnf("ledger write retry %d/%d for exchange %s after money moved: %v",
				attempt, maxAttempts, id, err)
		}
	}
	return err
}

// handlerChooseEffectiveCap returns the smaller of perExchangeCap and daily
// headroom (dailyCap - dailySpent), bounding Execute's MaxPaymentDueUSD so a
// fresh re-quote cannot exceed the remaining daily budget (H2 fix).
func handlerChooseEffectiveCap(dailyCap, dailySpent, perExchangeCap *big.Rat) *big.Rat {
	remaining := new(big.Rat).Sub(dailyCap, dailySpent)
	if remaining.Cmp(perExchangeCap) < 0 {
		return remaining
	}
	return perExchangeCap
}

// handlerAcceptedAmount extracts the confirmed payment amount from a fresh
// Execute quote, falling back to fallback when freshQ is nil or empty (H3 fix).
func handlerAcceptedAmount(freshQ *exchange.ExchangeQuoteSummary, fallback string) string {
	if freshQ == nil {
		return fallback
	}
	if freshQ.PaymentDueUSDStr != "" {
		return freshQ.PaymentDueUSDStr
	}
	// Zero-cost exchange: PaymentDueRaw was empty (AWS returned nil) so
	// PaymentDueUSDStr is also empty. Use "0" to avoid a NULL payment_due in
	// the DB that would silently distort GetRIExchangeDailySpend's SUM.
	return "0"
}

// checkCapsAndComputeHeadroom validates the spending-cap configuration, runs the
// daily-cap check, and computes the effective MaxPaymentDueUSD that Execute must
// not exceed (H2: remaining daily headroom vs per-exchange cap, whichever is
// smaller). Returns a non-empty reason string on any failure so the caller can
// forward it to failExchange.
func checkCapsAndComputeHeadroom(dailySpendStr, paymentDue string, cfg *config.GlobalConfig) (effectiveCap *big.Rat, reason string) {
	if cfg.RIExchangeMaxDailyUSD == 0 {
		return nil, "daily spending cap is not configured (RIExchangeMaxDailyUSD is 0)"
	}
	if reason := checkDailyCap(dailySpendStr, paymentDue, cfg.RIExchangeMaxDailyUSD); reason != "" {
		return nil, reason
	}
	if cfg.RIExchangeMaxPerExchangeUSD == 0 {
		return nil, "per-exchange spending cap is not configured (RIExchangeMaxPerExchangeUSD is 0)"
	}
	// checkDailyCap already verified dailySpendStr is parseable; a second failure
	// is an internal error - fail closed to avoid executing with wrong headroom.
	dailySpent, err := exchange.ParseDecimalRat(dailySpendStr)
	if err != nil || dailySpent == nil {
		return nil, fmt.Sprintf("daily spend re-parse failed (internal error): %v", err)
	}
	dailyCap := new(big.Rat).SetFloat64(cfg.RIExchangeMaxDailyUSD)
	perExchangeCap := new(big.Rat).SetFloat64(cfg.RIExchangeMaxPerExchangeUSD)
	return handlerChooseEffectiveCap(dailyCap, dailySpent, perExchangeCap), ""
}

// executeApprovedExchange checks caps and executes the exchange after approval.
func (h *Handler) executeApprovedExchange(ctx context.Context, id string, record *config.RIExchangeRecord) (any, error) {
	dailySpendStr, err := h.config.GetRIExchangeDailySpend(ctx, time.Now())
	if err != nil {
		return h.failExchange(ctx, id, "daily spending cap check failed")
	}

	globalCfg, err := h.config.GetGlobalConfig(ctx)
	if err != nil {
		return h.failExchange(ctx, id, "config load failed")
	}

	region := record.Region
	if region == "" {
		// Region is a required field captured at record-creation time.
		// Defaulting to us-east-1 here would execute a financial mutation
		// in the wrong region for records created without a region stamp.
		return h.failExchange(ctx, id, "exchange record has no region; cannot execute safely")
	}

	effectiveCap, reason := checkCapsAndComputeHeadroom(dailySpendStr, record.PaymentDue, globalCfg)
	if reason != "" {
		return h.failExchange(ctx, id, reason)
	}

	execFn := exchange.ExecuteExchange
	if h.executeExchangeFn != nil {
		execFn = h.executeExchangeFn
	}
	exchangeID, freshQ, execErr := execFn(ctx, exchange.ExchangeExecuteRequest{
		Region:           region,
		ReservedIDs:      record.SourceRIIDs,
		TargetOfferingID: record.TargetOfferingID,
		TargetCount:      int32(record.TargetCount), // #nosec G115 -- RI quantity stored from validated API request; AWS limits RI counts well below math.MaxInt32
		MaxPaymentDueUSD: effectiveCap,
	})
	if execErr != nil {
		return h.failExchange(ctx, id, execErr.Error())
	}

	// H3: persist the amount AWS actually accepted, not the stale pre-execution
	// quote stored in record.PaymentDue.
	acceptedPaymentDue := handlerAcceptedAmount(freshQ, record.PaymentDue)

	// H4: retry the ledger write via retryCompleteWithPayment; persistent
	// failure is returned as an error (HTTP 500) so the caller knows money
	// moved but the record was not updated.
	if completeErr := h.retryCompleteWithPayment(ctx, id, exchangeID, acceptedPaymentDue); completeErr != nil {
		logging.Errorf("all ledger write attempts failed for exchange %s after money moved: %v",
			id, completeErr)
		return nil, fmt.Errorf("exchange executed (id=%s) but ledger update failed: %w",
			exchangeID, completeErr)
	}

	return map[string]any{"status": "completed", "exchange_id": exchangeID}, nil
}

// checkDailyCap verifies the exchange payment won't exceed the daily spending cap.
// Returns an empty string if within cap, or a reason string if exceeded or if
// either input cannot be parsed (fail closed on parse errors).
func checkDailyCap(dailySpendStr, paymentDueStr string, maxDailyUSD float64) string {
	dailyCap := new(big.Rat).SetFloat64(maxDailyUSD)
	dailySpent, err := exchange.ParseDecimalRat(dailySpendStr)
	if err != nil || dailySpent == nil {
		// A parse failure means we cannot determine today's spend; treat as a cap
		// check failure to avoid under-counting spend (fail-safe).
		logging.Warnf("checkDailyCap: failed to parse daily spend string %q: %v; blocking exchange to avoid exceeding cap", dailySpendStr, err)
		return fmt.Sprintf("daily spend check failed: could not parse today's spend value %q", dailySpendStr)
	}
	paymentDue, err := exchange.ParseDecimalRat(paymentDueStr)
	if err != nil || paymentDue == nil {
		// H1 fix: fail closed on an unparseable payment-due string instead of
		// treating it as $0. An unparseable value means we cannot determine the
		// true cost of this exchange, so proceeding risks exceeding the cap.
		logging.Warnf("checkDailyCap: failed to parse payment due string %q: %v; blocking exchange to avoid cap bypass", paymentDueStr, err)
		return fmt.Sprintf("daily spend check failed: could not parse payment due value %q", paymentDueStr)
	}

	newTotal := new(big.Rat).Add(dailySpent, paymentDue)
	if newTotal.Cmp(dailyCap) > 0 {
		return fmt.Sprintf("daily cap exceeded: spent $%s + payment $%s > cap $%.2f",
			dailySpent.FloatString(2), paymentDue.FloatString(2), maxDailyUSD)
	}
	return ""
}

// rejectRIExchange handles rejection of a pending RI exchange via token.
func (h *Handler) rejectRIExchange(ctx context.Context, id, token string) (any, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}
	if token == "" {
		return nil, NewClientError(400, "rejection token is required")
	}

	record, err := h.config.GetRIExchangeRecord(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to look up exchange record: %w", err)
	}
	if record == nil {
		return nil, NewClientError(404, "exchange record not found")
	}

	if record.ApprovalToken == "" {
		return nil, NewClientError(403, "this exchange record does not support rejection")
	}

	if subtle.ConstantTimeCompare([]byte(token), []byte(record.ApprovalToken)) != 1 {
		return nil, NewClientError(403, "invalid rejection token")
	}

	// Token-based rejection: no session user, so transitioned_by = NULL.
	transitioned, err := h.config.TransitionRIExchangeStatus(ctx, id, "pending", config.StatusCanceled, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to transition exchange status: %w", err)
	}
	if transitioned == nil {
		return nil, NewClientError(409, "exchange already processed, expired, or was canceled")
	}

	return map[string]string{"status": config.StatusCanceled}, nil
}

// RIExchangeConfigResponse is the response for GET /api/ri-exchange/config.
type RIExchangeConfigResponse struct {
	Mode                     string  `json:"mode"`
	UtilizationThreshold     float64 `json:"utilization_threshold"`
	MaxPaymentPerExchangeUSD float64 `json:"max_payment_per_exchange_usd"`
	MaxPaymentDailyUSD       float64 `json:"max_payment_daily_usd"`
	LookbackDays             int     `json:"lookback_days"`
	AutoExchangeEnabled      bool    `json:"auto_exchange_enabled"`
}

// RIExchangeConfigUpdateRequest is the request body for PUT /api/ri-exchange/config.
type RIExchangeConfigUpdateRequest struct {
	Mode                     string  `json:"mode"`
	UtilizationThreshold     float64 `json:"utilization_threshold"`
	MaxPaymentPerExchangeUSD float64 `json:"max_payment_per_exchange_usd"`
	MaxPaymentDailyUSD       float64 `json:"max_payment_daily_usd"`
	LookbackDays             int     `json:"lookback_days"`
	AutoExchangeEnabled      bool    `json:"auto_exchange_enabled"`
}

func (r *RIExchangeConfigUpdateRequest) validate() error {
	if r.Mode != "manual" && r.Mode != "auto" {
		return NewClientError(400, "mode must be 'manual' or 'auto'")
	}
	if r.UtilizationThreshold < 0 || r.UtilizationThreshold > 100 {
		return NewClientError(400, "utilization_threshold must be between 0 and 100")
	}
	if r.LookbackDays < 1 || r.LookbackDays > 365 {
		return NewClientError(400, "lookback_days must be between 1 and 365")
	}
	if r.MaxPaymentPerExchangeUSD < 0 {
		return NewClientError(400, "max_payment_per_exchange_usd must be >= 0")
	}
	if r.MaxPaymentDailyUSD < 0 {
		return NewClientError(400, "max_payment_daily_usd must be >= 0")
	}
	return nil
}

// RIExchangeHistoryResponse is the response for GET /api/ri-exchange/history.
type RIExchangeHistoryResponse struct {
	Records []config.RIExchangeRecord `json:"records"`
}
