// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

// fakeELBv2 records load-balancer API calls and returns configured attributes.
type fakeELBv2 struct {
	values      map[string][]string
	describeErr map[string]error
	modifyCalls []string
	register    []int32
	deregister  []int32
	describe    []int32
}

// DescribeTargetGroupAttributes returns the next configured attribute value.
func (f *fakeELBv2) DescribeTargetGroupAttributes(_ context.Context, input *elasticloadbalancingv2.DescribeTargetGroupAttributesInput, _ ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetGroupAttributesOutput, error) {
	arn := awsSDK.ToString(input.TargetGroupArn)
	if err := f.describeErr[arn]; err != nil {
		return nil, err
	}
	values := f.values[arn]
	value := ""
	if len(values) > 0 {
		value = values[0]
		f.values[arn] = values[1:]
	}
	return &elasticloadbalancingv2.DescribeTargetGroupAttributesOutput{Attributes: []types.TargetGroupAttribute{{Key: awsSDK.String(crossZoneAttribute), Value: awsSDK.String(value)}}}, nil
}

// ModifyTargetGroupAttributes records the target group being modified.
func (f *fakeELBv2) ModifyTargetGroupAttributes(_ context.Context, input *elasticloadbalancingv2.ModifyTargetGroupAttributesInput, _ ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.ModifyTargetGroupAttributesOutput, error) {
	f.modifyCalls = append(f.modifyCalls, awsSDK.ToString(input.TargetGroupArn))
	return &elasticloadbalancingv2.ModifyTargetGroupAttributesOutput{}, nil
}

// RegisterTargets records the registered target port.
func (f *fakeELBv2) RegisterTargets(_ context.Context, input *elasticloadbalancingv2.RegisterTargetsInput, _ ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.RegisterTargetsOutput, error) {
	f.register = append(f.register, awsSDK.ToInt32(input.Targets[0].Port))
	return &elasticloadbalancingv2.RegisterTargetsOutput{}, nil
}

// DeregisterTargets records the deregistered target port.
func (f *fakeELBv2) DeregisterTargets(_ context.Context, input *elasticloadbalancingv2.DeregisterTargetsInput, _ ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DeregisterTargetsOutput, error) {
	f.deregister = append(f.deregister, awsSDK.ToInt32(input.Targets[0].Port))
	return &elasticloadbalancingv2.DeregisterTargetsOutput{}, nil
}

// DescribeTargetHealth records the queried target port and reports it healthy.
func (f *fakeELBv2) DescribeTargetHealth(_ context.Context, input *elasticloadbalancingv2.DescribeTargetHealthInput, _ ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetHealthOutput, error) {
	f.describe = append(f.describe, awsSDK.ToInt32(input.Targets[0].Port))
	return &elasticloadbalancingv2.DescribeTargetHealthOutput{TargetHealthDescriptions: []types.TargetHealthDescription{{
		TargetHealth: &types.TargetHealth{State: types.TargetHealthStateEnumHealthy},
	}}}, nil
}

// TestSetLBCrossZone verifies idempotent updates and post-update confirmation.
func TestSetLBCrossZone(t *testing.T) {
	tests := []struct {
		name        string
		fake        *fakeELBv2
		targets     []string
		wantModify  []string
		wantErrText string
	}{
		{
			name:    "already correct",
			fake:    &fakeELBv2{values: map[string][]string{"tg-1": {"true", "true"}}, describeErr: map[string]error{}},
			targets: []string{"tg-1"},
		},
		{
			name:    "mutation and confirmation",
			fake:    &fakeELBv2{values: map[string][]string{"tg-1": {"false", "true"}}, describeErr: map[string]error{}},
			targets: []string{"tg-1"}, wantModify: []string{"tg-1"},
		},
		{
			name:    "confirmation failure",
			fake:    &fakeELBv2{values: map[string][]string{"tg-1": {"false", "false"}}, describeErr: map[string]error{}},
			targets: []string{"tg-1"}, wantModify: []string{"tg-1"}, wantErrText: "was not confirmed as true",
		},
		{
			name:    "multi target group failure",
			fake:    &fakeELBv2{values: map[string][]string{"tg-1": {"true", "true"}}, describeErr: map[string]error{"tg-2": errors.New("denied")}},
			targets: []string{"tg-1", "tg-2"}, wantErrText: "target group tg-2: denied",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Provider{elbv2Client: tt.fake, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			configs := make([]provider.AWSTargetGroupConfig, 0, len(tt.targets))
			for _, arn := range tt.targets {
				configs = append(configs, provider.AWSTargetGroupConfig{ARN: arn})
			}
			err := p.SetLBCrossZone(context.Background(), provider.SetLBCrossZoneRequest{
				LBConfig: provider.LoadBalancerConfig{TargetGroups: configs}, Enabled: true,
			})
			if tt.wantErrText == "" && err != nil {
				t.Fatalf("SetLBCrossZone() error = %v", err)
			}
			if tt.wantErrText != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErrText)) {
				t.Fatalf("SetLBCrossZone() error = %v, want containing %q", err, tt.wantErrText)
			}
			if strings.Join(tt.fake.modifyCalls, ",") != strings.Join(tt.wantModify, ",") {
				t.Fatalf("modify calls = %v, want %v", tt.fake.modifyCalls, tt.wantModify)
			}
		})
	}
}

// TestWakeProxySelectsPort verifies direct and wake-proxy port selection.
func TestWakeProxySelectsPort(t *testing.T) {
	for _, tt := range []struct {
		name      string
		wakeProxy bool
		port      int32
	}{
		{name: "direct", port: 6443},
		{name: "wake proxy", wakeProxy: true, port: 16443},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeELBv2{}
			p := &Provider{elbv2Client: fake, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			req := provider.RegisterLBRequest{
				ProviderInstanceID: "i-1",
				LBConfig: provider.LoadBalancerConfig{TargetGroups: []provider.AWSTargetGroupConfig{{
					ARN: "tg-1", TargetPort: 6443, ProxyPort: 16443,
				}}},
				WakeProxy: tt.wakeProxy,
			}
			if err := p.RegisterWithLB(context.Background(), req); err != nil {
				t.Fatalf("RegisterWithLB(): %v", err)
			}
			if _, err := p.GetLBTargetState(context.Background(), req); err != nil {
				t.Fatalf("GetLBTargetState(): %v", err)
			}
			if err := p.DeregisterFromLB(context.Background(), provider.DeregisterLBRequest(req)); err != nil {
				t.Fatalf("DeregisterFromLB(): %v", err)
			}
			if len(fake.register) != 1 || fake.register[0] != tt.port ||
				len(fake.describe) != 1 || fake.describe[0] != tt.port ||
				len(fake.deregister) != 1 || fake.deregister[0] != tt.port {
				t.Fatalf("ports register=%v describe=%v deregister=%v, want %d", fake.register, fake.describe, fake.deregister, tt.port)
			}
		})
	}
}
