package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/LeanerCloud/CUDly/ci_cd_sanity_tests/pkg/sanity/report"
)

type Options struct {
	Region          string
	ExpectedAccount string // optional safety check
	MaxList         int32  // used for EC2; RDS will clamp to valid range
}

func checkIdentity(ctx context.Context, cfg aws.Config, expectedAccount string) (map[string]string, error) {
	out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, err
	}
	d := map[string]string{
		"account": aws.ToString(out.Account),
		"arn":     aws.ToString(out.Arn),
		"user_id": aws.ToString(out.UserId),
	}
	if expectedAccount != "" && aws.ToString(out.Account) != expectedAccount {
		return d, fmt.Errorf("unexpected AWS account: got %s want %s", aws.ToString(out.Account), expectedAccount)
	}
	return d, nil
}

func checkRegions(ctx context.Context, cfg aws.Config) (map[string]string, error) {
	out, err := ec2.NewFromConfig(cfg).DescribeRegions(ctx, &ec2.DescribeRegionsInput{})
	if err != nil {
		return nil, err
	}
	return map[string]string{"regions_count": fmt.Sprintf("%d", len(out.Regions))}, nil
}

func checkInstances(ctx context.Context, cfg aws.Config, maxList int32) (map[string]string, error) {
	out, err := ec2.NewFromConfig(cfg).DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		MaxResults: aws.Int32(maxList),
	})
	if err != nil {
		return nil, err
	}
	instances := 0
	for _, r := range out.Reservations {
		instances += len(r.Instances)
	}
	return map[string]string{"instances_seen": fmt.Sprintf("%d", instances)}, nil
}

func checkRDS(ctx context.Context, cfg aws.Config, maxList int32) (map[string]string, error) {
	max := maxList
	if max < 20 {
		max = 20
	}
	if max > 100 {
		max = 100
	}
	out, err := rds.NewFromConfig(cfg).DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		MaxRecords: aws.Int32(max),
	})
	if err != nil {
		return nil, err
	}
	return map[string]string{"db_instances_seen": fmt.Sprintf("%d", len(out.DBInstances))}, nil
}

func Run(ctx context.Context, opts Options) (*report.Report, error) {
	if opts.Region == "" {
		opts.Region = "us-east-1"
	}
	if opts.MaxList <= 0 {
		opts.MaxList = 5
	}

	rep := &report.Report{
		RunID:     fmt.Sprintf("aws-%d", time.Now().Unix()),
		Cloud:     "aws",
		Mode:      "dry-run",
		StartedAt: time.Now().UTC(),
	}

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(opts.Region))
	if err != nil {
		return nil, err
	}

	runCheck := func(name string, fn func() (map[string]string, error)) {
		start := time.Now().UTC()
		details, e := fn()
		cr := report.CheckResult{Name: name, StartedAt: start, EndedAt: time.Now().UTC()}
		if e == nil {
			cr.Status = report.StatusPass
		} else {
			cr.Status = report.StatusFail
			cr.Message = e.Error()
		}
		cr.Details = details
		rep.Add(cr)
	}

	runCheck("sts:GetCallerIdentity", func() (map[string]string, error) {
		return checkIdentity(ctx, cfg, opts.ExpectedAccount)
	})
	runCheck("ec2:DescribeRegions", func() (map[string]string, error) {
		return checkRegions(ctx, cfg)
	})
	runCheck("ec2:DescribeInstances (sample)", func() (map[string]string, error) {
		return checkInstances(ctx, cfg, opts.MaxList)
	})
	runCheck("rds:DescribeDBInstances (sample)", func() (map[string]string, error) {
		return checkRDS(ctx, cfg, opts.MaxList)
	})

	rep.EndedAt = time.Now().UTC()
	return rep, nil
}
