package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	logstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/mathspace/lambdafy/fnspec"
)

type cloudwatchLogRetentionClient interface {
	CreateLogGroup(context.Context, *cloudwatchlogs.CreateLogGroupInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogGroupOutput, error)
	DescribeLogGroups(context.Context, *cloudwatchlogs.DescribeLogGroupsInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error)
	PutRetentionPolicy(context.Context, *cloudwatchlogs.PutRetentionPolicyInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutRetentionPolicyOutput, error)
}

func lambdaLogGroupName(fnName string) string {
	return fmt.Sprintf("/aws/lambda/%s", fnName)
}

func ensureLogGroupRetention(ctx context.Context, logsCl cloudwatchLogRetentionClient, fnName string, retentionDays int32) error {
	if !fnspec.IsValidLogGroupRetentionDays(retentionDays) {
		return fmt.Errorf("invalid log_group_retention_days value %d", retentionDays)
	}

	logGroupName := lambdaLogGroupName(fnName)
	currentRetention, found, err := describeLogGroupRetention(ctx, logsCl, logGroupName)
	if err != nil {
		return err
	}
	if found && currentRetention != nil && *currentRetention == retentionDays {
		return nil
	}
	if !found {
		if err := createLogGroup(ctx, logsCl, logGroupName); err != nil {
			return err
		}
	}

	err = retry(ctx, func() error {
		_, err := logsCl.PutRetentionPolicy(ctx, &cloudwatchlogs.PutRetentionPolicyInput{
			LogGroupName:    aws.String(logGroupName),
			RetentionInDays: aws.Int32(retentionDays),
		})
		return err
	}, "OperationAbortedException", "ResourceNotFoundException")
	if err != nil {
		return fmt.Errorf("failed to set retention policy for log group %q: %w", logGroupName, err)
	}
	return nil
}

func describeLogGroupRetention(ctx context.Context, logsCl cloudwatchLogRetentionClient, logGroupName string) (*int32, bool, error) {
	var nextToken *string
	for {
		out, err := logsCl.DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
			LogGroupNamePrefix: aws.String(logGroupName),
			NextToken:          nextToken,
		})
		if err != nil {
			return nil, false, fmt.Errorf("failed to describe log group %q: %w", logGroupName, err)
		}
		for _, group := range out.LogGroups {
			if group.LogGroupName == nil || *group.LogGroupName != logGroupName {
				continue
			}
			return group.RetentionInDays, true, nil
		}
		if out.NextToken == nil {
			return nil, false, nil
		}
		nextToken = out.NextToken
	}
}

func createLogGroup(ctx context.Context, logsCl cloudwatchLogRetentionClient, logGroupName string) error {
	_, err := logsCl.CreateLogGroup(ctx, &cloudwatchlogs.CreateLogGroupInput{
		LogGroupName: aws.String(logGroupName),
	})
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*logstypes.ResourceAlreadyExistsException](err); ok {
		return nil
	}
	return fmt.Errorf("failed to create log group %q: %w", logGroupName, err)
}
