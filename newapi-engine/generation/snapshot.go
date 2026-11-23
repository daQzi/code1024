// Package generation owns durable task snapshots, execution and settlement
// decisions. Hosts provide identity, transport and atomic financial storage.
package generation

import (
	"crypto/sha256"
	"encoding/hex"
)

// MonetaryScale matches the existing USD ledger. Changing it requires a
// coordinated ledger migration, not a plugin configuration change.
const MonetaryScale = 8
const MaxRequestPolicyEncrypted = 512 << 10
const MaxReportEncrypted = 256 << 10

func HashPayload(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	value := sha256.Sum256(payload)
	return hex.EncodeToString(value[:])
}
