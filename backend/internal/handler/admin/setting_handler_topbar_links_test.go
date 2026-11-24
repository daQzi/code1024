//go:build unit

package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpdateSettingsCustomTopbarLinks(t *testing.T) {
	t.Run("saves valid links with preset icon, custom svg, auto-generated ID, and default visibility", func(t *testing.T) {
		h, repo := newStepUpSwitchTestHandler(t, map[string]string{})

		rec := doUpdateSettings(t, h, map[string]any{
			"custom_topbar_links": []map[string]any{
				{
					"id":         "canvas",
					"label":      "画布入口",
					"url":        "https://qimu.code1024.cc/",
					"icon":       "palette",
					"icon_svg":   "",
					"visibility": "all",
					"sort_order": 0,
				},
				{
					"label":      "  商店入口  ",
					"url":        "https://ai.code1024.cc/",
					"icon_svg":   `<svg viewBox="0 0 24 24"><path d="M3 3h18v18H3z"/></svg>`,
					"sort_order": 1,
				},
			},
		}, nil)
		require.Equal(t, http.StatusOK, rec.Code)

		saved := dto.ParseCustomTopbarLinks(repo.values[service.SettingKeyCustomTopbarLinks])
		require.Len(t, saved, 2)
		require.Equal(t, "canvas", saved[0].ID)
		require.Equal(t, "画布入口", saved[0].Label)
		require.Equal(t, "palette", saved[0].Icon)
		require.Equal(t, "all", saved[0].Visibility)

		require.NotEmpty(t, saved[1].ID)
		require.Equal(t, "商店入口", saved[1].Label)
		require.Equal(t, "all", saved[1].Visibility)
		require.Contains(t, saved[1].IconSVG, "<svg")

		var resp struct {
			Data dto.SystemSettings `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Len(t, resp.Data.CustomTopbarLinks, 2)
	})

	t.Run("omitting custom_topbar_links preserves stored value", func(t *testing.T) {
		initial := `[{"id":"canvas","label":"画布入口","url":"https://qimu.code1024.cc/","icon":"palette","visibility":"all","sort_order":0}]`
		h, repo := newStepUpSwitchTestHandler(t, map[string]string{
			service.SettingKeyCustomTopbarLinks: initial,
		})

		rec := doUpdateSettings(t, h, map[string]any{"site_name": "Updated"}, nil)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, initial, repo.values[service.SettingKeyCustomTopbarLinks])
	})

	t.Run("explicit empty array clears stored links to []", func(t *testing.T) {
		initial := `[{"id":"canvas","label":"画布入口","url":"https://qimu.code1024.cc/","icon":"palette","visibility":"all","sort_order":0}]`
		h, repo := newStepUpSwitchTestHandler(t, map[string]string{
			service.SettingKeyCustomTopbarLinks: initial,
		})

		rec := doUpdateSettings(t, h, map[string]any{"custom_topbar_links": []any{}}, nil)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "[]", repo.values[service.SettingKeyCustomTopbarLinks])
	})

	t.Run("rejects invalid payloads", func(t *testing.T) {
		tooMany := make([]map[string]any, 11)
		for i := range tooMany {
			tooMany[i] = map[string]any{
				"label":      "Link",
				"url":        "https://example.com",
				"visibility": "all",
			}
		}

		cases := []struct {
			name    string
			payload []map[string]any
		}{
			{
				name:    "javascript URL",
				payload: []map[string]any{{"label": "Bad", "url": "javascript:alert(1)", "visibility": "all"}},
			},
			{
				name:    "relative URL",
				payload: []map[string]any{{"label": "Bad", "url": "/relative", "visibility": "all"}},
			},
			{
				name:    "empty label",
				payload: []map[string]any{{"label": "   ", "url": "https://example.com", "visibility": "all"}},
			},
			{
				name:    "invalid visibility",
				payload: []map[string]any{{"label": "Link", "url": "https://example.com", "visibility": "guest"}},
			},
			{
				name: "duplicate IDs",
				payload: []map[string]any{
					{"id": "dup", "label": "One", "url": "https://one.example.com", "visibility": "all"},
					{"id": "dup", "label": "Two", "url": "https://two.example.com", "visibility": "all"},
				},
			},
			{
				name:    "more than 10 links",
				payload: tooMany,
			},
			{
				name: "oversized icon_svg",
				payload: []map[string]any{{
					"label":      "Huge",
					"url":        "https://example.com",
					"visibility": "all",
					"icon_svg":   "<svg>" + strings.Repeat("a", 10*1024) + "</svg>",
				}},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				h, _ := newStepUpSwitchTestHandler(t, map[string]string{})
				rec := doUpdateSettings(t, h, map[string]any{
					"custom_topbar_links": tc.payload,
				}, nil)
				require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
			})
		}
	})
}
