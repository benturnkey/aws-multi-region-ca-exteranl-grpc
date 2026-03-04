package discovery

import (
	"context"
	"fmt"
	"strings"

	"aws-multi-region-ca-exteranl-grpc/pkg/cache"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
)

// SnapshotBuilder builds an immutable cache snapshot.
type SnapshotBuilder interface {
	Build(ctx context.Context) (cache.Snapshot, error)
}

// ASGSnapshotBuilder builds snapshots from regional ASG discovery.
type ASGSnapshotBuilder struct {
	provider      RegionClientProvider
	discoveryTags map[string]string
	explicitNames []string
}

// NewASGSnapshotBuilder creates a concrete snapshot builder backed by region-scoped clients.
func NewASGSnapshotBuilder(provider RegionClientProvider, tags map[string]string, explicitNames []string) *ASGSnapshotBuilder {
	return &ASGSnapshotBuilder{
		provider:      provider,
		discoveryTags: tags,
		explicitNames: explicitNames,
	}
}

// Build rebuilds nodegroup/node/instance mappings from ASGs in all regions.
func (b *ASGSnapshotBuilder) Build(ctx context.Context) (cache.Snapshot, error) {
	snap := cache.Snapshot{
		NodeGroups:            map[string]cache.NodeGroup{},
		NodeGroupByProviderID: map[string]string{},
		NodeGroupByNodeName:   map[string]string{},
		InstancesByNodeGroup:  map[string][]cache.Instance{},
	}

	var tagFilters []autoscalingtypes.Filter
	for k, v := range b.discoveryTags {
		tagFilters = append(tagFilters, autoscalingtypes.Filter{
			Name:   aws.String("tag:" + k),
			Values: []string{v},
		})
	}

	for _, region := range b.provider.Regions() {
		clients, err := b.provider.ForRegion(region)
		if err != nil {
			return cache.Snapshot{}, fmt.Errorf("get clients for region %q: %w", region, err)
		}

		seenASGs := make(map[string]bool)

		processASGs := func(groups []autoscalingtypes.AutoScalingGroup) {
			for _, g := range groups {
				name := aws.ToString(g.AutoScalingGroupName)
				if name == "" || seenASGs[name] {
					continue
				}
				seenASGs[name] = true
				nodeGroupID := region + "/" + name

				minSize := int(aws.ToInt32(g.MinSize))
				maxSize := int(aws.ToInt32(g.MaxSize))
				targetSize := int(aws.ToInt32(g.DesiredCapacity))

				snap.NodeGroups[nodeGroupID] = cache.NodeGroup{
					ID:         nodeGroupID,
					MinSize:    minSize,
					MaxSize:    maxSize,
					TargetSize: targetSize,
				}

				snap.InstancesByNodeGroup[nodeGroupID] = append(snap.InstancesByNodeGroup[nodeGroupID], mapASGInstances(g.Instances)...)

				for _, inst := range g.Instances {
					instanceID := aws.ToString(inst.InstanceId)
					if instanceID == "" {
						continue
					}
					snap.NodeGroupByProviderID[instanceID] = nodeGroupID
					if az := aws.ToString(inst.AvailabilityZone); az != "" {
						snap.NodeGroupByProviderID["aws:///"+az+"/"+instanceID] = nodeGroupID
					}
					snap.NodeGroupByProviderID["aws:///"+instanceID] = nodeGroupID
				}
			}
		}

		if len(tagFilters) > 0 {
			var nextToken *string
			for {
				resp, err := clients.AutoScaling.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
					Filters:   tagFilters,
					NextToken: nextToken,
				})
				if err != nil {
					return cache.Snapshot{}, fmt.Errorf("describe autoscaling groups by tags for region %q: %w", region, err)
				}
				processASGs(resp.AutoScalingGroups)
				if resp.NextToken == nil || aws.ToString(resp.NextToken) == "" {
					break
				}
				nextToken = resp.NextToken
			}
		}

		if len(b.explicitNames) > 0 {
			// AWS DescribeAutoScalingGroups accepts up to 50 names at a time.
			// Batch the names appropriately.
			var nextToken *string
			for {
				// We don't need to manually chunk if we use NextToken, but we do need to pass all names
				resp, err := clients.AutoScaling.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
					AutoScalingGroupNames: b.explicitNames,
					NextToken:             nextToken,
				})
				if err != nil {
					return cache.Snapshot{}, fmt.Errorf("describe autoscaling groups by name for region %q: %w", region, err)
				}
				processASGs(resp.AutoScalingGroups)
				if resp.NextToken == nil || aws.ToString(resp.NextToken) == "" {
					break
				}
				nextToken = resp.NextToken
			}
		}
	}

	return snap, nil
}

// BuildSnapshot preserves existing call sites while delegating to the concrete builder.
func BuildSnapshot(ctx context.Context, provider RegionClientProvider) (cache.Snapshot, error) {
	return NewASGSnapshotBuilder(provider, nil, nil).Build(ctx)
}

func mapASGInstances(in []autoscalingtypes.Instance) []cache.Instance {
	out := make([]cache.Instance, 0, len(in))
	for _, inst := range in {
		id := aws.ToString(inst.InstanceId)
		if id == "" {
			continue
		}
		providerID := id
		if az := aws.ToString(inst.AvailabilityZone); az != "" {
			providerID = "aws:///" + az + "/" + id
		}
		out = append(out, cache.Instance{
			ID:    providerID,
			State: mapInstanceState(inst.LifecycleState),
		})
	}
	return out
}

func mapInstanceState(state autoscalingtypes.LifecycleState) cache.InstanceState {
	s := strings.ToLower(string(state))
	switch {
	case s == "inservice":
		return cache.InstanceStateRunning
	case s == "pending" || strings.HasPrefix(s, "pending"):
		return cache.InstanceStateCreating
	case s == "terminating" || strings.HasPrefix(s, "terminating"):
		return cache.InstanceStateDeleting
	default:
		return cache.InstanceStateUnspecified
	}
}
