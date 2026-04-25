package commitmentopts

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
)

// maxPages is the hard ceiling on paginated Describe*Offerings calls per
// probe. We only need unique (duration, payment) tuples, which saturate
// well before 5 pages of 100 offerings each. The cap bounds worst-case API
// spend if an SDK change ever breaks pagination detection.
const maxPages = 5

// pageSize is the MaxRecords/MaxResults value we request. 100 is the
// documented maximum for every AWS Describe*Offerings API we call.
const pageSize int32 = 100

// Canonical probe targets. Picked per service so "offerings exist" is
// guaranteed in every commercial region — small/cheap instance types with
// long-standing public availability. The targets never round-trip through
// a purchase so their cost is irrelevant; what matters is that AWS
// actually has offerings to return for them.
//
// probeTargetMemoryDB carries an empirical risk: MemoryDB reserved-node
// coverage has historically skewed to db.r6g.* tiers, and db.t4g.small may
// return an empty offerings list in some regions. If that happens the
// orchestrating Service still persists the run (so we don't re-probe in a
// hot loop) and the frontend silently falls back to hardcoded MemoryDB
// rules. Switching to db.r6g.large would be the safe alternative once a
// human with AWS creds confirms via:
//
//	aws memorydb describe-reserved-nodes-offerings \
//	    --region us-east-1 --node-type db.t4g.small
//
// Tracked in github.com/LeanerCloud/CUDly#61.
const (
	probeTargetRDS         = "db.t3.micro"
	probeTargetElastiCache = "cache.t3.micro"
	probeTargetOpenSearch  = "t3.small.search"
	probeTargetRedshift    = "dc2.large"
	probeTargetMemoryDB    = "db.t4g.small"
	probeTargetEC2         = "t3.micro"
)

// walkPaginated runs the standard paginated-Describe loop shared by every
// per-service prober: call fetchPage, accumulate the rawOffers it yields,
// thread the next-page token, and stop after maxPages iterations even if
// AWS keeps returning a non-empty token. Each prober supplies its own
// fetchPage closure that handles per-API quirks (Marker vs NextToken,
// per-item shape conversion, optional client-side instance-type filter).
//
// The loop is broken out into a single helper so the page cap is exercised
// by one unit test (TestWalkPaginated_StopsAtPageCap) rather than six
// near-duplicate tests, and so a refactor can't silently lose the cap on
// one prober while leaving the others intact.
//
// fetchPage receives the token from the previous page (nil on the first
// call) and returns:
//   - the rawOffers extracted from this page
//   - the next-page token (nil or "" means "no more pages")
//   - any API error
//
// service is the canonical service name; it's used to wrap fetchPage errors
// as "<service>: <err>" so callers see which prober blew up without each
// one having to repeat the wrap.
func walkPaginated(
	ctx context.Context,
	service string,
	fetchPage func(ctx context.Context, token *string) ([]rawOffer, *string, error),
) ([]rawOffer, error) {
	var raw []rawOffer
	var token *string
	for page := 0; page < maxPages; page++ {
		offers, next, err := fetchPage(ctx, token)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", service, err)
		}
		raw = append(raw, offers...)
		if next == nil || aws.ToString(next) == "" {
			break
		}
		token = next
	}
	return raw, nil
}

// collect dedupes a probe's raw (durationSeconds, rawPayment) pairs, runs
// both normalizers, and builds the Combo slice. Duplicates — a single
// (term, payment) tuple appears once per instance size × AZ × engine
// variant — are collapsed so the caller sees at most six Combos per
// service (2 terms × 3 payments).
func collect(service string, raw []rawOffer) []Combo {
	type key struct {
		term    int
		payment string
	}
	seen := make(map[key]struct{}, len(raw))
	out := make([]Combo, 0, len(raw))
	for _, r := range raw {
		term, ok := durationToTerm(r.durationSeconds)
		if !ok {
			continue
		}
		payment, ok := normalizePayment(r.payment)
		if !ok {
			continue
		}
		k := key{term: term, payment: payment}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, Combo{
			Provider:  "aws",
			Service:   service,
			TermYears: term,
			Payment:   payment,
		})
	}
	return out
}

// rawOffer is the pre-normalization shape every per-service probe feeds
// into collect(). Keeping the shape uniform means normalization lives in
// exactly one place.
type rawOffer struct {
	durationSeconds int64
	payment         string
}

// ---------------------------------------------------------------------------
// RDS
// ---------------------------------------------------------------------------

