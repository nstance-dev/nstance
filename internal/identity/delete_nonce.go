// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package identity

import "github.com/nstance-dev/nstance/internal/files"

// DeleteNonce removes the nonce file from disk.
func (i *Identity) DeleteNonce() error {
	return files.DeleteFile(i.dir, nonceFilename)
}
