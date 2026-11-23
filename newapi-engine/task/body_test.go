package task

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskDescriptorBodyWireContract(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""}, {" null \n", ""}, {`""`, ""},
		{`"plain <>&\ntext"`, "plain <>&\ntext"},
		{`"{\"__fileRef\":\"not-a-reference\"}"`, `{"__fileRef":"not-a-reference"}`},
		{`{"z":9007199254740993,"a":"<>&"}`, `{"z":9007199254740993,"a":"<>&"}`},
		{`[{"z":1,"a":2}]`, `[{"z":1,"a":2}]`},
	} {
		t.Run(tc.input, func(t *testing.T) {
			body, err := (Request{Body: json.RawMessage(tc.input)}).ResolveBody(context.Background(), nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, string(body))
		})
	}
	_, err := (Request{Body: json.RawMessage(`"bad`)}).ResolveBody(context.Background(), nil)
	require.Error(t, err)
	_, err = (Request{Body: json.RawMessage(`"` + strings.Repeat("a", MaxSnapshotBytes) + `"`)}).ResolveBody(context.Background(), nil)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = (Request{Body: json.RawMessage(`"text"`)}).ResolveBody(ctx, nil)
	require.ErrorIs(t, err, context.Canceled)
}

func TestTaskOrderedBodyWithFilesStillUsesSortedCodec(t *testing.T) {
	closed := false
	open := func(context.Context, string) (File, io.ReadCloser, error) {
		return File{Size: 3, MimeType: "image/png"}, &fileTestReader{Reader: strings.NewReader("abc"), close: func() { closed = true }}, nil
	}
	body, err := (Request{Body: json.RawMessage(`{"z":1,"a":{"__fileRef":"owned","encoding":"base64"}}`)}).ResolveBody(context.Background(), open)
	require.NoError(t, err)
	require.Equal(t, `{"a":"YWJj","z":1}`, string(body))
	require.True(t, closed)
	require.True(t, (Request{Body: json.RawMessage(` "text"`)}).OpaqueBody())
	require.True(t, (Request{BodyType: "multipart"}).OpaqueBody())
	require.False(t, (Request{Body: json.RawMessage(`{"x":1}`)}).OpaqueBody())
}

func TestTaskContentWireBodyDoesNotMaterializeGenerationFiles(t *testing.T) {
	request := Request{Body: json.RawMessage(`{"z":1,"a":{"__fileRef":"opaque-vendor-value","encoding":"base64"}}`)}
	body, err := request.WireBody(context.Background())
	require.NoError(t, err)
	require.Equal(t, string(request.Body), string(body), "content/fetch descriptors use their own encoded JSON, as in the pinned adaptor")
	_, err = (Request{Body: json.RawMessage(`not JSON`)}).WireBody(context.Background())
	require.Error(t, err)
}
