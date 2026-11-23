package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/stretchr/testify/require"
)

func TestRunnerMediaTextValidationBounds(t *testing.T) {
	source := &remoteMedia{}
	for _, query := range []mediaRequest{{Operation: "unknown"}, {Operation: "media", Ref: strings.Repeat("x", 65)}, {Operation: "text", Ref: "[media:0]", MaxChars: jsplugin.MaxMediaTextChars + 1}} {
		_, err := readMedia(context.Background(), source, query)
		require.Error(t, err)
	}
}
