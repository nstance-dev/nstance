// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/nstance-dev/nstance/internal/operator/config"
	"github.com/nstance-dev/nstance/internal/operator/connection"
	"github.com/nstance-dev/nstance/internal/proto"
)

type sleepOperatorServer struct {
	proto.UnimplementedOperatorServiceServer
	sleep func() (*proto.SleepTenantResponse, error)
}

func (s *sleepOperatorServer) SleepTenant(context.Context, *proto.SleepTenantRequest) (*proto.SleepTenantResponse, error) {
	return s.sleep()
}

func newSleepConnection(t *testing.T, service proto.OperatorServiceServer) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	proto.RegisterOperatorServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	connection, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}

// TestSuspendedJobPodOwnershipRequiresUID verifies that names cannot substitute for owner UIDs.
func TestSuspendedJobPodOwnershipRequiresUID(t *testing.T) {
	deleting := metav1.NewTime(time.Now())
	pods := []corev1.Pod{{
		ObjectMeta: metav1.ObjectMeta{
			DeletionTimestamp: &deleting,
			OwnerReferences:   []metav1.OwnerReference{{Name: "job", UID: types.UID("other")}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
	}}
	if hasLiveOwnedPod(pods, types.UID("job-uid")) {
		t.Fatal("pod with a different owner UID blocked sleep")
	}
	pods[0].OwnerReferences[0].UID = types.UID("job-uid")
	if !hasLiveOwnedPod(pods, types.UID("job-uid")) {
		t.Fatal("terminating owned pod did not block sleep")
	}
}

// TestNextCronHonorsTimeZone verifies schedule evaluation in the CronJob time zone.
func TestNextCronHonorsTimeZone(t *testing.T) {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.January, 5, 16, 30, 0, 0, time.UTC)
	next, err := nextCron("0 9 * * 1-5", now, location)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.January, 5, 17, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next run = %s, want %s", next, want)
	}
}

// TestOrderedShardsPlacesCoordinatorLast verifies the destructive ordering invariant.
func TestOrderedShardsPlacesCoordinatorLast(t *testing.T) {
	connections := map[string]*grpc.ClientConn{"b": nil, "a": nil, "c": nil}
	got := orderedShards(connections, "b")
	want := []string{"a", "c", "b"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ordered shards = %v, want %v", got, want)
		}
	}
}

// TestDecodeProgressPreservesCompensation verifies restart recovery retains accepted shards.
func TestDecodeProgressPreservesCompensation(t *testing.T) {
	progress, err := decodeProgress(`{"request":"force:x","phase":"compensating","accepted":["a"]}`, "force:x")
	if err != nil {
		t.Fatal(err)
	}
	if progress.Phase != sleepPhaseCompensating || len(progress.Accepted) != 1 || progress.Accepted[0] != "a" {
		t.Fatalf("decoded progress = %#v", progress)
	}
	if _, err := decodeProgress(`{"request":"old","phase":"compensating"}`, "new"); err == nil {
		t.Fatal("mismatched request was accepted")
	}
}

// TestDecodeProgressRequiresFinalShard verifies the pre-shutdown checkpoint
// always identifies the shard whose sleep may stop the operator.
func TestDecodeProgressRequiresFinalShard(t *testing.T) {
	if _, err := decodeProgress(`{"request":"x","phase":"finalizing"}`, "x"); err == nil {
		t.Fatal("finalizing progress without a final shard was accepted")
	}
	progress, err := decodeProgress(`{"request":"x","phase":"finalizing","accepted":["a"],"final_shard":"b"}`, "x")
	if err != nil {
		t.Fatal(err)
	}
	if progress.FinalShard != "b" || len(progress.Accepted) != 1 || progress.Accepted[0] != "a" {
		t.Fatalf("decoded progress = %#v", progress)
	}
}

