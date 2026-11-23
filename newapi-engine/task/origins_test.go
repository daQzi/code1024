package task

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOriginTaskIDs(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      []string
		invalid   bool
	}{
		{"absent", "", nil, false},
		{"empty", `[]`, []string{}, false},
		{"deduplicate and trim", `[" video_a ","video_b","video_a"]`, []string{"video_a", "video_b"}, false},
		{"null", `null`, nil, true},
		{"not array", `{}`, nil, true},
		{"not string", `[12]`, nil, true},
		{"empty id", `[" "]`, nil, true},
		{"too long", `["` + strings.Repeat("a", 129) + `"]`, nil, true},
		{"unicode length", `["` + strings.Repeat("字", 128) + `"]`, []string{strings.Repeat("字", 128)}, false},
		{"too many before dedup", `[` + strings.Repeat(`"video_a",`, 16) + `"video_a"]`, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ids, err := OriginTaskIDs(json.RawMessage(tc.raw))
			if tc.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, ids)
		})
	}
}
