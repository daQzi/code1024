// Package codec bridges New API's JSON helpers to the shared moego codec.
package codec

import "github.com/MoeForge/moego/json"

func Marshal(value any) ([]byte, error)             { return json.Marshal(value) }
func Unmarshal(data []byte, value any) error        { return json.Unmarshal(data, value) }
func UnmarshalJsonStr(data string, value any) error { return json.Unmarshal([]byte(data), value) }

// MaxQuota is part of the pinned plugin usage-fact contract, not a Sub2API
// balance limit. Money is calculated by the host after unit conversion.
const MaxQuota = 1<<31 - 1
