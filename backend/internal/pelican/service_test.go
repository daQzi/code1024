package pelican

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mockService(t *testing.T) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()); _ = db.Close() })
	return &Service{db: db}, mock
}

func TestRunRejectsConcurrentReplicaBeforeReadingKeys(t *testing.T) {
	s, mock := mockService(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT pg_try_advisory_xact_lock").WithArgs(int64(runLockID)).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(false))
	mock.ExpectRollback()
	_, err := s.Run(context.Background())
	require.ErrorContains(t, err, "其他实例")
	require.NoError(t, s.acquire(), "failed runs must release the local lock")
	s.release()
}

func TestSchedulerRechecksIntervalUnderSharedLock(t *testing.T) {
	s, mock := mockService(t)
	cfg := DefaultConfig()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT pg_try_advisory_xact_lock").WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectQuery("SELECT data FROM pelican_config").WithArgs(configRowID).WillReturnRows(sqlmock.NewRows([]string{"data"}).AddRow(raw))
	mock.ExpectRollback()
	report, err := s.runInner(context.Background(), true)
	require.NoError(t, err)
	require.Nil(t, report, "disabled scheduler must not call the gateway")
}

func TestRunOneUsesGroupBoundKeyAndFixedPrompt(t *testing.T) {
	s, mock := mockService(t)
	g := GroupConfig{GroupID: 7, APIKeyID: 9, Model: "test-model", ReasoningEffort: "high"}
	mock.ExpectQuery("SELECT k.key FROM api_keys").WithArgs(g.APIKeyID, g.GroupID).WillReturnRows(sqlmock.NewRows([]string{"key"}).AddRow("test-only-key"))
	s.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "http://127.0.0.1:8080/v1/chat/completions", r.URL.String())
		require.Equal(t, "Bearer test-only-key", r.Header.Get("Authorization"))
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, "test-model", payload["model"])
		require.Equal(t, "high", payload["reasoning_effort"])
		require.Equal(t, false, payload["stream"])
		messages, ok := payload["messages"].([]any)
		require.True(t, ok)
		require.NotEmpty(t, messages)
		firstMessage, ok := messages[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, DefaultPrompt, firstMessage["content"])
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"<svg></svg>"}}],"usage":{"prompt_tokens":10,"completion_tokens":20}}`))}, nil
	})}
	mock.ExpectExec("INSERT INTO pelican_results").WithArgs(int64(7), "test-model", "high", "ok", "<svg></svg>", "", sqlmock.AnyArg(), 10, 20).WillReturnResult(sqlmock.NewResult(1, 1))
	result := s.runOne(context.Background(), s.db, DefaultConfig(), g)
	require.Equal(t, "ok", result.Status)
}

func TestRunOneDoesNotReportSuccessWhenPersistenceFails(t *testing.T) {
	s, mock := mockService(t)
	mock.ExpectQuery("SELECT k.key FROM api_keys").WillReturnRows(sqlmock.NewRows([]string{"key"}).AddRow("test-key"))
	s.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"<svg></svg>"}}]}`))}, nil
	})}
	mock.ExpectExec("INSERT INTO pelican_results").WillReturnError(errors.New("database unavailable"))
	result := s.runOne(context.Background(), s.db, DefaultConfig(), GroupConfig{GroupID: 1, APIKeyID: 2, Model: "test"})
	require.Equal(t, "error", result.Status)
	require.ErrorContains(t, errors.New(result.Error), "入库失败")
}

func TestOptionsPropagatesDatabaseErrors(t *testing.T) {
	s, mock := mockService(t)
	mock.ExpectQuery("SELECT g.id").WillReturnError(errors.New("connection lost"))
	_, err := s.Options(context.Background())
	require.ErrorContains(t, err, "connection lost")
}

func TestExtractHTML(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"```html\n<svg></svg>\n```", "<svg></svg>"},
		{"Here is the result: <html><body>test</body></html>", "<html><body>test</body></html>"},
		{"No markup was produced", ""},
		{"```json\n{}\n```", ""},
	} {
		require.Equal(t, tt.want, extractHTML(tt.input))
	}
}

func TestCloseStopsSchedulerAndRejectsNewRuns(t *testing.T) {
	s := New(nil)
	s.StartScheduler()
	s.Close()
	require.ErrorContains(t, s.StartRun(), "已停止")
	s.Close() // cleanup remains idempotent
}
