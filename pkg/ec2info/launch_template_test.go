package ec2info

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestGetInstanceTypeFromLaunchTemplate(t *testing.T) {
	t.Parallel()

	client := &fakeEC2Client{
		describeLTVersionsOutput: &ec2.DescribeLaunchTemplateVersionsOutput{
			LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
				{
					LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
						InstanceType: ec2types.InstanceTypeM5Xlarge,
					},
				},
			},
		},
	}

	got, err := GetInstanceTypeFromLaunchTemplate(context.Background(), client, "my-template", "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "m5.xlarge" {
		t.Fatalf("instance type=%q want=m5.xlarge", got)
	}
}

func TestGetInstanceTypeFromLaunchTemplateDefaultVersion(t *testing.T) {
	t.Parallel()

	client := &fakeEC2Client{
		describeLTVersionsOutput: &ec2.DescribeLaunchTemplateVersionsOutput{
			LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
				{
					LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
						InstanceType: ec2types.InstanceTypeT3Medium,
					},
				},
			},
		},
	}

	got, err := GetInstanceTypeFromLaunchTemplate(context.Background(), client, "my-template", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "t3.medium" {
		t.Fatalf("instance type=%q want=t3.medium", got)
	}
}

func TestGetInstanceTypeFromLaunchTemplateEmptyName(t *testing.T) {
	t.Parallel()

	_, err := GetInstanceTypeFromLaunchTemplate(context.Background(), nil, "", "1")
	if err == nil {
		t.Fatal("expected error for empty template name")
	}
}

func TestGetInstanceTypeFromLaunchTemplateAPIError(t *testing.T) {
	t.Parallel()

	client := &fakeEC2Client{
		describeLTVersionsErr: errors.New("access denied"),
	}

	_, err := GetInstanceTypeFromLaunchTemplate(context.Background(), client, "my-template", "1")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetInstanceTypeFromLaunchTemplateNoVersions(t *testing.T) {
	t.Parallel()

	client := &fakeEC2Client{
		describeLTVersionsOutput: &ec2.DescribeLaunchTemplateVersionsOutput{
			LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{},
		},
	}

	_, err := GetInstanceTypeFromLaunchTemplate(context.Background(), client, "my-template", "1")
	if err == nil {
		t.Fatal("expected error for no versions")
	}
}

func TestGetInstanceTypeFromLaunchTemplateNoInstanceType(t *testing.T) {
	t.Parallel()

	client := &fakeEC2Client{
		describeLTVersionsOutput: &ec2.DescribeLaunchTemplateVersionsOutput{
			LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
				{
					LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{},
				},
			},
		},
	}

	_, err := GetInstanceTypeFromLaunchTemplate(context.Background(), client, "my-template", "1")
	if err == nil {
		t.Fatal("expected error when instance type is not specified")
	}
}
