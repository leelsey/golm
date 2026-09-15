// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import "strings"

// RedactedMarker replaces a secret wherever one is scrubbed.
const RedactedMarker = "[redacted]"

const minRedactable = 12

// Redact removes every secret from text, replacing each with RedactedMarker.
func Redact(text string, secrets ...string) string {
	if text == "" {
		return text
	}
	for _, s := range secrets {
		if len(s) < minRedactable {
			continue
		}
		text = strings.ReplaceAll(text, s, RedactedMarker)
	}
	return text
}
