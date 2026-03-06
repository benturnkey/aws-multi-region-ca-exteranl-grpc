package observability

import (
	"context"
	"errors"
	"testing"

	"aws-multi-region-ca-exteranl-grpc/pkg/awsclient"

	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"go.opentelemetry.io/otel"
)

// --- Fake AutoScaling ---

type fakeAutoScaling struct {
	describeOut   *autoscaling.DescribeAutoScalingGroupsOutput
	setCapOut     *autoscaling.SetDesiredCapacityOutput
	terminateOut  *autoscaling.TerminateInstanceInAutoScalingGroupOutput
	returnErr     error
	calledMethods []string
}

func (f *fakeAutoScaling) DescribeAutoScalingGroups(ctx context.Context, params *autoscaling.DescribeAutoScalingGroupsInput, optFns ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	f.calledMethods = append(f.calledMethods, "DescribeAutoScalingGroups")
	return f.describeOut, f.returnErr
}

func (f *fakeAutoScaling) SetDesiredCapacity(ctx context.Context, params *autoscaling.SetDesiredCapacityInput, optFns ...func(*autoscaling.Options)) (*autoscaling.SetDesiredCapacityOutput, error) {
	f.calledMethods = append(f.calledMethods, "SetDesiredCapacity")
	return f.setCapOut, f.returnErr
}

func (f *fakeAutoScaling) TerminateInstanceInAutoScalingGroup(ctx context.Context, params *autoscaling.TerminateInstanceInAutoScalingGroupInput, optFns ...func(*autoscaling.Options)) (*autoscaling.TerminateInstanceInAutoScalingGroupOutput, error) {
	f.calledMethods = append(f.calledMethods, "TerminateInstanceInAutoScalingGroup")
	return f.terminateOut, f.returnErr
}

// --- Fake EC2 ---

type fakeEC2 struct {
	ltOut         *ec2.DescribeLaunchTemplateVersionsOutput
	itOut         *ec2.DescribeInstanceTypesOutput
	returnErr     error
	calledMethods []string
}

func (f *fakeEC2) DescribeLaunchTemplateVersions(ctx context.Context, params *ec2.DescribeLaunchTemplateVersionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplateVersionsOutput, error) {
	f.calledMethods = append(f.calledMethods, "DescribeLaunchTemplateVersions")
	return f.ltOut, f.returnErr
}

func (f *fakeEC2) DescribeInstanceTypes(ctx context.Context, params *ec2.DescribeInstanceTypesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
	f.calledMethods = append(f.calledMethods, "DescribeInstanceTypes")
	return f.itOut, f.returnErr
}

func TestInstrumentedAutoScalingPassThrough(t *testing.T) {
	t.Parallel()
	m, err := NewMetrics(otel.GetMeterProvider())
	if err != nil {
		t.Fatal(err)
	}

	expectedOut := &autoscaling.DescribeAutoScalingGroupsOutput{}
	fake := &fakeAutoScaling{describeOut: expectedOut}
	wrapped := &InstrumentedAutoScaling{inner: fake, region: "us-east-1", metrics: m}

	got, err := wrapped.DescribeAutoScalingGroups(context.Background(), &autoscaling.DescribeAutoScalingGroupsInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != expectedOut {
		t.Fatal("expected output to pass through")
	}
	if len(fake.calledMethods) != 1 || fake.calledMethods[0] != "DescribeAutoScalingGroups" {
		t.Fatalf("expected DescribeAutoScalingGroups called, got %v", fake.calledMethods)
	}
}

func TestInstrumentedAutoScalingErrorPassThrough(t *testing.T) {
	t.Parallel()
	m, err := NewMetrics(otel.GetMeterProvider())
	if err != nil {
		t.Fatal(err)
	}

	wantErr := errors.New("boom")
	fake := &fakeAutoScaling{returnErr: wantErr}
	wrapped := &InstrumentedAutoScaling{inner: fake, region: "us-east-1", metrics: m}

	_, err = wrapped.SetDesiredCapacity(context.Background(), &autoscaling.SetDesiredCapacityInput{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected error %v, got %v", wantErr, err)
	}
}

func TestInstrumentedEC2PassThrough(t *testing.T) {
	t.Parallel()
	m, err := NewMetrics(otel.GetMeterProvider())
	if err != nil {
		t.Fatal(err)
	}

	expectedOut := &ec2.DescribeInstanceTypesOutput{}
	fake := &fakeEC2{itOut: expectedOut}
	wrapped := &InstrumentedEC2{inner: fake, region: "us-west-2", metrics: m}

	got, err := wrapped.DescribeInstanceTypes(context.Background(), &ec2.DescribeInstanceTypesInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != expectedOut {
		t.Fatal("expected output to pass through")
	}
}

func TestInstrumentClientsWraps(t *testing.T) {
	t.Parallel()
	m, err := NewMetrics(otel.GetMeterProvider())
	if err != nil {
		t.Fatal(err)
	}

	original := &awsclient.Clients{
		AutoScaling: &fakeAutoScaling{},
		EC2:         &fakeEC2{},
	}
	wrapped := InstrumentClients(original, "us-east-1", m)

	if _, ok := wrapped.AutoScaling.(*InstrumentedAutoScaling); !ok {
		t.Fatal("expected AutoScaling to be instrumented")
	}
	if _, ok := wrapped.EC2.(*InstrumentedEC2); !ok {
		t.Fatal("expected EC2 to be instrumented")
	}
}

// --- Fake client provider ---

type fakeClientProvider struct {
	clients *awsclient.Clients
	err     error
}

func (f *fakeClientProvider) ForRegion(region string) (*awsclient.Clients, error) {
	return f.clients, f.err
}

func (f *fakeClientProvider) ForNodeGroupID(id string) (*awsclient.Clients, error) {
	return f.clients, f.err
}

func TestInstrumentedClientProviderWraps(t *testing.T) {
	t.Parallel()
	m, err := NewMetrics(otel.GetMeterProvider())
	if err != nil {
		t.Fatal(err)
	}

	inner := &fakeClientProvider{
		clients: &awsclient.Clients{
			AutoScaling: &fakeAutoScaling{},
			EC2:         &fakeEC2{},
		},
	}
	provider := NewInstrumentedClientProvider(inner, m)

	c, err := provider.ForRegion("us-east-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := c.AutoScaling.(*InstrumentedAutoScaling); !ok {
		t.Fatal("expected AutoScaling to be instrumented via provider")
	}

	c2, err := provider.ForNodeGroupID("us-east-1/my-asg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := c2.EC2.(*InstrumentedEC2); !ok {
		t.Fatal("expected EC2 to be instrumented via provider")
	}
}

func TestInstrumentedClientProviderForwardsErrors(t *testing.T) {
	t.Parallel()
	m, err := NewMetrics(otel.GetMeterProvider())
	if err != nil {
		t.Fatal(err)
	}

	wantErr := errors.New("no such region")
	inner := &fakeClientProvider{err: wantErr}
	provider := NewInstrumentedClientProvider(inner, m)

	_, err = provider.ForRegion("bad-region")
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
}
