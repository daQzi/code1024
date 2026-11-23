package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func TestModelModifierSettingsLocalAtomicCompareAndSwap(t *testing.T) {
	db := generationLocalDB(t)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	r := &settingRepository{client: client}
	ctx := context.Background()
	key := "plugin_model_modifier_exemptions"
	previous := `{"use_startup":false,"exemptions":[]}`
	next := `{"use_startup":false,"exemptions":["real@effort:high"]}`
	for _, expected := range []*string{nil, &previous} {
		var wins atomic.Int32
		var wg sync.WaitGroup
		errors := make(chan error, 8)
		value := previous
		if expected != nil {
			value = next
		}
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ok, err := r.CompareAndSwapSetting(ctx, key, expected, value)
				if ok {
					wins.Add(1)
				}
				errors <- err
			}()
		}
		wg.Wait()
		close(errors)
		for err := range errors {
			require.NoError(t, err)
		}
		require.EqualValues(t, 1, wins.Load(), "only one editor may replace the observed setting")
		stored, err := r.GetValue(ctx, key)
		require.NoError(t, err)
		require.Equal(t, value, stored)
	}
	require.NoError(t, r.Delete(ctx, key))
	ok, err := r.CompareAndSwapSetting(ctx, key, &next, previous)
	require.NoError(t, err)
	require.False(t, ok, "an update must not recreate a deleted setting")
}
