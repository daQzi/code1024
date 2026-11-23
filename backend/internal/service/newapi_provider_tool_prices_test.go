package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/provider"
	"github.com/stretchr/testify/require"
)

type providerToolPriceRepo struct{ modelModifierSettingsFixture }

func (r *providerToolPriceRepo) GetValue(ctx context.Context, key string) (string, error) {
	if key != providerToolPriceSettingKey {
		return "", ErrSettingNotFound
	}
	return r.modelModifierSettingsFixture.GetValue(ctx, key)
}

func providerToolPriceFixture() (*NewAPIPluginService, *providerToolPriceRepo) {
	r := &providerToolPriceRepo{}
	return &NewAPIPluginService{marketSettings: r}, r
}

func TestProviderToolPricesReadSaveConflictAndCopies(t *testing.T) {
	s, r := providerToolPriceFixture()
	ctx := context.Background()
	initial, err := s.ProviderToolPrices(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]float64{}, initial.Prices)
	require.NotEmpty(t, initial.Revision)
	input := ProviderToolPriceSettings{Prices: map[string]float64{"file_search": 2.5, "file_search:model-mini*": 0}, Revision: initial.Revision}
	saved, err := s.SaveProviderToolPrices(ctx, input)
	require.NoError(t, err)
	require.NotEqual(t, initial.Revision, saved.Revision)
	input.Prices["file_search"] = 999
	saved.Prices["file_search"] = 999
	u, err := s.providerToolUsage(ctx, nil, "model-large")
	require.NoError(t, err)
	price, found := u.lookup("file_search")
	require.True(t, found)
	require.Equal(t, 2.5, price)
	u, err = s.providerToolUsage(ctx, nil, "model-mini-later")
	require.NoError(t, err)
	price, found = u.lookup("file_search")
	require.True(t, found)
	require.Zero(t, price)
	_, err = s.SaveProviderToolPrices(ctx, input)
	require.ErrorIs(t, err, ErrProviderToolPricesConflict)
	require.Equal(t, 1, r.writes)
	current, err := s.ProviderToolPrices(ctx)
	require.NoError(t, err)
	current.Prices = map[string]float64{}
	_, err = s.SaveProviderToolPrices(ctx, current)
	require.NoError(t, err)
	price, found = u.lookup("file_search")
	require.True(t, found)
	require.Zero(t, price, "an in-flight request keeps the immutable old book")
	fresh, err := s.providerToolUsage(ctx, nil, "model-mini")
	require.NoError(t, err)
	_, found = fresh.lookup("file_search")
	require.False(t, found)
}

func TestProviderToolPricesInvalidMissingAndFailedRefreshNeverBecomeFree(t *testing.T) {
	s, r := providerToolPriceFixture()
	ctx := context.Background()
	value, err := s.ProviderToolPrices(ctx)
	require.NoError(t, err)
	for _, prices := range []map[string]float64{nil, {"file_search": -1}, {"file_search:model": 1, "file_search:model*": 2}} {
		_, err = s.SaveProviderToolPrices(ctx, ProviderToolPriceSettings{Prices: prices, Revision: value.Revision})
		require.Error(t, err)
	}
	require.Zero(t, r.writes)
	for _, raw := range []string{`{}`, `{"prices":null}`, `{"prices":{"file_search":1,"file_search":2}}`, `{"prices":{"file_search":"2"}}`, `not-json`} {
		r.mu.Lock()
		r.raw = &raw
		r.mu.Unlock()
		_, err = s.ProviderToolPrices(ctx)
		require.Error(t, err, raw)
	}
	r.mu.Lock()
	r.raw = nil
	r.err = errors.New("storage unavailable")
	r.mu.Unlock()
	_, err = s.ProviderToolPrices(ctx)
	require.Error(t, err)
	r.mu.Lock()
	r.err = nil
	r.loseCAS = true
	r.mu.Unlock()
	_, err = s.SaveProviderToolPrices(ctx, ProviderToolPriceSettings{Prices: map[string]float64{}, Revision: value.Revision})
	require.ErrorIs(t, err, ErrProviderToolPricesConflict)
}

func TestProviderToolPricesConcurrentEditorsOnlyOneWins(t *testing.T) {
	s, r := providerToolPriceFixture()
	value, err := s.ProviderToolPrices(context.Background())
	require.NoError(t, err)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for n := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.SaveProviderToolPrices(context.Background(), ProviderToolPriceSettings{Prices: map[string]float64{"file_search": float64(n)}, Revision: value.Revision})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	wins := 0
	for err := range errs {
		if err == nil {
			wins++
		} else {
			require.ErrorIs(t, err, ErrProviderToolPricesConflict)
		}
	}
	require.Equal(t, 1, wins)
	require.Equal(t, 1, r.writes)
}