// RDSDescribeOfferings is the minimal RDS surface the probe needs. It
// matches the method signature on the generated client so tests can
// substitute a mock without dragging in the full RDSAPI interface.
type RDSDescribeOfferings interface {
	DescribeReservedDBInstancesOfferings(ctx context.Context, params *rds.DescribeReservedDBInstancesOfferingsInput, optFns ...func(*rds.Options)) (*rds.DescribeReservedDBInstancesOfferingsOutput, error)
}

// RDSProber probes rds:DescribeReservedDBInstancesOfferings.
type RDSProber struct {
	// NewClient builds a client from the probe's aws.Config. Override in
	// tests to return a mock.
	NewClient func(cfg aws.Config) RDSDescribeOfferings
}

// Service returns "rds".
func (p *RDSProber) Service() string { return "rds" }

// Probe returns the normalized (term, payment) combos RDS currently sells
// against db.t3.micro.
func (p *RDSProber) Probe(ctx context.Context, cfg aws.Config) ([]Combo, error) {
	client := p.client(cfg)
	raw, err := walkPaginated(ctx, p.Service(), func(ctx context.Context, token *string) ([]rawOffer, *string, error) {
		out, err := client.DescribeReservedDBInstancesOfferings(ctx, &rds.DescribeReservedDBInstancesOfferingsInput{
			DBInstanceClass: aws.String(probeTargetRDS),
			MaxRecords:      aws.Int32(pageSize),
			Marker:          token,
		})
		if err != nil {
			return nil, nil, err
		}
		offers := make([]rawOffer, 0, len(out.ReservedDBInstancesOfferings))
		for _, o := range out.ReservedDBInstancesOfferings {
			offers = append(offers, rawOffer{
				durationSeconds: int64(aws.ToInt32(o.Duration)),
				payment:         aws.ToString(o.OfferingType),
			})
		}
		return offers, out.Marker, nil
	})
	if err != nil {
		return nil, err
	}
	return collect(p.Service(), raw), nil
}

func (p *RDSProber) client(cfg aws.Config) RDSDescribeOfferings {
	if p.NewClient != nil {
		return p.NewClient(cfg)
	}
	return rds.NewFromConfig(cfg)
}

// ---------------------------------------------------------------------------
// ElastiCache
// ---------------------------------------------------------------------------

// ElastiCacheDescribeOfferings is the minimal ElastiCache surface we use.
type ElastiCacheDescribeOfferings interface {
	DescribeReservedCacheNodesOfferings(ctx context.Context, params *elasticache.DescribeReservedCacheNodesOfferingsInput, optFns ...func(*elasticache.Options)) (*elasticache.DescribeReservedCacheNodesOfferingsOutput, error)
}

// ElastiCacheProber probes elasticache:DescribeReservedCacheNodesOfferings.
type ElastiCacheProber struct {
	NewClient func(cfg aws.Config) ElastiCacheDescribeOfferings
}

// Service returns "elasticache".
func (p *ElastiCacheProber) Service() string { return "elasticache" }

// Probe returns the combos for cache.t3.micro.
func (p *ElastiCacheProber) Probe(ctx context.Context, cfg aws.Config) ([]Combo, error) {
	client := p.client(cfg)
	raw, err := walkPaginated(ctx, p.Service(), func(ctx context.Context, token *string) ([]rawOffer, *string, error) {
		out, err := client.DescribeReservedCacheNodesOfferings(ctx, &elasticache.DescribeReservedCacheNodesOfferingsInput{
			CacheNodeType: aws.String(probeTargetElastiCache),
			MaxRecords:    aws.Int32(pageSize),
			Marker:        token,
		})
		if err != nil {
			return nil, nil, err
		}
		offers := make([]rawOffer, 0, len(out.ReservedCacheNodesOfferings))
		for _, o := range out.ReservedCacheNodesOfferings {
			offers = append(offers, rawOffer{
				durationSeconds: int64(aws.ToInt32(o.Duration)),
				payment:         aws.ToString(o.OfferingType),
			})
		}
		return offers, out.Marker, nil
	})
	if err != nil {
		return nil, err
	}
	return collect(p.Service(), raw), nil
}

func (p *ElastiCacheProber) client(cfg aws.Config) ElastiCacheDescribeOfferings {
	if p.NewClient != nil {
		return p.NewClient(cfg)
	}
	return elasticache.NewFromConfig(cfg)
}

// ---------------------------------------------------------------------------
// OpenSearch
// ---------------------------------------------------------------------------

