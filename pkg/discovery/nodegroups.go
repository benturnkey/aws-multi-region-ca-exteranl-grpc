package discovery

import (
	"context"
	"fmt"

	"aws-multi-region-ca-exteranl-grpc/pkg/awsclient"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
)

// RegionClientProvider resolves region-scoped AWS clients.
type RegionClientProvider interface {
	Regions() []string
	ForRegion(region string) (*awsclient.Clients, error)
}

// ListNodeGroupIDs discovers ASG-backed nodegroups in configured regions.
// IDs are returned as `region/asg-name`.
func ListNodeGroupIDs(ctx context.Context, provider RegionClientProvider) ([]string, error) {
	regions := provider.Regions()
	ids := make([]string, 0)

	for _, region := range regions {
		clients, err := provider.ForRegion(region)
		if err != nil {
			return nil, fmt.Errorf("get clients for region %q: %w", region, err)
		}

		var nextToken *string
		for {
			resp, err := clients.AutoScaling.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
				NextToken: nextToken,
			})
			if err != nil {
				return nil, fmt.Errorf("describe autoscaling groups for region %q: %w", region, err)
			}

			for _, g := range resp.AutoScalingGroups {
				if g.AutoScalingGroupName == nil || *g.AutoScalingGroupName == "" {
					continue
				}
				ids = append(ids, region+"/"+aws.ToString(g.AutoScalingGroupName))
			}

			if resp.NextToken == nil || aws.ToString(resp.NextToken) == "" {
				break
			}
			nextToken = resp.NextToken
		}
	}

	return ids, nil
}
