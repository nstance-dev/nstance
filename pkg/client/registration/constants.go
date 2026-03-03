// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package registration

import (
	"crypto/tls"
	"time"
)

const dialTimeout = 10 * time.Second
const minTLSVersion = tls.VersionTLS13
