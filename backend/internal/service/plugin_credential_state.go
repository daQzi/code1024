package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

var ErrPluginCredentialConflict = errors.New("plugin credential changed or maintenance is in progress")
var ErrPluginCredentialIdentity = errors.New("upstream identity already belongs to another plugin account")

// Connection is a projection of the existing account, never a second upstream
// connection. Repositories compare it against the live account again when a
// hook finishes, so a late refresh cannot overwrite a new login or binding.
type PluginCredentialState struct {
	AccountID            int64           `json:"-"`
	PluginVersionID      int64           `json:"-"`
	Connection           json.RawMessage `json:"-"`
	Revision             int64           `json:"revision"`
	Encrypted            string          `json:"-"`
	Public               json.RawMessage `json:"-"`
	NextRunAt            time.Time       `json:"-"`
	LeaseToken           string          `json:"-"`
	OnlyIfDue            bool            `json:"-"`
	NativeDisabledAt     *time.Time      `json:"-"`
	NativeDisablePending bool            `json:"-"`
	NativeDisabledReason string          `json:"-"`
	LastDisabledRunAt    *time.Time      `json:"-"`
	RecoverNative        bool            `json:"-"`
}

type PluginCredentialRepository interface {
	ReadPluginCredential(context.Context, int64) (*PluginCredentialState, error)
	ClaimPluginCredential(context.Context, *PluginCredentialState, int64) (*PluginCredentialState, error)
	FinishPluginCredential(context.Context, *PluginCredentialState) error
	ReleasePluginCredential(context.Context, *PluginCredentialState) error
	DuePluginCredentials(context.Context) ([]int64, error)
	PluginCredentialInUse(context.Context, int64, string) (bool, error)
}

func pluginCredentialConnection(account *Account) json.RawMessage {
	values := []any{account.Platform, account.Type, account.ProxyID}
	values = append(values, account.Credentials[NewAPIPluginRevisionKey])
	for _, name := range []string{"base_url", "api_key", newAPIPluginConfigEncryptedKey} {
		var hash any
		if value, ok := account.Credentials[name].(string); ok {
			sum := sha256.Sum256([]byte(value))
			hash = hex.EncodeToString(sum[:])
		}
		values = append(values, hash)
	}
	raw, _ := json.Marshal(values)
	return raw
}
