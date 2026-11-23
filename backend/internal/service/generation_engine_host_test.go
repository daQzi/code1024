package service

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestGenerationEngineHostPreservesDomainErrorsAndLedgerScale(t *testing.T) {
	require.EqualValues(t, UsageBillingMonetaryScale, generation.MonetaryScale)
	require.ErrorIs(t, ErrGenerationTaskNotFound, generation.ErrTaskNotFound)
	require.Equal(t, http.StatusNotFound, infraerrors.Code(ErrGenerationTaskNotFound))
	require.Error(t, ValidateGenerationTaskSnapshot(nil))
	id, err := NewGenerationTaskID()
	require.NoError(t, err)
	zero := decimal.Zero
	task := &GenerationTask{ID: id, UserID: 1, APIKeyID: 2, GroupID: 3, AccountID: 4, PluginVersionID: 5,
		IdempotencyKey: id, Model: "fixture", UpstreamModel: "fixture", Platform: "openai", Fingerprint: strings.Repeat("a", 64), Request: json.RawMessage(`{}`),
		Connection: GenerationConnection{BaseURL: "https://example.com", Kind: "vendor"}, Price: GenerationPriceSnapshot{Rule: GenerationPriceRule{Mode: "fixed", Unit: "request", UnitPrice: &zero}},
	}
	require.NoError(t, ValidateGenerationTaskSnapshot(task))
	task.Origins = []GenerationOriginTask{{TaskID: "foreign-parent"}}
	require.ErrorIs(t, task.Validate(), generation.ErrOrigin)
	err = ValidateGenerationTaskSnapshot(task)
	require.ErrorIs(t, err, ErrGenerationOrigin)
	require.ErrorIs(t, err, generation.ErrOrigin)
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
}

func TestGenerationEngineHostStartsOnlyOneExecutorAndCannotRestartAfterStop(t *testing.T) {
	host := NewGenerationHostService(nil, nil, nil, nil, nil, nil)
	executor := generation.NewExecutor(host.TaskExecutorPorts())
	var starts atomic.Int32
	var attempts sync.WaitGroup
	for range 8 {
		attempts.Add(1)
		go func() {
			defer attempts.Done()
			if host.StartTaskExecutor(executor) == nil {
				starts.Add(1)
			}
		}()
	}
	attempts.Wait()
	require.EqualValues(t, 1, starts.Load(), "duplicate composition must not start another claim pool")
	host.Stop()
	host.Stop()
	require.Error(t, host.StartTaskExecutor(executor))
	unstarted := NewGenerationHostService(nil, nil, nil, nil, nil, nil)
	unstarted.Stop()
	require.Error(t, unstarted.StartTaskExecutor(executor))
}
