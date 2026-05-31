// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"crypto/tls"
	"time"
)

const dialTimeout = 10 * time.Second
const minTLSVersion = tls.VersionTLS13