// OpenSearchDescribeOfferings is the minimal OpenSearch surface we use.
// The OpenSearch API has no per-instance-type filter on this endpoint, so
// the probe filters client-side after fetching.
type OpenSearchDescribeOfferings interface {
	DescribeReservedInstanceOfferings(ctx context.Context, params *opensearch.DescribeReservedInstanceOfferingsInput, optFns ...func(*opensearch.Options)) (*opensearch.DescribeReservedInstanceOfferingsOutput, error)
}

// OpenSearchProber probes opensearch:DescribeReservedInstanceOfferings.
type OpenSearchProber struct {
	NewClient func(cfg aws.Config) OpenSearchDescribeOfferings
}

// Service returns "opensearch".
func (p *OpenSearchProber) Service() string { return "opensearch" }

// Probe returns the combos for t3.small.search.
func (p *OpenSearchProber) Probe(ctx context.Context, cfg aws.Config) ([]Combo, error) {
	client := p.client(cfg)
	raw, err := walkPaginated(ctx, p.Service(), func(ctx context.Context, token *string) ([]rawOffer, *string, error) {
		out, err := client.DescribeReservedInstanceOfferings(ctx, &opensearch.DescribeReservedInstanceOfferingsInput{
			MaxResults: pageSize,
			NextToken:  token,
		})
		if err != nil {
			return nil, nil, err
		}
		offers := make([]rawOffer, 0, len(out.ReservedInstanceOfferings))
		for _, o := range out.ReservedInstanceOfferings {
			if string(o.InstanceType) != probeTargetOpenSearch {
				continue
			}
			offers = append(offers, rawOffer{
				durationSeconds: int64(o.Duration),
				payment:         string(o.PaymentOption),
			})
		}
		return offers, out.NextToken, nil
	})
	if err != nil {
		return nil, err
	}
	return collect(p.Service(), raw), nil
}

func (p *OpenSearchProber) client(cfg aws.Config) OpenSearchDescribeOfferings {
	if p.NewClient != nil {
		return p.NewClient(cfg)
	}
	return opensearch.NewFromConfig(cfg)
}

// ---------------------------------------------------------------------------
// Redshift
// ---------------------------------------------------------------------------

// RedshiftDescribeOfferings is the minimal Redshift surface we use. The
// API has no NodeType filter on DescribeReservedNodeOfferings so the probe
// filters client-side.
type RedshiftDescribeOfferings interface {
	DescribeReservedNodeOfferings(ctx context.Context, params *redshift.DescribeReservedNodeOfferingsInput, optFns ...func(*redshift.Options)) (*redshift.DescribeReservedNodeOfferingsOutput, error)
}

// RedshiftProber probes redshift:DescribeReservedNodeOfferings.
type RedshiftProber struct {
	NewClient func(cfg aws.Config) RedshiftDescribeOfferings
}

// Service returns "redshift".
func (p *RedshiftProber) Service() string { return "redshift" }

// Probe returns the combos for dc2.large.
func (p *RedshiftProber) Probe(ctx context.Context, cfg aws.Config) ([]Combo, error) {
	client := p.client(cfg)
	raw, err := walkPaginated(ctx, p.Service(), func(ctx context.Context, token *string) ([]rawOffer, *string, error) {
		out, err := client.DescribeReservedNodeOfferings(ctx, &redshift.DescribeReservedNodeOfferingsInput{
			MaxRecords: aws.Int32(pageSize),
			Marker:     token,
		})
		if err != nil {
			return nil, nil, err
		}
		offers := make([]rawOffer, 0, len(out.ReservedNodeOfferings))
		for _, o := range out.ReservedNodeOfferings {
			if aws.ToString(o.NodeType) != probeTargetRedshift {
				continue
			}
			offers = append(offers, rawOffer{
				durationSeconds: int64(aws.ToInt32(o.Duration)),
				payment:         aws.ToString(o.OfferingType),
			})
		}
		return offers, out.Marker, nil
	})
	if err != nil {
		return nil, err
	}
	return collect(p.Service(), raw), nil
}

func (p *RedshiftProber) client(cfg aws.Config) RedshiftDescribeOfferings {
	if p.NewClient != nil {
		return p.NewClient(cfg)
	}
	return redshift.NewFromConfig(cfg)
}

// ---------------------------------------------------------------------------
// MemoryDB
// ---------------------------------------------------------------------------

