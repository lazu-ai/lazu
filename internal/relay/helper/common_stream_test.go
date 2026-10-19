package helper

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestStringDataWritesDataFramesAndDone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	if err := StringData(c, `{"delta":"one"}`); err != nil {
		t.Fatalf("first StringData failed: %v", err)
	}
	if err := PingData(c); err != nil {
		t.Fatalf("PingData failed: %v", err)
	}
	Done(c)

	body := recorder.Body.String()
	if !strings.Contains(body, `data: {"delta":"one"}`) {
		t.Fatalf("data frame missing from stream: %q", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("done frame missing from stream: %q", body)
	}
}
