// SPDX-License-Identifier: AGPL-3.0-or-later
// Credential identity semantics follow the pinned New API Task contract.
package generation

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// A task retains only a non-secret login identity and the hashed account
// connection. Access/refresh tokens remain in encrypted account-bound storage.
type CredentialBinding struct {
	Mode       string          `json:"mode,omitempty"`
	Identity   string          `json:"identity,omitempty"`
	Connection json.RawMessage `json:"connection,omitempty"`
}

var ErrCredential = errors.New("task credential no longer belongs to its original connection or login")

func (b CredentialBinding) Validate() error {
	if b.Mode != "" && b.Mode != "static" && b.Mode != "login" {
		return ErrCredential
	}
	if b.Mode == "login" {
		if len(b.Identity) != 64 || len(b.Connection) == 0 {
			return ErrCredential
		}
		if _, err := hex.DecodeString(b.Identity); err != nil {
			return ErrCredential
		}
	} else if b.Identity != "" {
		return ErrCredential
	}
	if len(b.Connection) > 2048 || len(b.Connection) > 0 && !json.Valid(b.Connection) {
		return ErrCredential
	}
	return nil
}

func SameCredential(a, b CredentialBinding) bool {
	mode := func(value string) string {
		if value == "" {
			return "static" // Pre-upgrade tasks never received a managed grant.
		}
		return value
	}
	return mode(a.Mode) == mode(b.Mode) && a.Identity == b.Identity && (len(a.Connection) == 0 && len(b.Connection) == 0 || sameConnection(a.Connection, b.Connection))
}

func sameConnection(a, b json.RawMessage) bool {
	var left, right bytes.Buffer
	return json.Compact(&left, a) == nil && json.Compact(&right, b) == nil && bytes.Equal(left.Bytes(), right.Bytes())
}
