package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type Clients struct {
	EC2 *ec2.Client
	RDS *rds.Client
	STS *sts.Client
}

func NewClients(ctx context.Context, region string) (*Clients, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS config: %w", err)
	}

	return &Clients{
		EC2: ec2.NewFromConfig(cfg),
		RDS: rds.NewFromConfig(cfg),
		STS: sts.NewFromConfig(cfg),
	}, nil
}
