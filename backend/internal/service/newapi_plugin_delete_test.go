package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type pluginDeleteTestRepo struct {
	NewAPIPluginRepository
	version        *NewAPIPluginVersion
	getErr         error
	deleteResult   *PluginDeleteResult
	deleteErr      error
	deletedID      int64
	runnableChecks []bool
	pricingItems   []PluginAccountPricingSummary
	pricingErr     error
	pricingKey     string
}

func (r *pluginDeleteTestRepo) Get(_ context.Context, _ int64) (*NewAPIPluginVersion, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	return r.version, nil
}

func (r *pluginDeleteTestRepo) DeleteVersion(_ context.Context, id int64, isRunnable func(NewAPIPluginVersion) bool) (*PluginDeleteResult, error) {
	r.deletedID = id
	if isRunnable != nil {
		r.runnableChecks = append(r.runnableChecks, isRunnable(NewAPIPluginVersion{
			Key:  "demo-custom",
			Meta: jsplugin.Meta{Name: "Demo", Models: []string{"m1"}},
		}))
	}
	if r.deleteErr != nil {
		return nil, r.deleteErr
	}
	return r.deleteResult, nil
}

func (r *pluginDeleteTestRepo) ListPluginAccountPricing(_ context.Context, pluginKey string) ([]PluginAccountPricingSummary, error) {
	r.pricingKey = pluginKey
	if r.pricingErr != nil {
		return nil, r.pricingErr
	}
	return r.pricingItems, nil
}

func TestNewAPIPluginDeleteVersionProtectionAndDelegation(t *testing.T) {
	factories, err := PluginFactories()
	require.NoError(t, err)
	require.NotEmpty(t, factories)

	t.Run("rejects invalid id", func(t *testing.T) {
		svc := &NewAPIPluginService{repo: &pluginDeleteTestRepo{}}
		_, err := svc.DeleteVersion(context.Background(), 0)
		require.Error(t, err)
		code, _ := infraerrors.ToHTTP(err)
		require.Equal(t, http.StatusBadRequest, code)
	})

	t.Run("maps missing version to 404", func(t *testing.T) {
		svc := &NewAPIPluginService{repo: &pluginDeleteTestRepo{getErr: sql.ErrNoRows}}
		_, err := svc.DeleteVersion(context.Background(), 404)
		require.ErrorIs(t, err, ErrPluginVersionNotFound)
		code, _ := infraerrors.ToHTTP(err)
		require.Equal(t, http.StatusNotFound, code)
	})

	t.Run("blocks builtin origin", func(t *testing.T) {
		repo := &pluginDeleteTestRepo{
			version: &NewAPIPluginVersion{ID: 10, Key: "sora", Version: "1.0.0", Origin: "builtin:sora"},
		}
		svc := &NewAPIPluginService{repo: repo}
		_, err := svc.DeleteVersion(context.Background(), 10)
		require.ErrorIs(t, err, ErrPluginBuiltinProtected)
		require.Zero(t, repo.deletedID)
	})

	t.Run("blocks matching factory digest even with custom origin", func(t *testing.T) {
		factory := factories[0]
		repo := &pluginDeleteTestRepo{
			version: &NewAPIPluginVersion{ID: 11, Key: factory.Key, Version: factory.Version, SHA256: factory.SHA256, Origin: "upload"},
		}
		svc := &NewAPIPluginService{repo: repo}
		_, err := svc.DeleteVersion(context.Background(), 11)
		require.ErrorIs(t, err, ErrPluginBuiltinProtected)
		require.Zero(t, repo.deletedID)
	})

	t.Run("preserves 409 conflict when referenced", func(t *testing.T) {
		repo := &pluginDeleteTestRepo{
			version:   &NewAPIPluginVersion{ID: 12, Key: "custom-plugin", Version: "1.0.0", Origin: "upload"},
			deleteErr: PluginVersionInUseError("该自定义版本仍被引用（账号 2 个），无法删除"),
		}
		svc := &NewAPIPluginService{repo: repo}
		_, err := svc.DeleteVersion(context.Background(), 12)
		require.Error(t, err)
		code, status := infraerrors.ToHTTP(err)
		require.Equal(t, http.StatusConflict, code)
		require.Contains(t, status.Message, "账号 2 个")
	})

	t.Run("deletes custom version and returns fallback result", func(t *testing.T) {
		fallbackID := int64(7)
		repo := &pluginDeleteTestRepo{
			version: &NewAPIPluginVersion{ID: 13, Key: "custom-plugin", Version: "2.0.0", Origin: "upload"},
			deleteResult: &PluginDeleteResult{
				DeletedID:         13,
				Key:               "custom-plugin",
				Version:           "2.0.0",
				FallbackVersionID: &fallbackID,
			},
		}
		svc := &NewAPIPluginService{repo: repo}
		res, err := svc.DeleteVersion(context.Background(), 13)
		require.NoError(t, err)
		require.EqualValues(t, 13, repo.deletedID)
		require.NotNil(t, res.FallbackVersionID)
		require.EqualValues(t, 7, *res.FallbackVersionID)
		require.Len(t, repo.runnableChecks, 1)
	})
}

