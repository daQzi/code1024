package replyfilter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGinBridgePreservesWriterAndPolicyFailureStatus(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "pass", true: "reject"}[reject], func(t *testing.T) {
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			original := c.Writer
			instance := &Instance{ID: "policy", Call: func(context.Context, bool, []jsplugin.ReplyOutput) (jsplugin.ReplyResult, error) {
				if reject {
					return jsplugin.ReplyResult{Action: "stop"}, nil
				}
				return jsplugin.ReplyResult{Action: "continue"}, nil
			}}
			writer, _, err := New(context.Background(), original, "openai", []*Instance{instance}, nil)
			require.NoError(t, err)
			require.Same(t, original, writer.ResponseWriter, "a framework writer keeps its existing header and size bookkeeping")
			c.Writer = writer
			c.JSON(http.StatusCreated, gin.H{"choices": []gin.H{{"message": gin.H{"content": "answer"}}}})
			_, stopped := writer.Finish()
			c.Writer = original
			original.WriteHeaderNow()
			require.Equal(t, reject, stopped)
			if reject {
				require.Equal(t, http.StatusForbidden, response.Code)
				require.NotContains(t, response.Body.String(), "answer")
			} else {
				require.Equal(t, http.StatusCreated, response.Code)
				require.Contains(t, response.Body.String(), "answer")
			}
		})
	}
}
