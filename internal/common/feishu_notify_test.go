package common

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostFeishuTextRejectsApplicationLevelFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"code":19001,"msg":"invalid webhook"}`)
	}))
	defer server.Close()
	t.Setenv("FEISHU_BOT_WEBHOOK", server.URL)

	err := PostFeishuText("test")
	if err == nil || !strings.Contains(err.Error(), "code=19001") {
		t.Fatalf("expected application-level rejection, got %v", err)
	}
}

func TestPostFeishuTextAcceptsLegacySuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"StatusCode":0,"StatusMessage":"success"}`)
	}))
	defer server.Close()
	t.Setenv("FEISHU_BOT_WEBHOOK", server.URL)

	if err := PostFeishuText("test"); err != nil {
		t.Fatalf("expected accepted response, got %v", err)
	}
}
