// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package proxmox

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	proxmox "github.com/luthermonson/go-proxmox"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

func (p *Provider) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.CreateInstanceResponse, error) {
	ta, err := extractTemplateArgs(req.Args)
	if err != nil {
		return nil, fmt.Errorf("invalid template args: %w", err)
	}

	node, err := p.selectNode(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to select node: %w", err)
	}

	vmid, err := p.nextVMID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to allocate VMID: %w", err)
	}

	templateVMID, err := p.resolveTemplateVMID(ctx, node, ta)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve template: %w", err)
	}

	template, err := node.VirtualMachine(ctx, templateVMID)
	if err != nil {
		return nil, fmt.Errorf("failed to get template VM: %w", err)
	}

	p.logger.Info("cloning template VM",
		"template_vmid", templateVMID,
		"new_vmid", vmid,
		"node", node.Name,
		"instance_id", req.InstanceID,
	)

	cloneOpts := &proxmox.VirtualMachineCloneOptions{
		NewID:   int(vmid),
		Name:    req.InstanceID,
		Full:    1,
		Storage: ta.StoragePool,
		Target:  node.Name,
	}
	if pool, ok := req.Args["Pool"].(string); ok && pool != "" {
		cloneOpts.Pool = pool
	}

	_, task, err := template.Clone(ctx, cloneOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to clone template: %w", err)
	}

	if err := task.Wait(ctx, 5*time.Second, 5*time.Minute); err != nil {
		return nil, fmt.Errorf("clone task failed: %w", err)
	}

	// pmxcfs may not have propagated the conf file to all nodes yet after the
	// clone task completes. A 1s delay matches the heuristic Proxmox uses
	// internally for cross-node conf propagation - see pve-cluster dcdb.c
	// https://github.com/proxmox/pve-cluster/blob/master/src/pmxcfs/dcdb.c#L418
	time.Sleep(1 * time.Second)

	vm, err := retry(ctx, p.logger, "get cloned VM", func() (*proxmox.VirtualMachine, error) {
		return node.VirtualMachine(ctx, int(vmid))
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get cloned VM: %w", err)
	}

	if err := p.configureVM(ctx, vm, req); err != nil {
		return nil, fmt.Errorf("failed to configure VM: %w", err)
	}

	if req.UserData != "" {
		if err := p.uploadCloudInitISO(ctx, node, int(vmid), req.InstanceID, req.UserData); err != nil {
			return nil, fmt.Errorf("failed to upload cloud-init: %w", err)
		}
	}

	// Apply association tags: nstance ownership, cluster-id, and shard.
	// Instance ID is derived from VM name, not stored as a tag.
	// "Annotation" Metadata (group, kind) are written to VM notes, not tags.
	tags := []string{"nstance"}
	if req.ClusterID != "" {
		tags = append(tags, req.ClusterID)
	}
	if req.Shard != "" {
		tags = append(tags, req.Shard)
	}

	for _, tag := range tags {
		_, err := vm.AddTag(ctx, tag)
		if err != nil {
			p.logger.Warn("failed to add tag", "tag", tag, "error", err)
		}
	}

	p.logger.Info("starting VM", "vmid", vmid)
	startTask, err := retry(ctx, p.logger, "start VM", func() (*proxmox.Task, error) {
		return vm.Start(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start VM: %w", err)
	}

	if err := startTask.Wait(ctx, 5*time.Second, 2*time.Minute); err != nil {
		p.logger.Warn("start task wait failed", "error", err)
	}

	return &provider.CreateInstanceResponse{
		InstanceID:         req.InstanceID,
		ProviderInstanceID: fmt.Sprintf("%d", vmid),
		Status:             provider.StatusPending,
		PrivateIPv4:        "",
		PrivateIPv6:        "",
		Hostname:           req.InstanceID, // Instance ID is set as local-hostname via cloud-init
		LaunchedAt:         time.Now().UTC(),
		Tags:               req.CustomTags,
	}, nil
}

func (p *Provider) configureVM(ctx context.Context, vm *proxmox.VirtualMachine, req provider.CreateInstanceRequest) error {
	args := req.Args

	var config []proxmox.VirtualMachineOption

	// Set managed notes with annotation metadata
	notes := formatManagedNotes(req.Group, req.InstanceKind, req.Template, time.Now().UTC())
	config = append(config, proxmox.VirtualMachineOption{Name: "description", Value: notes})

	if len(args) > 0 {
		if cores, ok := args["Cores"].(float64); ok {
			config = append(config, proxmox.VirtualMachineOption{Name: "cores", Value: int(cores)})
		}
		if memory, ok := args["Memory"].(float64); ok {
			config = append(config, proxmox.VirtualMachineOption{Name: "memory", Value: int(memory)})
		}
		if bridge, ok := args["Bridge"].(string); ok {
			net0 := fmt.Sprintf("virtio,bridge=%s", bridge)
			if vlan, ok := args["VLANTag"].(float64); ok && vlan > 0 {
				net0 += fmt.Sprintf(",tag=%d", int(vlan))
			}
			config = append(config, proxmox.VirtualMachineOption{Name: "net0", Value: net0})
		}
		if startOnBoot, ok := args["StartOnBoot"].(bool); ok && startOnBoot {
			config = append(config, proxmox.VirtualMachineOption{Name: "onboot", Value: 1})
		}
	}

	if len(config) > 0 {
		configTask, err := retry(ctx, p.logger, "configure VM", func() (*proxmox.Task, error) {
			return vm.Config(ctx, config...)
		})
		if err != nil {
			return fmt.Errorf("failed to configure VM: %w", err)
		}
		if err := configTask.Wait(ctx, 5*time.Second, 2*time.Minute); err != nil {
			return fmt.Errorf("config task failed: %w", err)
		}
	}

	if len(args) > 0 {
		if diskSize, ok := args["DiskSize"].(string); ok && diskSize != "" {
			resizeTask, err := retry(ctx, p.logger, "resize disk", func() (*proxmox.Task, error) {
				return vm.ResizeDisk(ctx, "scsi0", diskSize)
			})
			if err != nil {
				return fmt.Errorf("failed to resize disk: %w", err)
			}
			if err := resizeTask.Wait(ctx, 5*time.Second, 2*time.Minute); err != nil {
				return fmt.Errorf("resize disk task failed: %w", err)
			}
			p.logger.Info("resized VM disk", "vmid", vm.VMID, "size", diskSize)
		}
	}

	return nil
}

func (p *Provider) DeleteInstance(ctx context.Context, instanceID, providerInstanceID string) error {
	vmid, err := strconv.Atoi(providerInstanceID)
	if err != nil {
		return fmt.Errorf("invalid VMID: %w", err)
	}

	vm, node, err := p.findVM(ctx, vmid)
	if err != nil {
		if errors.Is(err, provider.ErrInstanceNotFound) {
			p.logger.Info("VM already deleted", "vmid", vmid)
			return nil
		}
		return err
	}

	if vm.Name != instanceID {
		p.logger.Info("VM name does not match expected instance, VMID was reused", "vmid", vmid, "expected", instanceID, "actual", vm.Name)
		return nil
	}

	if vm.Status == "running" {
		p.logger.Info("stopping VM before deletion", "vmid", vmid)
		task, err := vm.Stop(ctx)
		if err != nil {
			p.logger.Warn("failed to stop VM", "vmid", vmid, "error", err)
		} else {
			if err := task.Wait(ctx, 5*time.Second, 30*time.Second); err != nil {
				p.logger.Warn("stop task failed", "error", err)
			}
		}
	}

	if err := p.deleteCloudInitISO(ctx, node, vmid); err != nil {
		p.logger.Warn("failed to delete cloud-init ISO", "error", err)
	}

	p.logger.Info("deleting VM", "vmid", vmid)
	task, err := vm.Delete(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete VM: %w", err)
	}

	if err := task.Wait(ctx, 5*time.Second, 30*time.Second); err != nil {
		return fmt.Errorf("delete task failed: %w", err)
	}

	return nil
}

func (p *Provider) GetInstanceStatus(ctx context.Context, instanceID, providerInstanceID string) (*provider.InstanceStatus, error) {
	vmid, err := strconv.Atoi(providerInstanceID)
	if err != nil {
		return nil, fmt.Errorf("invalid VMID: %w", err)
	}

	vm, _, err := p.findVM(ctx, vmid)
	if err != nil {
		return nil, err
	}

	if vm.Name != instanceID {
		p.logger.Warn("VM name does not match instance ID",
			"vmid", providerInstanceID,
			"vm_name", vm.Name,
			"expected_instance_id", instanceID)
		return nil, provider.ErrInstanceNotFound
	}

	status := p.vmToInstanceStatus(ctx, vm, "", "")
	if status == nil {
		p.logger.Warn("VM missing nstance ownership tags",
			"vmid", providerInstanceID,
			"instance_id", instanceID)
		return nil, provider.ErrInstanceNotFound
	}
	return status, nil
}

func (p *Provider) ListInstances(ctx context.Context, req provider.ListInstancesRequest) (*provider.ListInstancesResponse, error) {
	cluster, err := p.client.Cluster(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster: %w", err)
	}

	resources, err := cluster.Resources(ctx, "vm")
	if err != nil {
		return nil, fmt.Errorf("failed to list VMs: %w", err)
	}

	var instances []*provider.InstanceStatus
	for _, res := range resources {
		if res.Type != "qemu" {
			continue
		}

		node, err := p.client.Node(ctx, res.Node)
		if err != nil {
			p.logger.Warn("failed to get node", "node", res.Node, "error", err)
			continue
		}

		vm, err := node.VirtualMachine(ctx, int(res.VMID))
		if err != nil {
			p.logger.Warn("failed to get VM", "vmid", res.VMID, "error", err)
			continue
		}

		status := p.vmToInstanceStatus(ctx, vm, req.ClusterID, req.Shard)
		if status != nil {
			instances = append(instances, status)
		}
	}

	return &provider.ListInstancesResponse{
		Instances: instances,
		NextToken: "",
		Total:     len(instances),
	}, nil
}

func (p *Provider) findVM(ctx context.Context, vmid int) (*proxmox.VirtualMachine, *proxmox.Node, error) {
	cluster, err := p.client.Cluster(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get cluster: %w", err)
	}

	resources, err := cluster.Resources(ctx, "vm")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list VMs: %w", err)
	}

	for _, res := range resources {
		if res.Type != "qemu" {
			continue
		}
		if int(res.VMID) == vmid {
			node, err := p.client.Node(ctx, res.Node)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to get node: %w", err)
			}

			vm, err := node.VirtualMachine(ctx, vmid)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to get VM: %w", err)
			}

			return vm, node, nil
		}
	}

	return nil, nil, provider.ErrInstanceNotFound
}

