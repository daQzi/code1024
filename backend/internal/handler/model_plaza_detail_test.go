package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPlazaModelStableIdentityAndNonMutatingFilter(t *testing.T) {
	id := plazaModelID("openai", "vendor/model/video")
	require.Regexp(t, `^mdl_[a-f0-9]{32}$`, id)
	require.Equal(t, id, plazaModelID("openai", "vendor/model/video"))
	require.NotEqual(t, id, plazaModelID("grok", "vendor/model/video"))
	groups := []service.PlazaGroup{
		{ID: 1, Models: []service.PlazaModel{{Platform: "openai", Name: "other"}, {Platform: "openai", Name: "vendor/model/video"}}},
		{ID: 2, Models: []service.PlazaModel{{Platform: "openai", Name: "vendor/model/video"}}},
		{ID: 3, Models: []service.PlazaModel{{Platform: "grok", Name: "vendor/model/video"}}},
	}
	filtered := filterPlazaModel(groups, id)
	require.Len(t, filtered, 2)
	require.Len(t, filtered[0].Models, 1)
	require.Len(t, groups[0].Models, 2, "detail queries cannot modify the original catalog")
	filtered[0].Models[0].Name = "changed"
	require.Equal(t, "vendor/model/video", groups[0].Models[1].Name)
	require.Empty(t, filterPlazaModel(groups, "mdl_missing"))
}

func TestPlazaSummaryScopeUsesOnlyVisibleModelGroups(t *testing.T) {
	id := plazaModelID("grok", "video")
	groups := []service.PlazaGroup{
		{ID: 1, Models: []service.PlazaModel{{Platform: "grok", Name: "video"}}},
		{ID: 2, IsExclusive: true, Models: []service.PlazaModel{{Platform: "grok", Name: "video"}}},
		{ID: 3, Models: []service.PlazaModel{{Platform: "openai", Name: "other"}}},
	}
	visible := filterPlazaVisibleGroups(groups, nil, false)
	scopes := plazaSummaryScopes(visible, map[string]bool{id: true, "hidden": true})
	require.Len(t, scopes, 1)
	require.Equal(t, []int64{1}, scopes[0].GroupIDs)
	require.Equal(t, "video", scopes[0].Model)
	require.Equal(t, "grok", scopes[0].Platform)
}
