// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infrastructurev1beta1 "github.com/nstance-dev/nstance/api/v1beta1"
	"github.com/nstance-dev/nstance/internal/operator/config"
	"github.com/nstance-dev/nstance/internal/operator/connection"
	"github.com/nstance-dev/nstance/internal/proto"
)

const (
	// SleepRequestAnnotation requests normal or forced cluster sleep.
	SleepRequestAnnotation = "nstance.dev/sleep-request"
	// SleepProgressAnnotation stores restart-safe all-shard transaction progress.
	SleepProgressAnnotation = "nstance.dev/sleep-progress"
	// sleepPhaseSleeping identifies forward progress.
	sleepPhaseSleeping = "sleeping"
	// sleepPhaseFinalizing identifies the checkpoint written before the coordinating shard stops.
	sleepPhaseFinalizing = "finalizing"
	// sleepPhaseCompensating identifies rollback progress.
	sleepPhaseCompensating = "compensating"
)

// SleepReconciler reconciles cluster sleep requests.
type SleepReconciler struct {
	client.Client
	ConnProvider *connection.Provider
	Recorder     record.EventRecorder
	Config       *config.OperatorConfig
	Namespace    string
	NodeName     string
	Now          func() time.Time
}

// sleepProgress remains on the Cluster until every shard is awake again.
type sleepProgress struct {
	Request    string   `json:"request"`
	Phase      string   `json:"phase"`
	Accepted   []string `json:"accepted,omitempty"`
	FinalShard string   `json:"final_shard,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

// eligibility contains normal-sleep inputs and the next scheduled workload.
type eligibility struct {
	WakeAt *time.Time
	Shard  string
}

// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines;machinepools,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nstancemachines;nstancemachinepools;nstancemachinepools/status;nstancemachines/status;nstanceshardgroups;nstanceshardgroups/status,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes;pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs;cronjobs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile executes or compensates one durable all-shard sleep request.
func (r *SleepReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if req.Name != r.Config.CAPIClusterName() {
		return ctrl.Result{}, nil
	}
	var cluster clusterv1.Cluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	request := cluster.Annotations[SleepRequestAnnotation]
	if request == "" {
		if !r.Config.Sleep.Enabled {
			return ctrl.Result{RequeueAfter: r.Config.Sleep.ReconcilePeriod.Duration}, nil
		}
		connections := r.ConnProvider.Get()
		if connections == nil {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		if _, err := r.eligible(ctx, cluster.Namespace, connections); err != nil {
			return ctrl.Result{RequeueAfter: r.Config.Sleep.ReconcilePeriod.Duration}, nil
		}
		base := cluster.DeepCopy()
		if cluster.Annotations == nil {
			cluster.Annotations = make(map[string]string)
		}
		cluster.Annotations[SleepRequestAnnotation] = r.now().UTC().Format(time.RFC3339Nano)
		if err := r.Patch(ctx, &cluster, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}
	requestTime := strings.TrimPrefix(request, "force:")
	if _, err := time.Parse(time.RFC3339Nano, requestTime); err != nil {
		return ctrl.Result{}, r.reject(ctx, &cluster, "invalid sleep request")
	}
	progress, err := decodeProgress(cluster.Annotations[SleepProgressAnnotation], request)
	if err != nil {
		return ctrl.Result{}, r.reject(ctx, &cluster, fmt.Sprintf("invalid sleep progress: %v", err))
	}
	switch progress.Phase {
	case sleepPhaseFinalizing:
		return r.restoreAfterWake(ctx, &cluster, progress)
	case sleepPhaseCompensating:
		return ctrl.Result{}, r.compensate(ctx, &cluster, progress)
	}
	connections := r.ConnProvider.Get()
	if connections == nil {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	force := strings.HasPrefix(request, "force:")
	state := eligibility{}
	if !force {
		state, err = r.eligible(ctx, cluster.Namespace, connections)
		if err != nil {
			if len(progress.Accepted) != 0 {
				progress.Phase = sleepPhaseCompensating
				progress.Reason = err.Error()
				if err := r.storeProgress(ctx, &cluster, progress); err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, r.compensate(ctx, &cluster, progress)
			}
			return ctrl.Result{}, r.reject(ctx, &cluster, err.Error())
		}
	}
	if r.NodeName != "" {
		var node corev1.Node
		if err := r.Get(ctx, types.NamespacedName{Name: r.NodeName}, &node); err != nil {
			return ctrl.Result{}, fmt.Errorf("read coordinating node: %w", err)
		}
		state.Shard = node.Labels[corev1.LabelTopologyZone]
		if state.Shard == "" {
			return ctrl.Result{}, fmt.Errorf("coordinating node %s has no topology zone", r.NodeName)
		}
	}
	shards := orderedShards(connections, state.Shard)
	finalShard := lastShard(shards)
	accepted := make(map[string]bool, len(progress.Accepted))
	for _, shard := range progress.Accepted {
		accepted[shard] = true
	}
	for _, shard := range shards {
		if accepted[shard] {
			continue
		}
		if shard == finalShard {
			progress.Phase = sleepPhaseFinalizing
			progress.FinalShard = shard
			if err := r.storeProgress(ctx, &cluster, progress); err != nil {
				return ctrl.Result{}, err
			}
		}
		var wakeAt *timestamppb.Timestamp
		if state.WakeAt != nil && shard == finalShard {
			wakeAt = timestamppb.New(*state.WakeAt)
		}
		response, callErr := proto.NewOperatorServiceClient(connections[shard]).SleepTenant(ctx, &proto.SleepTenantRequest{
			Tenant: r.Config.Tenant, IfNotBusy: !force, WakeAt: wakeAt,
		})
		if callErr != nil && shard == finalShard {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		result := response.GetResult()
		affirmed := result == proto.SleepTenantResponse_RESULT_SLEPT || result == proto.SleepTenantResponse_RESULT_ALREADY_ASLEEP
		if callErr != nil || !affirmed || response.GetStatus() != proto.TenantSleepStatus_TENANT_SLEEP_STATUS_ASLEEP {
			progress.Phase = sleepPhaseCompensating
			if !accepted[shard] {
				progress.Accepted = append(progress.Accepted, shard)
			}
			if callErr != nil {
				progress.Reason = callErr.Error()
			} else if response.GetResult() == proto.SleepTenantResponse_RESULT_BUSY {
				progress.Reason = "shard rejected sleep as busy"
			} else {
				progress.Reason = fmt.Sprintf("shard %s did not confirm sleep", shard)
			}
			if err := r.storeProgress(ctx, &cluster, progress); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, r.compensate(ctx, &cluster, progress)
		}
		if shard == finalShard {
			return ctrl.Result{RequeueAfter: r.Config.Sleep.ReconcilePeriod.Duration}, nil
		}
		progress.Accepted = append(progress.Accepted, shard)
		if err := r.storeProgress(ctx, &cluster, progress); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: r.Config.Sleep.ReconcilePeriod.Duration}, nil
}

// eligible evaluates Kubernetes-visible normal-sleep guards.
func (r *SleepReconciler) eligible(ctx context.Context, namespace string, connections map[string]*grpc.ClientConn) (eligibility, error) {
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return eligibility{}, err
	}
	if len(nodes.Items) != r.Config.Sleep.RemainingNodes {
		return eligibility{}, fmt.Errorf("normal sleep requires exactly %d node(s)", r.Config.Sleep.RemainingNodes)
	}
	var machines clusterv1.MachineList
	if err := r.List(ctx, &machines, client.InNamespace(namespace)); err != nil {
		return eligibility{}, err
	}
	for i := range machines.Items {
		machine := &machines.Items[i]
		if machine.Spec.ClusterName == r.Config.CAPIClusterName() && !machine.Status.NodeRef.IsDefined() && machine.DeletionTimestamp.IsZero() {
			return eligibility{}, fmt.Errorf("machine %s is awaiting scale-up", machine.Name)
		}
	}
	var pools clusterv1.MachinePoolList
	if err := r.List(ctx, &pools, client.InNamespace(namespace)); err != nil {
		return eligibility{}, err
	}
	nstancePools := make(map[string]bool)
	for i := range pools.Items {
		pool := &pools.Items[i]
		if pool.Spec.ClusterName != r.Config.CAPIClusterName() {
			continue
		}
		if pool.Spec.Template.Spec.InfrastructureRef.Kind == "NstanceMachinePool" {
			nstancePools[pool.Spec.Template.Spec.InfrastructureRef.Name] = true
		}
		if pool.Status.Replicas == nil || pool.Spec.Replicas == nil || *pool.Status.Replicas != *pool.Spec.Replicas ||
			pool.Status.Phase == string(clusterv1.MachinePoolPhaseScalingUp) ||
			pool.Status.Phase == string(clusterv1.MachinePoolPhaseScalingDown) ||
			pool.Status.Phase == string(clusterv1.MachinePoolPhaseScaling) {
			return eligibility{}, fmt.Errorf("MachinePool %s is scaling", pool.Name)
		}
	}
	var shardGroups infrastructurev1beta1.NstanceShardGroupList
	if err := r.List(ctx, &shardGroups, client.InNamespace(namespace)); err != nil {
		return eligibility{}, err
	}
	for i := range shardGroups.Items {
		group := &shardGroups.Items[i]
		owned := false
		for _, owner := range group.OwnerReferences {
			owned = owned || owner.Kind == "NstanceMachinePool" && nstancePools[owner.Name]
		}
		if !owned {
			continue
		}
		if group.Status.ObservedGeneration != group.Generation || group.Status.Replicas != group.Spec.Size {
			return eligibility{}, fmt.Errorf("NstanceShardGroup %s is scaling", group.Name)
		}
	}
	if err := r.jobsIdle(ctx); err != nil {
		return eligibility{}, err
	}
	seenListeners := make(map[string]bool)
	for shard, connection := range connections {
		response, err := proto.NewOperatorServiceClient(connection).GetTenantStatus(ctx, &proto.GetTenantStatusRequest{Tenant: r.Config.Tenant})
		if err != nil {
			return eligibility{}, fmt.Errorf("read shard %s activity: %w", shard, err)
		}
		for _, listener := range response.GetListeners() {
			seenListeners[listener.GetListener()] = true
			if !listener.GetAvailable() || listener.IdleSince == nil {
				return eligibility{}, fmt.Errorf("listener %s activity is unavailable", listener.GetListener())
			}
			if err := listener.IdleSince.CheckValid(); err != nil {
				return eligibility{}, fmt.Errorf("listener %s activity time is invalid: %w", listener.GetListener(), err)
			}
			window := r.Config.Sleep.Inactivity.Duration
			if configured, ok := r.Config.Sleep.ActivityWindows[listener.GetListener()]; ok {
				window = configured.Duration
			}
			if listener.IdleSince.AsTime().Add(window).After(r.now()) {
				return eligibility{}, fmt.Errorf("listener %s is still active", listener.GetListener())
			}
		}
	}
	for listener := range r.Config.Sleep.ActivityWindows {
		if !seenListeners[listener] {
			return eligibility{}, fmt.Errorf("listener %s activity is unavailable", listener)
		}
	}
	wakeAt, err := r.nextWake(ctx)
	if err != nil {
		return eligibility{}, err
	}
	if wakeAt != nil && !wakeAt.After(r.now().Add(r.Config.Sleep.CronLookAhead.Duration-r.Config.Sleep.WakeLead.Duration)) {
		return eligibility{}, fmt.Errorf("CronJob is due within look-ahead window")
	}
	return eligibility{WakeAt: wakeAt, Shard: nodes.Items[0].Labels[corev1.LabelTopologyZone]}, nil
}

// jobsIdle rejects active/deleting Jobs and suspended Jobs with live owned Pods.
func (r *SleepReconciler) jobsIdle(ctx context.Context) error {
	var jobs batchv1.JobList
	if err := r.List(ctx, &jobs); err != nil {
		return err
	}
	var pods corev1.PodList
	if err := r.List(ctx, &pods); err != nil {
		return err
	}
	for i := range jobs.Items {
		job := &jobs.Items[i]
		terminal := false
		for _, condition := range job.Status.Conditions {
			terminal = terminal || condition.Status == corev1.ConditionTrue && (condition.Type == batchv1.JobComplete || condition.Type == batchv1.JobFailed)
		}
		if terminal && job.DeletionTimestamp.IsZero() {
			continue
		}
		if job.Spec.Suspend != nil && *job.Spec.Suspend && job.DeletionTimestamp.IsZero() && !hasLiveOwnedPod(pods.Items, job.UID) {
			continue
		}
		return fmt.Errorf("job %s is nonterminal or deleting", job.Name)
	}
	return nil
}

// hasLiveOwnedPod reports whether a Job UID owns an active or terminating Pod.
func hasLiveOwnedPod(pods []corev1.Pod, uid types.UID) bool {
	for i := range pods {
		pod := &pods[i]
		owned := false
		for _, owner := range pod.OwnerReferences {
			owned = owned || owner.UID == uid
		}
		if owned && (pod.Status.Phase == corev1.PodPending || pod.Status.Phase == corev1.PodRunning || !pod.DeletionTimestamp.IsZero()) {
			return true
		}
	}
	return false
}

// nextWake returns the earliest enabled CronJob run minus wake lead.
func (r *SleepReconciler) nextWake(ctx context.Context) (*time.Time, error) {
	var cronJobs batchv1.CronJobList
	if err := r.List(ctx, &cronJobs); err != nil {
		return nil, err
	}
	now := r.now()
	var earliest *time.Time
	for i := range cronJobs.Items {
		cronJob := &cronJobs.Items[i]
		if cronJob.Spec.Suspend != nil && *cronJob.Spec.Suspend {
			continue
		}
		location := time.Local
		if cronJob.Spec.TimeZone != nil {
			var err error
			location, err = time.LoadLocation(*cronJob.Spec.TimeZone)
			if err != nil {
				return nil, fmt.Errorf("CronJob %s has invalid time zone: %w", cronJob.Name, err)
			}
		}
		next, err := nextCron(cronJob.Spec.Schedule, now, location)
		if err != nil {
			return nil, fmt.Errorf("CronJob %s: %w", cronJob.Name, err)
		}
		wake := next.Add(-r.Config.Sleep.WakeLead.Duration)
		if earliest == nil || wake.Before(*earliest) {
			earliest = &wake
		}
	}
	return earliest, nil
}

// compensate wakes every accepted shard and retains progress until all calls succeed.
func (r *SleepReconciler) compensate(ctx context.Context, cluster *clusterv1.Cluster, progress sleepProgress) error {
	connections := r.ConnProvider.Get()
	remaining := progress.Accepted[:0]
	var errs []error
	for _, shard := range progress.Accepted {
		connection, ok := connections[shard]
		if !ok {
			remaining = append(remaining, shard)
			errs = append(errs, fmt.Errorf("shard %s has no connection", shard))
			continue
		}
		response, err := proto.NewOperatorServiceClient(connection).WakeTenant(ctx, &proto.WakeTenantRequest{Tenant: r.Config.Tenant})
		if err != nil || response.GetStatus() != proto.TenantSleepStatus_TENANT_SLEEP_STATUS_AWAKE {
			remaining = append(remaining, shard)
			if err == nil {
				err = fmt.Errorf("shard returned non-awake status")
			}
			errs = append(errs, fmt.Errorf("wake shard %s: %w", shard, err))
		}
	}
	progress.Accepted = remaining
	if len(remaining) != 0 {
		if err := r.storeProgress(ctx, cluster, progress); err != nil {
			return err
		}
		return errors.Join(errs...)
	}
	reason := progress.Reason
	if err := r.finish(ctx, cluster); err != nil {
		return err
	}
	if reason != "" {
		r.Recorder.Event(cluster, corev1.EventTypeWarning, "SleepRejected", reason)
	}
	return nil
}

// storeProgress durably records progress before another remote mutation.
func (r *SleepReconciler) storeProgress(ctx context.Context, cluster *clusterv1.Cluster, progress sleepProgress) error {
	value, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	base := cluster.DeepCopy()
	if cluster.Annotations == nil {
		cluster.Annotations = map[string]string{}
	}
	cluster.Annotations[SleepProgressAnnotation] = string(value)
	return r.Patch(ctx, cluster, client.MergeFrom(base))
}

// finish removes request state after compensation or post-wake restoration.
func (r *SleepReconciler) finish(ctx context.Context, cluster *clusterv1.Cluster) error {
	base := cluster.DeepCopy()
	delete(cluster.Annotations, SleepRequestAnnotation)
	delete(cluster.Annotations, SleepProgressAnnotation)
	return r.Patch(ctx, cluster, client.MergeFrom(base))
}

// reject emits a rejection after removing a request that made no remote change.
func (r *SleepReconciler) reject(ctx context.Context, cluster *clusterv1.Cluster, reason string) error {
	if err := r.finish(ctx, cluster); err != nil {
		return err
	}
	r.Recorder.Event(cluster, corev1.EventTypeWarning, "SleepRejected", reason)
	return nil
}

// restoreAfterWake keeps the pre-shutdown checkpoint while every shard remains
// asleep, then wakes all shards after any one of them wakes.
func (r *SleepReconciler) restoreAfterWake(ctx context.Context, cluster *clusterv1.Cluster, progress sleepProgress) (ctrl.Result, error) {
	connections := r.ConnProvider.Get()
	if connections == nil {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	allAsleep := true
	for shard, connection := range connections {
		response, err := proto.NewOperatorServiceClient(connection).GetTenantStatus(ctx, &proto.GetTenantStatusRequest{Tenant: r.Config.Tenant})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("read shard %s sleep status: %w", shard, err)
		}
		allAsleep = allAsleep && response.GetStatus() == proto.TenantSleepStatus_TENANT_SLEEP_STATUS_ASLEEP
	}
	if allAsleep {
		return ctrl.Result{RequeueAfter: r.Config.Sleep.ReconcilePeriod.Duration}, nil
	}
	progress.Phase = sleepPhaseCompensating
	progress.Accepted = progress.Accepted[:0]
	progress.Reason = ""
	for shard := range connections {
		progress.Accepted = append(progress.Accepted, shard)
	}
	sort.Strings(progress.Accepted)
	if err := r.storeProgress(ctx, cluster, progress); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, r.compensate(ctx, cluster, progress)
}

// now returns the injectable current time.
func (r *SleepReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// SetupWithManager registers all resources that can change sleep eligibility.
func (r *SleepReconciler) SetupWithManager(mgr ctrl.Manager) error {
	mapToCluster := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, object client.Object) []reconcile.Request {
		namespace := object.GetNamespace()
		if namespace == "" {
			namespace = r.Namespace
		}
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: namespace, Name: r.Config.CAPIClusterName()}}}
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&clusterv1.Cluster{}, builder.WithPredicates()).
		Watches(&clusterv1.Machine{}, mapToCluster).
		Watches(&clusterv1.MachinePool{}, mapToCluster).
		Watches(&infrastructurev1beta1.NstanceMachine{}, mapToCluster).
		Watches(&infrastructurev1beta1.NstanceMachinePool{}, mapToCluster).
		Watches(&infrastructurev1beta1.NstanceShardGroup{}, mapToCluster).
		Watches(&corev1.Node{}, mapToCluster).
		Watches(&batchv1.Job{}, mapToCluster).
		Watches(&batchv1.CronJob{}, mapToCluster).
		Watches(&corev1.Pod{}, mapToCluster).
		Complete(r)
}

// decodeProgress restores progress or initializes a new forward transaction.
func decodeProgress(value, request string) (sleepProgress, error) {
	progress := sleepProgress{Request: request, Phase: sleepPhaseSleeping}
	if value == "" {
		return progress, nil
	}
	if err := json.Unmarshal([]byte(value), &progress); err != nil {
		return sleepProgress{}, err
	}
	if progress.Request != request || progress.Phase != sleepPhaseSleeping && progress.Phase != sleepPhaseFinalizing && progress.Phase != sleepPhaseCompensating {
		return sleepProgress{}, fmt.Errorf("progress does not match request")
	}
	if progress.Phase == sleepPhaseFinalizing && progress.FinalShard == "" {
		return sleepProgress{}, fmt.Errorf("finalizing progress has no final shard")
	}
	return progress, nil
}

// orderedShards sorts shards and places the coordinating shard last.
func orderedShards(connections map[string]*grpc.ClientConn, coordinating string) []string {
	shards := make([]string, 0, len(connections))
	for shard := range connections {
		if shard != coordinating {
			shards = append(shards, shard)
		}
	}
	sort.Strings(shards)
	if _, ok := connections[coordinating]; ok {
		shards = append(shards, coordinating)
	}
	return shards
}

// lastShard returns the shard that is committed after all others.
func lastShard(shards []string) string {
	if len(shards) == 0 {
		return ""
	}
	return shards[len(shards)-1]
}

// nextCron computes the next execution of a Kubernetes CronJob schedule.
func nextCron(expression string, after time.Time, location *time.Location) (time.Time, error) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	schedule, err := parser.Parse("CRON_TZ=" + location.String() + " " + expression)
	if err != nil {
		return time.Time{}, err
	}
	return schedule.Next(after), nil
}
