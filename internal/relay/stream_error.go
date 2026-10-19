package relay

import (
	"encoding/json"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
)

// SafeStreamError emits an error response using the only format that is
// still legal for the current response state. Before any byte has been
// written, it returns normal JSON. After SSE has started, it can only
// append SSE frames and close the stream.
func SafeStreamError(c *gin.Context, code types.ErrorCode, details ...types.D) {
	var d map[string]any
	if len(details) > 0 {
		d = details[0]
	}
	w := types.RenderWire(code, d, 0)
	writeSafeStreamOpenAIError(c, types.RelayFormatOpenAI, w.Status, types.OpenAIError{
		Message:   w.Message,
		Type:      w.Type,
		Code:      string(w.Code),
		Lazu:      w.ID,
		Docs:      w.Docs,
		RequestID: c.GetString(common.RequestIdKey),
	})
}

func SafeStreamLazuError(c *gin.Context, err *types.LazuError) {
	SafeStreamLazuErrorForFormat(c, types.RelayFormatOpenAI, err)
}

// SafeStreamLazuErrorForFormat emits a terminal error in the frame shape the
// client's protocol actually reads. A Responses stream names every event, so an
// unnamed `data:` frame is delivered as a generic message and dropped — the
// caller would see the stream simply stop. A keep-alive ping is enough to
// commit the response before any content exists, which is exactly the slow
// upstream failure most likely to land here.
func SafeStreamLazuErrorForFormat(c *gin.Context, relayFormat types.RelayFormat, err *types.LazuError) {
	if err == nil {
		return
	}
	openAIError := err.ToOpenAIError()
	openAIError.RequestID = c.GetString(common.RequestIdKey)
	writeSafeStreamOpenAIError(c, relayFormat, err.WireStatus(), openAIError)
}

func writeSafeStreamOpenAIError(c *gin.Context, relayFormat types.RelayFormat, status int, openAIError types.OpenAIError) {
	if reqID := c.GetString(common.RequestIdKey); reqID != "" {
		c.Header("X-Request-Id", reqID)
	}
	if openAIError.Lazu != "" && !c.Writer.Written() {
		c.Header("X-Lazu-Error", openAIError.Lazu)
	}
	if !c.Writer.Written() {
		c.Header("Content-Type", "application/json; charset=utf-8")
		c.AbortWithStatusJSON(status, gin.H{"error": openAIError})
		return
	}

	if relayFormat == types.RelayFormatOpenAIResponses {
		// A Responses `error` event is flat — type/code/message/param at the
		// top level — not the chat-completions {"error": {...}} envelope. The
		// shape is load-bearing twice over: a strict client reads `message`
		// from the top level and would otherwise render an empty error, and
		// lazu's own decoder keys failure detection off the top-level `type`,
		// so an enveloped frame would not even be recognised as one. There is
		// no [DONE] sentinel in this protocol.
		event, _ := json.Marshal(struct {
			types.OpenAIError
			Type string `json:"type"`
		}{
			OpenAIError: openAIError,
			Type:        "error",
		})
		fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", event)
		c.Writer.Flush()
		c.Abort()
		return
	}

	data, _ := json.Marshal(gin.H{"error": openAIError})
	fmt.Fprintf(c.Writer, "data: %s\n\n", data)
	fmt.Fprint(c.Writer, "data: [DONE]\n\n")
	c.Writer.Flush()
	c.Abort()
}
