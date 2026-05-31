// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package drain

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrastructurev1beta1 "github.com/nstance-dev/nstance/api/v1beta1"
	"github.com/nstance-dev/nstance/internal/operator/node"
	"github.com/nstance-dev/nstance/internal/proto"
)

const (
	// ConditionTypeServerDeleted indicates the instance was deleted server-side.
	ConditionTypeServerDeleted = "ServerDeleted"
)

// Coordinator manages drain coordination across all shards
type Coordinator struct {
	client      client.Client
	clientset   *kubernetes.Clientset
	logger      logr.Logger
	connections map[string]*grpc.ClientConn
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

// NewCoordinator creates a new drain coordinator
func NewCoordinator(c client.Client, logger logr.Logger, connections map[string]*grpc.ClientConn, restConfig *rest.Config) (*Coordinator, error) {
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	return &Coordinator{
		client:      c,
		clientset:   clientset,
		logger:      logger,
		connections: connections,
	}, nil
}

// Start begins watching instance events from all shards
func (c *Coordinator) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	for shard, conn := range c.connections {
		c.wg.Add(1)
		go c.watchShard(ctx, shard, conn)
	}

	c.logger.Info("drain coordinator started", "shards", len(c.connections))
	return nil
}

// Stop gracefully stops the coordinator
func (c *Coordinator) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
	c.logger.Info("drain coordinator stopped")
}

// watchShard watches instance events from a single shard
func (c *Coordinator) watchShard(ctx context.Context, shard string, conn *grpc.ClientConn) {
	defer c.wg.Done()

	logger := c.logger.WithValues("shard", shard)
	logger.Info("starting instance event watcher")

	for {
		select {
		case <-ctx.Done():
			logger.Info("stopping instance event watcher")
			return
		default:
		}

		if err := c.processEventStream(ctx, shard, conn, logger); err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Error(err, "event stream error, reconnecting in 5s")
			time.Sleep(5 * time.Second)
		}
	}
}

// processEventStream processes the instance event stream from a shard via WatchInstances.
func (c *Coordinator) processEventStream(ctx context.Context, shard string, conn *grpc.ClientConn, logger logr.Logger) error {
	client := proto.NewOperatorServiceClient(conn)

	stream, err := client.WatchInstances(ctx, &emptypb.Empty{})
	if err != nil {
		return fmt.Errorf("failed to start watch: %w", err)
	}

	logger.Info("connected to instance event stream")

	for {
		event, err := stream.Recv()
		if err == io.EOF {
			logger.Info("instance event stream closed by server")
			return nil
		}
		if err != nil {
			return fmt.Errorf("stream recv error: %w", err)
		}

		logger.Info("received instance event",
			"instanceID", event.InstanceId,
			"group", event.Group,
			"status", event.Status,
			"reason", event.Reason)

		if err := c.handleEvent(ctx, shard, conn, event, logger); err != nil {
			logger.Error(err, "failed to handle event", "instanceID", event.InstanceId)
		}
	}
}

