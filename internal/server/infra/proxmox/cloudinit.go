// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package proxmox

import (
	"context"
	"fmt"
	"strings"

	proxmox "github.com/luthermonson/go-proxmox"
)

const cloudInitDevice = "ide2"

// uploadCloudInitISO creates and attaches a NoCloud cloud-init ISO to the VM.
// It generates an ISO containing the userdata script and basic metadata (instance-id,
// hostname), uploads it to Proxmox storage, and mounts it as a CD-ROM device.
// Cloud-init in the guest will read this ISO on first boot and execute the userdata script.
func (p *Provider) uploadCloudInitISO(ctx context.Context, node *proxmox.Node, vmid int, instanceID, userdata string) error {
	vm, err := node.VirtualMachine(ctx, vmid)
	if err != nil {
		return fmt.Errorf("get VM for cloud-init: %w", err)
	}

	trimmed := strings.TrimSpace(userdata)
	if !strings.HasPrefix(trimmed, "#!") && !strings.HasPrefix(trimmed, "#cloud-config") {
		userdata = "#!/bin/bash\n" + userdata
	}

	if instanceID == "" {
		return fmt.Errorf("instanceID is required for cloud-init metadata")
	}

	metadata := fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", instanceID, instanceID)

	if err := vm.CloudInit(ctx, cloudInitDevice, userdata, metadata, "", ""); err != nil {
		return fmt.Errorf("attach cloud-init ISO: %w", err)
	}

	p.logger.Info("attached cloud-init ISO", "vmid", vmid, "device", cloudInitDevice)
	return nil
}

// deleteCloudInitISO unmounts the cloud-init ISO from the VM and deletes it from
// Proxmox storage. This is called during VM deletion to clean up cloud-init resources.
// If the VM or ISO is already gone, this is a no-op.
func (p *Provider) deleteCloudInitISO(ctx context.Context, node *proxmox.Node, vmid int) error {
	vm, err := node.VirtualMachine(ctx, vmid)
	if err != nil {
		return nil
	}

	if err := vm.UnmountCloudInitISO(ctx, cloudInitDevice); err != nil {
		p.logger.Warn("failed to unmount cloud-init ISO", "vmid", vmid, "error", err)
	}

	return nil
}