func TestProviderToolPricesSlowReadCannotUndoSavedPrices(t *testing.T) {
	s, r := providerToolPriceFixture()
	started, release := make(chan struct{}), make(chan struct{})
	r.onRead = func(ctx context.Context, n int) error {
		if n == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	result := make(chan providerToolPricesSnapshot, 1)
	errs := make(chan error, 1)
	go func() { v, err := s.loadProviderToolPrices(context.Background(), true); result <- v; errs <- err }()
	<-started
	// The first read captured "not found" before the save; the guarded cache
	// must retry it, even though it completes after the successful DB write.
	value, _, err := s.readProviderToolPrices(context.Background())
	require.NoError(t, err)
	_, err = s.SaveProviderToolPrices(context.Background(), ProviderToolPriceSettings{Prices: map[string]float64{"file_search": 7}, Revision: value.settings.Revision})
	require.NoError(t, err)
	close(release)
	select {
	case value = <-result:
		require.NoError(t, <-errs)
		price, found := value.book.Lookup("file_search", "model")
		require.True(t, found)
		require.Equal(t, 7.0, price)
	case <-time.After(5 * time.Second):
		t.Fatal("superseded price read did not finish")
	}
}

func TestNewAPIProviderGenericToolPricesAndCapturedModel(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", path, stream), func(t *testing.T) {
				s, account, upstream := providerFixture(t, providerToolsSource(providerConverterSource()))
				r := &providerToolPriceRepo{}
				raw := `{"prices":{"file_search":99,"file_search:vendor*":2.5,"web_search":0,"custom_tool":3}}`
				r.raw = &raw
				s.marketSettings = r
				upstream.response = `{"reply":"hello","input":12,"output":5,"tools":{"file_search":3,"web_search":2,"custom_tool":1}}`
				if stream {
					upstream.contentType = "application/x-ndjson"
					upstream.response = "{\"seq\":0,\"text\":\"hello\",\"tools\":{\"file_search\":3,\"custom_tool\":1}}\n{\"seq\":1,\"finished\":true,\"input\":12,\"output\":5,\"tools\":{\"web_search\":2}}\n"
				}
				body := fmt.Sprintf(`{"model":"public-text","messages":[],"input":"hello","stream":%t,"tools":[{"type":"file_search"}]}`, stream)
				c, _ := providerContext(path, body)
				// The global price book can price search without a group fallback.
				result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), path, "")
				require.NoError(t, err)
				require.NotNil(t, result.providerToolCharges)
				require.InDelta(t, 0.0105, result.providerToolCharges.TotalUSD, 1e-12)
				require.GreaterOrEqual(t, result.SearchCount, 2)
				assertNewAPIProviderToolsBilling(t, result, account, 0.021088)
			})
		}
	}
}

func TestNewAPIProviderToolPriceAdmissionFailsBeforeAnyHTTP(t *testing.T) {
	for _, scenario := range []string{"missing-file-price", "storage-failure"} {
		s, account, upstream := providerFixture(t, providerExchangeSource(providerToolsSource(providerConverterSource())))
		r := &providerToolPriceRepo{}
		if scenario == "storage-failure" {
			r.err = errors.New("unavailable")
		}
		s.marketSettings = r
		body := `{"model":"public-text","messages":[],"tools":[{"type":"file_search"}]}`
		c, recorder := providerContext(newAPIProviderChat, body)
		_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderChat, "")
		require.Error(t, err)
		require.Empty(t, upstream.requests, "even token exchange must not run without admitted prices")
		if scenario == "storage-failure" {
			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		} else {
			require.Equal(t, http.StatusBadRequest, recorder.Code)
		}
	}
}

