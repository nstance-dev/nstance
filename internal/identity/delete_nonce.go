// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package identity

import "github.com/nstance-dev/nstance/internal/files"

// DeleteNonce removes the nonce file from disk.
func (i *Identity) DeleteNonce() error {
	return files.DeleteFile(i.dir, nonceFilename)
}
