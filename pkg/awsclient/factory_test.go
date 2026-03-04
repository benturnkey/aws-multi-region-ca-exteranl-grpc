package awsclient

import (
	"context"
	"reflect"
	"testing"

	"aws-multi-region-ca-exteranl-grpc/pkg/config"
	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
)

func TestNewFactoryBuildsClientsPerNormalizedRegion(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Regions: []string{" us-east-1 ", "us-west-2", "", "us-east-1"},
	}

	var called []string
	loader := func(_ context.Context, region string) (awsv2.Config, error) {
		called = append(called, region)
		return awsv2.Config{Region: region}, nil
	}

	factory, err := NewFactoryWithLoader(context.Background(), cfg, loader)
	if err != nil {
		t.Fatalf("NewFactoryWithLoader error: %v", err)
	}

	wantRegions := []string{"us-east-1", "us-west-2"}
	if got := factory.Regions(); !reflect.DeepEqual(got, wantRegions) {
		t.Fatalf("Regions=%v want=%v", got, wantRegions)
	}
	if !reflect.DeepEqual(called, wantRegions) {
		t.Fatalf("loader called with=%v want=%v", called, wantRegions)
	}

	for _, region := range wantRegions {
		clients, getErr := factory.ForRegion(region)
		if getErr != nil {
			t.Fatalf("ForRegion(%q): %v", region, getErr)
		}
		if clients.AutoScaling == nil {
			t.Fatalf("ForRegion(%q): AutoScaling client is nil", region)
		}
		if clients.EC2 == nil {
			t.Fatalf("ForRegion(%q): EC2 client is nil", region)
		}
	}
}

func TestNewFactoryErrorsWithoutRegions(t *testing.T) {
	t.Parallel()

	_, err := NewFactoryWithLoader(context.Background(), config.Config{}, func(_ context.Context, region string) (awsv2.Config, error) {
		return awsv2.Config{Region: region}, nil
	})
	if err == nil {
		t.Fatalf("expected error for empty regions")
	}
}

func TestParseNodeGroupID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id         string
		wantRegion string
		wantName   string
		wantErr    bool
	}{
		{id: "us-east-1/my-asg", wantRegion: "us-east-1", wantName: "my-asg"},
		{id: "", wantErr: true},
		{id: "us-east-1", wantErr: true},
		{id: "/my-asg", wantErr: true},
		{id: "us-east-1/", wantErr: true},
		{id: "us-east-1/my/asg", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.id, func(t *testing.T) {
			t.Parallel()
			gotRegion, gotName, err := ParseNodeGroupID(tt.id)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.id)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotRegion != tt.wantRegion || gotName != tt.wantName {
				t.Fatalf("ParseNodeGroupID(%q)=(%q,%q), want (%q,%q)", tt.id, gotRegion, gotName, tt.wantRegion, tt.wantName)
			}
		})
	}
}

func TestForNodeGroupIDRoutesByRegion(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Regions: []string{"us-east-1", "us-west-2"}}
	factory, err := NewFactoryWithLoader(context.Background(), cfg, func(_ context.Context, region string) (awsv2.Config, error) {
		return awsv2.Config{Region: region}, nil
	})
	if err != nil {
		t.Fatalf("NewFactoryWithLoader error: %v", err)
	}

	clients, err := factory.ForNodeGroupID("us-west-2/asg-a")
	if err != nil {
		t.Fatalf("ForNodeGroupID error: %v", err)
	}
	if clients == nil {
		t.Fatalf("expected clients")
	}

	if _, err := factory.ForNodeGroupID("eu-central-1/asg-a"); err == nil {
		t.Fatalf("expected error for unknown region")
	}
}