func TestNewAPIProviderToolPricesAtomicCountValidationAndSnapshot(t *testing.T) {
	book, err := provider.NewPriceBook(map[string]float64{"file_search": 2.5})
	require.NoError(t, err)
	u := newProviderToolUsage(nil)
	u.prices = book
	require.NoError(t, u.add(map[string]int{"file_search": 1}))
	require.Error(t, u.add(map[string]int{"file_search": 1, "unconfigured": 1}))
	require.Equal(t, map[string]int{"file_search": 1}, u.usage.Counts())
	result := &OpenAIForwardResult{}
	u.apply(result)
	require.NotNil(t, result.providerToolCharges)
	require.InDelta(t, 0.0025, result.providerToolCharges.TotalUSD, 1e-12)
	encoded, err := json.Marshal(result.providerToolCharges)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"price_per_1k":2.5`)
}

func TestNewAPIProviderNativeToolCallsUseSamePricesWithoutDoubleCharge(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			s, account, upstream := providerFixture(t, providerFixtureSource)
			r := &providerToolPriceRepo{}
			raw := `{"prices":{"file_search":2.5}}`
			r.raw = &raw
			s.marketSettings = r
			item := `{"id":"file1","type":"file_search_call","status":"completed"}`
			response := `{"id":"resp-tools","model":"vendor-text","object":"response","status":"completed","output":[` + item + `],"usage":{"input_tokens":12,"output_tokens":5}}`
			upstream.response = response
			if stream {
				upstream.contentType = "text/event-stream"
				upstream.response = "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":" + item + "}\n\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":" + item + "}\n\ndata: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
			}
			body := fmt.Sprintf(`{"model":"public-text","input":"hello","stream":%t,"tools":[{"type":"file_search"}]}`, stream)
			c, recorder := providerContext(newAPIProviderResponses, body)
			result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderResponses, "")
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.NotNil(t, result.providerToolCharges)
			require.InDelta(t, 0.0025, result.providerToolCharges.TotalUSD, 1e-12)
			assertNewAPIProviderToolsBilling(t, result, account, 0.005088)
		})
	}
}

func TestNewAPIProviderToolStreamPreservesFragmentedWireAndRejectsUnpricedCalls(t *testing.T) {
	book, err := provider.NewPriceBook(map[string]float64{"file_search": 2.5})
	require.NoError(t, err)
	u := newProviderToolUsage(nil)
	u.prices = book
	u.protocol = provider.NewProtocolTools(nil, u.lookup)
	payload := ": comment\r\ndata: {\"type\":\"response.output_item.done\",\r\ndata: \"output_index\":0,\"item\":{\"id\":\"f1\",\"type\":\"file_search_call\"}}\r\n\r\n"
	r := &newAPIProviderToolStream{ReadCloser: io.NopCloser(strings.NewReader(payload)), tools: u, path: newAPIProviderResponses}
	var out strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		out.Write(buf[:n])
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}
	require.Equal(t, payload, out.String())
	result := &OpenAIForwardResult{}
	require.NoError(t, u.apply(result))
	require.InDelta(t, 0.0025, result.providerToolCharges.TotalUSD, 1e-12)

	// An unexpected tool cannot import the native xAI price on partial usage.
	unknown := newProviderToolUsage(nil)
	unknown.protocol = provider.NewProtocolTools(nil, unknown.lookup)
	require.Error(t, unknown.observe(newAPIProviderResponses, []byte(`{"output":[{"type":"web_search_call"}]}`), false))
	result = &OpenAIForwardResult{}
	require.Error(t, unknown.apply(result))
	require.NotNil(t, result.providerToolCharges)
	require.Zero(t, result.providerToolCharges.TotalUSD)
}

func TestNewAPIProviderExplicitToolsReplaceOnlyMatchingProtocolKeys(t *testing.T) {
	for _, count := range []int{0, 2} {
		source := providerToolsSource(providerConverterSource())
		source = strings.Replace(source, `r.tools=JSON.parse(resp.body).tools;return r;`, `r.tools=JSON.parse(resp.body).tools;r.body.output.push({type:"file_search_call",id:"file1"});return r;`, 1)
		s, account, upstream := providerFixture(t, source)
		r := &providerToolPriceRepo{}
		raw := `{"prices":{"file_search":2.5}}`
		if count == 0 {
			raw = `{"prices":{}}`
		}
		r.raw = &raw
		s.marketSettings = r
		upstream.response = fmt.Sprintf(`{"reply":"hello","input":12,"output":5,"tools":{"file_search":%d,"web_search":1}}`, count)
		body := `{"model":"public-text","input":"hello"}`
		c, _ := providerContext(newAPIProviderResponses, body)
		price := 10.0
		c.Set("api_key", providerSearchKey(&price))
		result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderResponses, "")
		require.NoError(t, err)
		require.Equal(t, 1, result.SearchCount)
		require.Len(t, result.providerToolCharges.Items, 2)
		require.Equal(t, count, result.providerToolCharges.Items[0].Count)
		assertNewAPIProviderToolsBilling(t, result, account, 0.000088+2*(0.01+float64(count)*0.0025))
	}
}

func TestProviderToolPricesCancelledWaiterDoesNotCancelSharedRead(t *testing.T) {
	s, r := providerToolPriceFixture()
	started, release := make(chan struct{}), make(chan struct{})
	r.onRead = func(ctx context.Context, n int) error {
		if n == 1 {
			close(started)
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := s.loadProviderToolPrices(ctx, false); first <- err }()
	<-started
	cancel()
	require.ErrorIs(t, <-first, context.Canceled)
	second := make(chan error, 1)
	go func() { _, err := s.loadProviderToolPrices(context.Background(), false); second <- err }()
	close(release)
	select {
	case err := <-second:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("shared price read did not finish")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Equal(t, 1, r.reads)
}
