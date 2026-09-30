// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"io"
	"log/slog"
	"testing"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

// fakeELBv2 records load-balancer API calls.
type fakeELBv2 struct {
	register   []int32
	deregister []int32
	describe   []int32
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