func (p *Provider) vmToInstanceStatus(_ context.Context, vm *proxmox.VirtualMachine, clusterID, shard string) *provider.InstanceStatus {
	vm.SplitTags()
	tagMap := parseTagsToMap(vm.Tags)

	// Check for nstance ownership marker
	if _, ok := tagMap["nstance"]; !ok {
		return nil
	}

	// Verify the VM has the expected cluster-id and shard as tags
	if clusterID != "" {
		if _, ok := tagMap[clusterID]; !ok {
			return nil
		}
	}
	if shard != "" {
		if _, ok := tagMap[shard]; !ok {
			return nil
		}
	}

	// Use the VM name as the instance ID
	instanceID := vm.Name
	if instanceID == "" || len(instanceID) != 29 {
		return nil
	}

	// Parse annotations from VM description/notes
	var annotations *provider.InstanceAnnotations
	if vm.VirtualMachineConfig != nil && vm.VirtualMachineConfig.Description != "" {
		annotations = parseManagedNotes(vm.VirtualMachineConfig.Description)
	}

	// Use CreatedAt from annotations as LaunchedAt for GC age checks
	var launchedAt time.Time
	if annotations != nil && annotations.CreatedAt != nil {
		launchedAt = *annotations.CreatedAt
	}

	return &provider.InstanceStatus{
		InstanceID:         instanceID,
		ProviderInstanceID: fmt.Sprintf("%d", uint64(vm.VMID)),
		Status:             convertStatus(vm.Status),
		Hostname:           instanceID, // Instance ID is set as local-hostname via cloud-init
		LaunchedAt:         launchedAt,
		Region:             p.config.Region,
		Zone:               p.config.Zone,
		ClusterID:          clusterID,
		Shard:              shard,
		Annotations:        annotations,
	}
}