// TestReconcileCheckpointsBeforeFinalShard verifies successful final shutdown
// needs no later Kubernetes write.
func TestReconcileCheckpointsBeforeFinalShard(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clusterv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	request := "force:" + time.Now().UTC().Format(time.RFC3339Nano)
	cluster := &clusterv1.Cluster{ObjectMeta: metav1.ObjectMeta{
		Namespace:   "nstance-system",
		Name:        "cluster--red",
		Annotations: map[string]string{SleepRequestAnnotation: request},
	}}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "operator-node", Labels: map[string]string{corev1.LabelTopologyZone: "a"}}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, node).Build()
	slept := func() (*proto.SleepTenantResponse, error) {
		return &proto.SleepTenantResponse{
			Result: proto.SleepTenantResponse_RESULT_SLEPT,
			Status: proto.TenantSleepStatus_TENANT_SLEEP_STATUS_ASLEEP,
		}, nil
	}
	checked := make(chan error, 1)
	provider := connection.NewProvider()
	provider.Set(map[string]*grpc.ClientConn{
		"b": newSleepConnection(t, &sleepOperatorServer{sleep: slept}),
		"a": newSleepConnection(t, &sleepOperatorServer{sleep: func() (*proto.SleepTenantResponse, error) {
			var current clusterv1.Cluster
			if err := kube.Get(context.Background(), client.ObjectKeyFromObject(cluster), &current); err != nil {
				checked <- err
				return slept()
			}
			var progress sleepProgress
			err := json.Unmarshal([]byte(current.Annotations[SleepProgressAnnotation]), &progress)
			if err == nil && (progress.Phase != sleepPhaseFinalizing || progress.FinalShard != "a" || len(progress.Accepted) != 1 || progress.Accepted[0] != "b") {
				err = fmt.Errorf("unexpected progress before final shard: %#v", progress)
			}
			checked <- err
			return slept()
		}}),
	})
	reconciler := &SleepReconciler{
		Client:       kube,
		ConnProvider: provider,
		Recorder:     record.NewFakeRecorder(1),
		Config:       &config.OperatorConfig{ClusterID: "cluster", Tenant: "red", Sleep: config.SleepPolicy{ReconcilePeriod: config.Duration{Duration: time.Minute}}},
		NodeName:     "operator-node",
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)}); err != nil {
		t.Fatal(err)
	}
	if err := <-checked; err != nil {
		t.Fatal(err)
	}
	if err := kube.Get(context.Background(), client.ObjectKeyFromObject(cluster), cluster); err != nil {
		t.Fatal(err)
	}
	progress, err := decodeProgress(cluster.Annotations[SleepProgressAnnotation], request)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Phase != sleepPhaseFinalizing || cluster.Annotations[SleepRequestAnnotation] != request {
		t.Fatalf("annotations = %#v, want retained finalizing transaction", cluster.Annotations)
	}
}

// TestReconcileRejectsMalformedRequest verifies invalid user input cannot leave
// transaction annotations behind.
func TestReconcileRejectsMalformedRequest(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clusterv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cluster := &clusterv1.Cluster{ObjectMeta: metav1.ObjectMeta{
		Namespace: "nstance-system",
		Name:      "cluster--red",
		Annotations: map[string]string{
			SleepRequestAnnotation:  "not-a-time",
			SleepProgressAnnotation: `{"request":"not-a-time","phase":"sleeping"}`,
		},
	}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build()
	reconciler := &SleepReconciler{
		Client:   kube,
		Recorder: record.NewFakeRecorder(1),
		Config:   &config.OperatorConfig{ClusterID: "cluster", Tenant: "red"},
	}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)})
	if err != nil {
		t.Fatal(err)
	}
	if err := kube.Get(context.Background(), client.ObjectKeyFromObject(cluster), cluster); err != nil {
		t.Fatal(err)
	}
	if cluster.Annotations[SleepRequestAnnotation] != "" || cluster.Annotations[SleepProgressAnnotation] != "" {
		t.Fatalf("annotations = %#v, want transaction annotations removed", cluster.Annotations)
	}
}

// TestEligibleRejectsScaleDown verifies sleep cannot race a downward replica transition.
func TestEligibleRejectsScaleDown(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := clusterv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	desired, actual := int32(1), int32(2)
	pool := &clusterv1.MachinePool{
		ObjectMeta: metav1.ObjectMeta{Namespace: "nstance-system", Name: "workers"},
		Spec: clusterv1.MachinePoolSpec{
			ClusterName: "cluster--red",
			Replicas:    &desired,
		},
		Status: clusterv1.MachinePoolStatus{Replicas: &actual, Phase: string(clusterv1.MachinePoolPhaseScalingDown)},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.Node{}, pool).Build()
	reconciler := &SleepReconciler{
		Client: kube,
		Config: &config.OperatorConfig{
			ClusterID: "cluster",
			Tenant:    "red",
			Sleep:     config.SleepPolicy{RemainingNodes: 1},
		},
	}
	if _, err := reconciler.eligible(context.Background(), "nstance-system", map[string]*grpc.ClientConn{}); err == nil {
		t.Fatal("scale-down did not block sleep")
	}
}
