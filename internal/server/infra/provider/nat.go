// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"crypto/sha256"
	"fmt"
)

// NATNetworkTag returns a stable provider-safe tag for one tenant subnet.
func NATNetworkTag(clusterID, tenant, subnetID string) string {
	digest := sha256.Sum256([]byte(clusterID + "\x00" + tenant + "\x00" + subnetID))
	return fmt.Sprintf("nstance-nat-%x", digest[:8])
}