// handleEvent processes a single instance event
func (c *Coordinator) handleEvent(ctx context.Context, shard string, conn *grpc.ClientConn, event *proto.InstanceEvent, logger logr.Logger) error {
	// Handle "deleted" events: clean up K8s resources
	if event.Status == "deleted" {
		return c.handleDeletedEvent(ctx, event, logger)
	}

	// For drain events (pending_deletion, deleting), validate provider instance ID is present
	if event.ProviderInstanceId == "" {
		return fmt.Errorf("instance event missing provider instance ID for instance %s", event.InstanceId)
	}

	n, err := node.FindByProviderID(ctx, c.client, event.ProviderInstanceId)
	if err != nil {
		return fmt.Errorf("failed to find node: %w", err)
	}

	if n == nil {
		logger.Info("node not found for instance, skipping drain",
			"instanceID", event.InstanceId,
			"providerInstanceID", event.ProviderInstanceId)

		// If the node is not found, it means it never joined the cluster or is already gone.
		// We should acknowledge this immediately so the server can proceed with deletion
		// instead of waiting for the drain timeout.
		if err := c.acknowledgeDrained(ctx, conn, event.InstanceId, logger); err != nil {
			return fmt.Errorf("failed to acknowledge drain for missing node: %w", err)
		}
		logger.Info("acknowledged drain for missing node", "instanceID", event.InstanceId)

		return nil
	}

	logger.Info("found node for instance",
		"instanceID", event.InstanceId,
		"providerInstanceID", event.ProviderInstanceId,
		"node", n.Name)

	// Determine drain deadline
	var deadline time.Time
	if event.DeleteAt != nil && event.DeleteAt.IsValid() {
		deadline = event.DeleteAt.AsTime().UTC()
	} else {
		// Fallback to 5 minutes if no deadline provided
		deadline = time.Now().UTC().Add(5 * time.Minute)
	}

	// Create context with deadline for drain operations
	drainCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	if err := c.cordonNode(drainCtx, n.Name); err != nil {
		return fmt.Errorf("failed to cordon node: %w", err)
	}

	logger.Info("cordoned node", "node", n.Name)

	if err := c.drainNode(drainCtx, n.Name, logger); err != nil {
		return fmt.Errorf("failed to drain node: %w", err)
	}

	logger.Info("drained node", "node", n.Name)

	// Use original context for acknowledgement to ensure it sends even if we're close to deadline
	if err := c.acknowledgeDrained(ctx, conn, event.InstanceId, logger); err != nil {
		return fmt.Errorf("failed to acknowledge drain: %w", err)
	}

	logger.Info("acknowledged drain completion", "instanceID", event.InstanceId)

	return nil
}

// handleDeletedEvent processes a "deleted" instance event by cleaning up corresponding K8s resources.
func (c *Coordinator) handleDeletedEvent(ctx context.Context, event *proto.InstanceEvent, logger logr.Logger) error {
	logger = logger.WithValues("instanceID", event.InstanceId)

	// Find the NstanceMachine by instance ID using the field index
	var nstanceMachines infrastructurev1beta1.NstanceMachineList
	if err := c.client.List(ctx, &nstanceMachines,
		client.MatchingFields{"status.instanceID": event.InstanceId},
	); err != nil {
		return fmt.Errorf("failed to list NstanceMachines by instanceID: %w", err)
	}

	if len(nstanceMachines.Items) == 0 {
		logger.Info("no NstanceMachine found for deleted instance, already cleaned up")
		return nil
	}

	nstanceMachine := &nstanceMachines.Items[0]
	logger = logger.WithValues("nstanceMachine", nstanceMachine.Name, "namespace", nstanceMachine.Namespace)

	// Update NstanceMachine status: ready=false, add ServerDeleted condition
	nstanceMachine.Status.Ready = false

	reason := event.Reason
	if reason == "" {
		reason = "ServerDeleted"
	}

	now := metav1.Now()
	serverDeletedCondition := metav1.Condition{
		Type:               ConditionTypeServerDeleted,
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            fmt.Sprintf("Instance deleted by server: %s", reason),
		LastTransitionTime: now,
	}

	conditionSet := false
	for i, cond := range nstanceMachine.Status.Conditions {
		if cond.Type == ConditionTypeServerDeleted {
			nstanceMachine.Status.Conditions[i] = serverDeletedCondition
			conditionSet = true
			break
		}
	}
	if !conditionSet {
		nstanceMachine.Status.Conditions = append(nstanceMachine.Status.Conditions, serverDeletedCondition)
	}

	if err := c.client.Status().Update(ctx, nstanceMachine); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("NstanceMachine already deleted during status update")
			return nil
		}
		return fmt.Errorf("failed to update NstanceMachine status: %w", err)
	}

	logger.Info("updated NstanceMachine status with ServerDeleted condition")

	// Find the owning Machine via OwnerReferences
	var ownerMachine *clusterv1.Machine
	for _, ref := range nstanceMachine.OwnerReferences {
		if ref.Kind == "Machine" && ref.APIVersion == clusterv1.GroupVersion.String() {
			machine := &clusterv1.Machine{}
			if err := c.client.Get(ctx, client.ObjectKey{
				Namespace: nstanceMachine.Namespace,
				Name:      ref.Name,
			}, machine); err != nil {
				if errors.IsNotFound(err) {
					logger.Info("owning Machine already deleted", "machine", ref.Name)
					return nil
				}
				return fmt.Errorf("failed to get owning Machine: %w", err)
			}
			ownerMachine = machine
			break
		}
	}

	if ownerMachine == nil {
		// Orphaned NstanceMachine with no owning Machine — delete it directly
		logger.Info("no owning Machine found, deleting orphaned NstanceMachine directly")
		if err := c.client.Delete(ctx, nstanceMachine); err != nil {
			if errors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("failed to delete orphaned NstanceMachine: %w", err)
		}
		return nil
	}

	// Skip if Machine is already being deleted
	if !ownerMachine.DeletionTimestamp.IsZero() {
		logger.Info("owning Machine is already deleting", "machine", ownerMachine.Name)
		return nil
	}

	// Delete the Machine — CAPI's cascade handles NstanceMachine deletion
	logger.Info("deleting owning Machine", "machine", ownerMachine.Name)
	if err := c.client.Delete(ctx, ownerMachine); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("owning Machine already deleted", "machine", ownerMachine.Name)
			return nil
		}
		return fmt.Errorf("failed to delete owning Machine: %w", err)
	}

	logger.Info("deleted owning Machine, CAPI cascade will clean up NstanceMachine", "machine", ownerMachine.Name)
	return nil
}

