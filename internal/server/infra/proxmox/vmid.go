// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package proxmox

import (
	"context"
	"fmt"
	"time"
)

// vmidCheckAttempts is the maximum number of candidate VMIDs to try before
// giving up. This bounds the loop in case the VMID space is nearly exhausted.
const vmidCheckAttempts = 100

// nextVMID allocates the next available VMID using a monotonically incrementing
// counter. On the first call the counter is seeded by scanning the cluster for
// the highest existing VMID. Subsequent calls increment from the last value.
// Each candidate is verified as free via the Proxmox CheckID API before being
// returned. On overflow past vmidMax the counter wraps back to vmidFloor.
func (p *Provider) nextVMID(ctx context.Context) (int, error) {
	if err := p.seedVMIDCounter(ctx); err != nil {
		return 0, fmt.Errorf("failed to seed VMID counter: %w", err)
	}

	cluster, err := p.client.Cluster(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get cluster: %w", err)
	}

	for range vmidCheckAttempts {
		next := p.vmidCounter.Add(1)
		if next > vmidMax {
			p.vmidCounter.Store(int64(vmidFloor))
			next = p.vmidCounter.Add(1)
		}

		vmid := int(next)
		free, err := cluster.CheckID(ctx, vmid)
		if err != nil {
			return 0, fmt.Errorf("failed to check VMID %d: %w", vmid, err)
		}
		if free {
			return vmid, nil
		}
		p.logger.Debug("VMID in use, skipping", "vmid", vmid)
	}

	return 0, fmt.Errorf("failed to find free VMID after %d attempts", vmidCheckAttempts)
}

// vmidHighWaterMarkTimeout is the maximum time to wait for the high water mark
// callback (e.g. a database query) during VMID counter seeding.
const vmidHighWaterMarkTimeout = 5 * time.Second

// seedVMIDCounter initializes the VMID counter on first use by scanning all
// cluster VMs for the highest existing VMID and, if configured, querying the
// database for the highest previously allocated VMID. The counter is set to
// max(highestClusterVMID, highestDBVMID, vmidFloor-1) so the next allocation
// starts above all of them.
func (p *Provider) seedVMIDCounter(ctx context.Context) error {
	if p.vmidCounter.Load() != 0 {
		return nil
	}

	cluster, err := p.client.Cluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}

	resources, err := cluster.Resources(ctx, "vm")
	if err != nil {
		return fmt.Errorf("failed to list VMs: %w", err)
	}

	var highest int64
	for _, res := range resources {
		if int64(res.VMID) > highest {
			highest = int64(res.VMID)
		}
	}

	if p.vmidHighWaterMark != nil {
		hwmCtx, cancel := context.WithTimeout(ctx, vmidHighWaterMarkTimeout)
		defer cancel()

		dbHighest, err := p.vmidHighWaterMark(hwmCtx)
		if err != nil {
			p.logger.Warn("failed to query VMID high water mark from local DB, using provider-known max VMID only", "error", err)
		} else if dbHighest > highest {
			highest = dbHighest
		}
	}

	if highest < int64(vmidFloor) {
		highest = int64(vmidFloor) - 1
	}

	p.vmidCounter.CompareAndSwap(0, highest)

	p.logger.Info("seeded VMID counter", "highest_vmid", highest, "floor", vmidFloor)
	return nil
}
