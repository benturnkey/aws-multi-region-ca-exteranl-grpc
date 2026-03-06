package ec2info

import (
	"context"
	"fmt"

	"aws-multi-region-ca-exteranl-grpc/pkg/awsclient"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// GetInstanceTypeFromLaunchTemplate resolves the instance type configured in an
// ASG's launch template. Calls DescribeLaunchTemplateVersions and reads
// ResponseLaunchTemplateData.InstanceType.
func GetInstanceTypeFromLaunchTemplate(ctx context.Context, ec2Client awsclient.EC2API, templateName, templateVersion string) (string, error) {
	if templateName == "" {
		return "", fmt.Errorf("launch template name is empty")
	}

	input := &ec2.DescribeLaunchTemplateVersionsInput{
		LaunchTemplateName: aws.String(templateName),
	}
	if templateVersion != "" {
		input.Versions = []string{templateVersion}
	} else {
		input.Versions = []string{"$Default"}
	}

	resp, err := ec2Client.DescribeLaunchTemplateVersions(ctx, input)
	if err != nil {
		return "", fmt.Errorf("describe launch template versions for %q: %w", templateName, err)
	}

	if len(resp.LaunchTemplateVersions) == 0 {
		return "", fmt.Errorf("no versions found for launch template %q", templateName)
	}

	ltData := resp.LaunchTemplateVersions[0].LaunchTemplateData
	if ltData == nil {
		return "", fmt.Errorf("launch template %q has no template data", templateName)
	}

	instanceType := string(ltData.InstanceType)
	if instanceType == "" {
		return "", fmt.Errorf("launch template %q does not specify an instance type", templateName)
	}

	return instanceType, nil
}