// MemoryDBDescribeOfferings is the minimal MemoryDB surface we use.
type MemoryDBDescribeOfferings interface {
	DescribeReservedNodesOfferings(ctx context.Context, params *memorydb.DescribeReservedNodesOfferingsInput, optFns ...func(*memorydb.Options)) (*memorydb.DescribeReservedNodesOfferingsOutput, error)
}

// MemoryDBProber probes memorydb:DescribeReservedNodesOfferings.
type MemoryDBProber struct {
	NewClient func(cfg aws.Config) MemoryDBDescribeOfferings
}

// Service returns "memorydb".
func (p *MemoryDBProber) Service() string { return "memorydb" }

// Probe returns the combos for db.t4g.small.
func (p *MemoryDBProber) Probe(ctx context.Context, cfg aws.Config) ([]Combo, error) {
	client := p.client(cfg)
	raw, err := walkPaginated(ctx, p.Service(), func(ctx context.Context, token *string) ([]rawOffer, *string, error) {
		out, err := client.DescribeReservedNodesOfferings(ctx, &memorydb.DescribeReservedNodesOfferingsInput{
			NodeType:   aws.String(probeTargetMemoryDB),
			MaxResults: aws.Int32(pageSize),
			NextToken:  token,
		})
		if err != nil {
			return nil, nil, err
		}
		offers := make([]rawOffer, 0, len(out.ReservedNodesOfferings))
		for _, o := range out.ReservedNodesOfferings {
			offers = append(offers, rawOffer{
				durationSeconds: int64(o.Duration),
				payment:         aws.ToString(o.OfferingType),
			})
		}
		return offers, out.NextToken, nil
	})
	if err != nil {
		return nil, err
	}
	return collect(p.Service(), raw), nil
}

func (p *MemoryDBProber) client(cfg aws.Config) MemoryDBDescribeOfferings {
	if p.NewClient != nil {
		return p.NewClient(cfg)
	}
	return memorydb.NewFromConfig(cfg)
}

// ---------------------------------------------------------------------------
// EC2
// ---------------------------------------------------------------------------

// EC2DescribeOfferings is the minimal EC2 surface we use.
type EC2DescribeOfferings interface {
	DescribeReservedInstancesOfferings(ctx context.Context, params *ec2.DescribeReservedInstancesOfferingsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeReservedInstancesOfferingsOutput, error)
}

// EC2Prober probes ec2:DescribeReservedInstancesOfferings.
type EC2Prober struct {
	NewClient func(cfg aws.Config) EC2DescribeOfferings
}

// Service returns "ec2".
func (p *EC2Prober) Service() string { return "ec2" }

// Probe returns the combos for t3.micro. IncludeMarketplace is explicitly
// false so we only see AWS-native (standard/convertible) offerings — the
// Marketplace resale market has arbitrary durations that would pollute
// normalization.
func (p *EC2Prober) Probe(ctx context.Context, cfg aws.Config) ([]Combo, error) {
	client := p.client(cfg)
	raw, err := walkPaginated(ctx, p.Service(), func(ctx context.Context, token *string) ([]rawOffer, *string, error) {
		out, err := client.DescribeReservedInstancesOfferings(ctx, &ec2.DescribeReservedInstancesOfferingsInput{
			InstanceType:       ec2types.InstanceType(probeTargetEC2),
			IncludeMarketplace: aws.Bool(false),
			MaxResults:         aws.Int32(pageSize),
			NextToken:          token,
		})
		if err != nil {
			return nil, nil, err
		}
		offers := make([]rawOffer, 0, len(out.ReservedInstancesOfferings))
		for _, o := range out.ReservedInstancesOfferings {
			offers = append(offers, rawOffer{
				durationSeconds: aws.ToInt64(o.Duration),
				payment:         string(o.OfferingType),
			})
		}
		return offers, out.NextToken, nil
	})
	if err != nil {
		return nil, err
	}
	return collect(p.Service(), raw), nil
}

func (p *EC2Prober) client(cfg aws.Config) EC2DescribeOfferings {
	if p.NewClient != nil {
		return p.NewClient(cfg)
	}
	return ec2.NewFromConfig(cfg)
}

// DefaultProbers returns one prober instance per commitment-capable
// service. The Service wires these up by default; tests override via a
// custom slice.
func DefaultProbers() []Prober {
	return []Prober{
		&RDSProber{},
		&ElastiCacheProber{},
		&OpenSearchProber{},
		&RedshiftProber{},
		&MemoryDBProber{},
		&EC2Prober{},
	}
}