// cordonNode marks a node as unschedulable
func (c *Coordinator) cordonNode(ctx context.Context, nodeName string) error {
	node, err := c.clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if node.Spec.Unschedulable {
		return nil
	}

	node.Spec.Unschedulable = true
	_, err = c.clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	return err
}

// drainNode evicts all pods from a node
func (c *Coordinator) drainNode(ctx context.Context, nodeName string, logger logr.Logger) error {
	podList, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName}).String(),
	})
	if err != nil {
		return err
	}

	for _, pod := range podList.Items {
		if pod.DeletionTimestamp != nil {
			continue
		}

		if isDaemonSetPod(pod) {
			logger.Info("skipping DaemonSet pod", "pod", pod.Name, "namespace", pod.Namespace)
			continue
		}

		if isMirrorPod(pod) {
			logger.Info("skipping mirror pod", "pod", pod.Name, "namespace", pod.Namespace)
			continue
		}

		logger.Info("evicting pod", "pod", pod.Name, "namespace", pod.Namespace)

		eviction := &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pod.Name,
				Namespace: pod.Namespace,
			},
		}

		if err := c.clientset.CoreV1().Pods(pod.Namespace).EvictV1(ctx, eviction); err != nil {
			logger.Error(err, "failed to evict pod", "pod", pod.Name, "namespace", pod.Namespace)
		}
	}

	if err := c.waitForPodsToTerminate(ctx, nodeName, logger); err != nil {
		return err
	}

	return nil
}

// waitForPodsToTerminate waits for all pods on a node to terminate
func (c *Coordinator) waitForPodsToTerminate(ctx context.Context, nodeName string, logger logr.Logger) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			podList, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
				FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName}).String(),
			})
			if err != nil {
				return err
			}

			remaining := 0
			for _, pod := range podList.Items {
				if !isDaemonSetPod(pod) && !isMirrorPod(pod) {
					remaining++
				}
			}

			if remaining == 0 {
				logger.Info("all pods terminated", "node", nodeName)
				return nil
			}

			logger.Info("waiting for pods to terminate", "node", nodeName, "remaining", remaining)
		}
	}
}

// acknowledgeDrained sends an acknowledgement to the server
func (c *Coordinator) acknowledgeDrained(ctx context.Context, conn *grpc.ClientConn, instanceID string, logger logr.Logger) error {
	client := proto.NewOperatorServiceClient(conn)

	req := &proto.DrainAckRequest{
		InstanceId: instanceID,
	}

	_, err := client.AcknowledgeDrained(ctx, req)
	return err
}

// isDaemonSetPod checks if a pod is owned by a DaemonSet
func isDaemonSetPod(pod corev1.Pod) bool {
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

// isMirrorPod checks if a pod is a mirror pod
func isMirrorPod(pod corev1.Pod) bool {
	_, ok := pod.Annotations["kubernetes.io/config.mirror"]
	return ok
}
