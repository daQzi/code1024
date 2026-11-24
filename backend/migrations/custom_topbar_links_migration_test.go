package migrations

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCustomTopbarLinksMigration(t *testing.T) {
	content, err := FS.ReadFile("292_default_custom_topbar_links.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "INSERT INTO settings (key, value)")
	require.Contains(t, sql, "'custom_topbar_links'")
	require.Contains(t, sql, "ON CONFLICT (key) DO NOTHING")
	require.Contains(t, sql, "https://qimu.code1024.cc/")
	require.Contains(t, sql, "画布入口")
	require.Contains(t, sql, "https://ai.code1024.cc/")
	require.Contains(t, sql, "商店入口")

	start := strings.Index(string(content), "'[{")
	end := strings.LastIndex(string(content), "}]'")
	require.Greater(t, start, 0)
	require.Greater(t, end, start)
	jsonPayload := string(content)[start+1 : end+2]

	var links []struct {
		ID         string `json:"id"`
		Label      string `json:"label"`
		URL        string `json:"url"`
		Icon       string `json:"icon"`
		Visibility string `json:"visibility"`
		SortOrder  int    `json:"sort_order"`
	}
	require.NoError(t, json.Unmarshal([]byte(jsonPayload), &links))
	require.Len(t, links, 2)
	require.Equal(t, "画布入口", links[0].Label)
	require.Equal(t, "https://qimu.code1024.cc/", links[0].URL)
	require.Equal(t, "商店入口", links[1].Label)
	require.Equal(t, "https://ai.code1024.cc/", links[1].URL)
}