func TestNewAPIPluginDryRunInstalledVersion(t *testing.T) {
	src := `export const meta = {
		apiVersion: 1,
		key: "sandbox-fixture",
		name: "Sandbox Fixture",
		version: "1.0.0",
		author: { name: "Test" },
		models: ["demo-video"],
		fetchMode: "per_task",
		usageSchema: { duration: { type: "number", unit: "second" } }
	};
	export function extractUsage(req) {
		return { duration: (req && req.seconds) || 5 };
	}
	export function buildSubmitRequest(req) {
		return { method: "POST", url: "https://example.invalid/jobs", body: req };
	}
	export function parseSubmitResponse(resp) {
		return { taskId: (resp && resp.id) || "1" };
	}
	export function buildQueryRequest(id) {
		return { method: "GET", url: "https://example.invalid/jobs/" + id };
	}
	export function parseTaskResult(resp) {
		return { status: "SUCCESS", data: resp };
	}`
	sum := sha256.Sum256([]byte(src))
	digest := hex.EncodeToString(sum[:])

	t.Run("executes installed hook offline", func(t *testing.T) {
		svc, _, _ := generationPluginFixture(t, "sora")
		svc.repo = &pluginDeleteTestRepo{
			version: &NewAPIPluginVersion{ID: 21, Key: "sandbox-fixture", Version: "1.0.0", SHA256: digest, Source: src},
		}

		res, err := svc.DryRunInstalledVersion(context.Background(), 21, "extractUsage", []json.RawMessage{json.RawMessage(`{"seconds":8}`)})
		require.NoError(t, err)
		require.NotNil(t, res)
		require.JSONEq(t, `{"duration":8}`, string(res.Result))
	})

	t.Run("rejects tampered digest", func(t *testing.T) {
		svc, _, _ := generationPluginFixture(t, "sora")
		svc.repo = &pluginDeleteTestRepo{
			version: &NewAPIPluginVersion{ID: 22, Key: "sandbox-fixture", Version: "1.0.0", SHA256: "0000000000000000000000000000000000000000000000000000000000000000", Source: src},
		}

		_, err := svc.DryRunInstalledVersion(context.Background(), 22, "extractUsage", []json.RawMessage{json.RawMessage(`{}`)})
		require.Error(t, err)
	})
}

func TestNewAPIPluginListAccountPricing(t *testing.T) {
	price := decimal.RequireFromString("0.15")
	repo := &pluginDeleteTestRepo{
		pricingItems: []PluginAccountPricingSummary{
			{
				AccountID:   88,
				AccountName: "Kling Prod",
				Status:      "active",
				Schedulable: true,
				VersionID:   5,
				Version:     "1.0.0",
				PricingRules: map[string]GenerationPriceRule{
					"kling-v2": {Mode: "fixed", Unit: "second", UnitPrice: &price},
				},
			},
		},
	}
	svc := &NewAPIPluginService{repo: repo}

	_, err := svc.ListAccountPricing(context.Background(), "../invalid")
	require.Error(t, err)

	items, err := svc.ListAccountPricing(context.Background(), "kling")
	require.NoError(t, err)
	require.Equal(t, "kling", repo.pricingKey)
	require.Len(t, items, 1)
	require.Equal(t, "Kling Prod", items[0].AccountName)
}
