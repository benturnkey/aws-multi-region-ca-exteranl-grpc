package awsclient

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"aws-multi-region-ca-exteranl-grpc/pkg/config"
	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// AutoScalingAPI is the subset of ASG calls used by this project.
type AutoScalingAPI interface {
	DescribeAutoScalingGroups(ctx context.Context, params *autoscaling.DescribeAutoScalingGroupsInput, optFns ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error)
	SetDesiredCapacity(ctx context.Context, params *autoscaling.SetDesiredCapacityInput, optFns ...func(*autoscaling.Options)) (*autoscaling.SetDesiredCapacityOutput, error)
	TerminateInstanceInAutoScalingGroup(ctx context.Context, params *autoscaling.TerminateInstanceInAutoScalingGroupInput, optFns ...func(*autoscaling.Options)) (*autoscaling.TerminateInstanceInAutoScalingGroupOutput, error)
}

// EC2API is the subset of EC2 calls used by this project.
type EC2API interface {
	DescribeLaunchTemplateVersions(ctx context.Context, params *ec2.DescribeLaunchTemplateVersionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplateVersionsOutput, error)
	DescribeInstanceTypes(ctx context.Context, params *ec2.DescribeInstanceTypesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error)
}

// Clients contains region-scoped AWS service clients used by this provider.
type Clients struct {
	AutoScaling AutoScalingAPI
	EC2         EC2API
}

// Factory builds and routes region-specific clients.
type Factory struct {
	regions []string
	clients map[string]*Clients
}

type loadConfigFunc func(ctx context.Context, region string) (awsv2.Config, error)

// NewFactory constructs AWS clients for every configured region.
func NewFactory(ctx context.Context, cfg config.Config) (*Factory, error) {
	return NewFactoryWithLoader(ctx, cfg, defaultLoadConfig)
}

// NewFactoryWithLoader allows deterministic tests by injecting AWS config loading.
func NewFactoryWithLoader(ctx context.Context, cfg config.Config, loader loadConfigFunc) (*Factory, error) {
	if loader == nil {
		return nil, errors.New("loader is required")
	}

	regions := cfg.NormalizedRegions()
	if len(regions) == 0 {
		return nil, errors.New("at least one region is required")
	}

	clients := make(map[string]*Clients, len(regions))
	for _, region := range regions {
		awsCfg, err := loader(ctx, region)
		if err != nil {
			return nil, fmt.Errorf("load aws config for region %q: %w", region, err)
		}
		clients[region] = &Clients{
			AutoScaling: autoscaling.NewFromConfig(awsCfg),
			EC2:         ec2.NewFromConfig(awsCfg),
		}
	}

	return &Factory{regions: regions, clients: clients}, nil
}

func defaultLoadConfig(ctx context.Context, region string) (awsv2.Config, error) {
	return awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(region))
}

// Regions returns normalized regions preserving configured order.
func (f *Factory) Regions() []string {
	out := make([]string, len(f.regions))
	copy(out, f.regions)
	return out
}

// ForRegion returns service clients for a known region.
func (f *Factory) ForRegion(region string) (*Clients, error) {
	norm := strings.TrimSpace(region)
	if norm == "" {
		return nil, errors.New("region is required")
	}
	c, ok := f.clients[norm]
	if !ok {
		return nil, fmt.Errorf("region %q is not configured", norm)
	}
	return c, nil
}

// ForNodeGroupID routes based on nodegroup id format `region/asg-name`.
func (f *Factory) ForNodeGroupID(id string) (*Clients, error) {
	region, _, err := ParseNodeGroupID(id)
	if err != nil {
		return nil, err
	}
	return f.ForRegion(region)
}

// ParseNodeGroupID parses a region-qualified nodegroup id.
func ParseNodeGroupID(id string) (region string, name string, err error) {
	parts := strings.Split(id, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid nodegroup id %q: expected region/asg-name", id)
	}
	region = strings.TrimSpace(parts[0])
	name = strings.TrimSpace(parts[1])
	if region == "" || name == "" {
		return "", "", fmt.Errorf("invalid nodegroup id %q: expected region/asg-name", id)
	}
	return region, name, nil
}
