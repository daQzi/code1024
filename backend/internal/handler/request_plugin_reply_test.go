package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIReplyPolicyErrorAlreadyCommunicated(t *testing.T) {
	for _, message := range []string{`{"error":{"type":"request_processing_error"}}`, "data: {\"error\":{\"type\":\"request_processing_error\"}}\n\n"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		before := c.Writer.Size()
		failure := &service.RequestPluginFailure{Rejected: true, Class: "rejected"}
		require.False(t, openAIForwardErrorAlreadyCommunicated(c, before, failure))
		_, err := c.Writer.WriteString(message)
		require.NoError(t, err)
		require.True(t, openAIForwardErrorAlreadyCommunicated(c, before, failure), "a second generic error would corrupt the policy response")
	}
}