func parseTagsToMap(tags string) map[string]string {
	result := make(map[string]string)
	for _, tag := range strings.Split(tags, ";") {
		tag = strings.TrimSpace(tag)
		if idx := strings.Index(tag, "="); idx > 0 {
			result[tag[:idx]] = tag[idx+1:]
		} else if tag != "" {
			result[tag] = ""
		}
	}
	return result
}

// Managed notes markers for Proxmox VM description field.
// Notes are informational only and never used for reconciliation.
const (
	managedNotesStart = "**DO NOT EDIT BELOW - managed by nstance**"
	managedNotesEnd   = "**DO NOT EDIT ABOVE - managed by nstance**"
)

// formatManagedNotes generates the managed notes block for VM description.
// Proxmox notes use Markdown - line breaks require two trailing spaces.
func formatManagedNotes(group, kind, template string, created time.Time) string {
	var lines []string
	lines = append(lines, managedNotesStart)
	if group != "" {
		lines = append(lines, "group: "+group)
	}
	if kind != "" {
		lines = append(lines, "kind: "+kind)
	}
	if template != "" {
		lines = append(lines, "template: "+template)
	}
	lines = append(lines, "created: "+created.UTC().Format(time.RFC3339))
	lines = append(lines, managedNotesEnd)
	// Two trailing spaces before newline = Markdown line break
	return strings.Join(lines, "  \n")
}

// parseManagedNotes extracts annotation metadata from VM description/notes.
// Returns nil if the managed notes block is not found or empty.
func parseManagedNotes(desc string) *provider.InstanceAnnotations {
	start := strings.Index(desc, managedNotesStart)
	end := strings.Index(desc, managedNotesEnd)
	if start < 0 || end < 0 || end <= start {
		return nil
	}

	block := desc[start+len(managedNotesStart) : end]
	ann := &provider.InstanceAnnotations{}

	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)

		switch k {
		case "group":
			ann.Group = v
		case "kind":
			ann.Kind = v
		case "created":
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				utc := t.UTC()
				ann.CreatedAt = &utc
			}
		}
	}

	if ann.Group == "" && ann.Kind == "" && ann.CreatedAt == nil {
		return nil
	}
	return ann
}
