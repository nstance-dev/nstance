// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package identifiers

import (
	"fmt"
	"regexp"
)

var validateRegex = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

const validateMaxLength = 32

// Validate validates an Nstance identifier against the required format:
// lowercase alphanumeric with hyphens (no leading/trailing/consecutive hyphens)
// and at most validateMaxLength characters. The kind parameter is used in error
// messages (e.g. "cluster ID").
func Validate(kind, value string) error {
	if value == "" {
		return fmt.Errorf("%s cannot be empty", kind)
	}
	if len(value) > validateMaxLength {
		return fmt.Errorf("%s %q exceeds maximum length of %d characters", kind, value, validateMaxLength)
	}
	if !validateRegex.MatchString(value) {
		return fmt.Errorf("%s %q must contain only lowercase alphanumeric characters and hyphens, with no leading/trailing/consecutive hyphens", kind, value)
	}
	return nil
}
