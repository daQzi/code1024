package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
)

type pluginSourceRepo struct {
	NewAPIPluginRepository
	version *NewAPIPluginVersion
	err     error
}

func (r *pluginSourceRepo) Get(context.Context, int64) (*NewAPIPluginVersion, error) {
	return r.version, r.err
}

func TestPluginSourceReturnsExactArtifactWithoutExecution(t *testing.T) {
	text := "\ufeffthrow Error('must never execute');\r\n// 中文\n"
	hash := sha256.Sum256([]byte(text))
	v := &NewAPIPluginVersion{ID: 7, Key: "source-fixture", Version: "1.0.0", Source: text, SHA256: hex.EncodeToString(hash[:])}
	s := &NewAPIPluginService{repo: &pluginSourceRepo{version: v}}
	out, err := s.PluginSource(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, text, out.Source, "BOM, CRLF and Unicode bytes must survive exactly")
	require.Equal(t, v.SHA256, out.SHA256)
	require.EqualValues(t, 7, out.ID)
	require.Equal(t, "source-fixture", out.Key)
}

func TestPluginSourceRejectsMissingCorruptAndOversizedArtifacts(t *testing.T) {
	for _, scenario := range []string{"invalid-id", "missing", "db-missing", "db-error", "empty", "too-large", "utf8", "digest"} {
		t.Run(scenario, func(t *testing.T) {
			repo := &pluginSourceRepo{version: &NewAPIPluginVersion{Source: "throw Error('not evaluated')", SHA256: strings.Repeat("0", 64)}}
			id := int64(7)
			switch scenario {
			case "invalid-id":
				id = 0
			case "missing":
				repo.version = nil
			case "db-missing":
				repo.err = sql.ErrNoRows
			case "db-error":
				repo.err = errors.New("database unavailable")
			case "empty":
				repo.version.Source = ""
			case "too-large":
				repo.version.Source = strings.Repeat("x", runner.MaxSourceBytes+1)
			case "utf8":
				repo.version.Source = string([]byte{0xff})
			}
			out, err := (&NewAPIPluginService{repo: repo}).PluginSource(context.Background(), id)
			require.Error(t, err)
			require.Nil(t, out)
		})
	}
}

func TestPluginFactorySourceMatchesRegisteredDigest(t *testing.T) {
	s := &NewAPIPluginService{}
	for _, key := range builtin.Keys() {
		out, err := s.FactorySource(key)
		require.NoError(t, err, key)
		expected, err := builtin.Source(key)
		require.NoError(t, err)
		require.Equal(t, expected, out.Source)
		require.Equal(t, key, out.Key)
		require.Zero(t, out.ID)
	}
	for _, key := range []string{"../source", "not-installed-fixture"} {
		_, err := s.FactorySource(key)
		require.Error(t, err)
	}
}
