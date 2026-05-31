// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package proxmox

import (
	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

func convertStatus(qemuStatus string) string {
	switch qemuStatus {
	case "running":
		return provider.StatusRunning
	case "stopped":
		return provider.StatusStopped
	case "paused":
		return provider.StatusSuspended
	default:
		return provider.StatusUnknown
	}
}
