package observability

import (
	"context"
	"time"

	"aws-multi-region-ca-exteranl-grpc/pkg/awsclient"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

var tracer = otel.Tracer(meterName)

// recordAWSCall traces and meters a single AWS API call.
func recordAWSCall[T any](
	ctx context.Context,
	m *Metrics,
	endpoint, region string,
	fn func(ctx context.Context) (T, error),
) (T, error) {
	ctx, span := tracer.Start(ctx, "aws."+endpoint,
		trace.WithAttributes(
			attribute.String("aws.endpoint", endpoint),
			attribute.String("aws.region", region),
		),
	)
	defer span.End()

	start := time.Now()
	result, err := fn(ctx)
	elapsed := time.Since(start).Seconds()

	statusVal := "ok"
	if err != nil {
		statusVal = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	m.AWSRequestDuration.Record(ctx, elapsed,
		metric.WithAttributes(
			attribute.String("endpoint", endpoint),
			attribute.String("region", region),
			attribute.String("status", statusVal),
		),
	)
	return result, err
}

// InstrumentedAutoScaling wraps AutoScalingAPI with tracing and metrics.
type InstrumentedAutoScaling struct {
	inner   awsclient.AutoScalingAPI
	region  string
	metrics *Metrics
}

func (w *InstrumentedAutoScaling) DescribeAutoScalingGroups(ctx context.Context, params *autoscaling.DescribeAutoScalingGroupsInput, optFns ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	return recordAWSCall(ctx, w.metrics, "DescribeAutoScalingGroups", w.region, func(ctx context.Context) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
		return w.inner.DescribeAutoScalingGroups(ctx, params, optFns...)
	})
}

func (w *InstrumentedAutoScaling) SetDesiredCapacity(ctx context.Context, params *autoscaling.SetDesiredCapacityInput, optFns ...func(*autoscaling.Options)) (*autoscaling.SetDesiredCapacityOutput, error) {
	return recordAWSCall(ctx, w.metrics, "SetDesiredCapacity", w.region, func(ctx context.Context) (*autoscaling.SetDesiredCapacityOutput, error) {
		return w.inner.SetDesiredCapacity(ctx, params, optFns...)
	})
}

func (w *InstrumentedAutoScaling) TerminateInstanceInAutoScalingGroup(ctx context.Context, params *autoscaling.TerminateInstanceInAutoScalingGroupInput, optFns ...func(*autoscaling.Options)) (*autoscaling.TerminateInstanceInAutoScalingGroupOutput, error) {
	return recordAWSCall(ctx, w.metrics, "TerminateInstanceInAutoScalingGroup", w.region, func(ctx context.Context) (*autoscaling.TerminateInstanceInAutoScalingGroupOutput, error) {
		return w.inner.TerminateInstanceInAutoScalingGroup(ctx, params, optFns...)
	})
}

// InstrumentedEC2 wraps EC2API with tracing and metrics.
type InstrumentedEC2 struct {
	inner   awsclient.EC2API
	region  string
	metrics *Metrics
}

func (w *InstrumentedEC2) DescribeLaunchTemplateVersions(ctx context.Context, params *ec2.DescribeLaunchTemplateVersionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplateVersionsOutput, error) {
	return recordAWSCall(ctx, w.metrics, "DescribeLaunchTemplateVersions", w.region, func(ctx context.Context) (*ec2.DescribeLaunchTemplateVersionsOutput, error) {
		return w.inner.DescribeLaunchTemplateVersions(ctx, params, optFns...)
	})
}

func (w *InstrumentedEC2) DescribeInstanceTypes(ctx context.Context, params *ec2.DescribeInstanceTypesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
	return recordAWSCall(ctx, w.metrics, "DescribeInstanceTypes", w.region, func(ctx context.Context) (*ec2.DescribeInstanceTypesOutput, error) {
		return w.inner.DescribeInstanceTypes(ctx, params, optFns...)
	})
}

// InstrumentClients returns a new Clients with instrumented wrappers.
func InstrumentClients(c *awsclient.Clients, region string, m *Metrics) *awsclient.Clients {
	out := &awsclient.Clients{
		AutoScaling: &InstrumentedAutoScaling{inner: c.AutoScaling, region: region, metrics: m},
	}
	if c.EC2 != nil {
		out.EC2 = &InstrumentedEC2{inner: c.EC2, region: region, metrics: m}
	}
	return out
}

// InstrumentedClientProvider wraps an AWSClientProvider to auto-instrument all returned clients.
type InstrumentedClientProvider struct {
	inner   ClientProvider
	metrics *Metrics
}

// ClientProvider matches the server.AWSClientProvider interface.
type ClientProvider interface {
	ForRegion(region string) (*awsclient.Clients, error)
	ForNodeGroupID(id string) (*awsclient.Clients, error)
}

// NewInstrumentedClientProvider creates a new instrumented client provider.
func NewInstrumentedClientProvider(inner ClientProvider, m *Metrics) *InstrumentedClientProvider {
	return &InstrumentedClientProvider{inner: inner, metrics: m}
}

func (p *InstrumentedClientProvider) ForRegion(region string) (*awsclient.Clients, error) {
	c, err := p.inner.ForRegion(region)
	if err != nil {
		return nil, err
	}
	return InstrumentClients(c, region, p.metrics), nil
}

func (p *InstrumentedClientProvider) ForNodeGroupID(id string) (*awsclient.Clients, error) {
	region, _, _ := awsclient.ParseNodeGroupID(id)
	c, err := p.inner.ForNodeGroupID(id)
	if err != nil {
		return nil, err
	}
	return InstrumentClients(c, region, p.metrics), nil
}
