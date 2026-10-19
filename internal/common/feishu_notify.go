package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// FeishuBotWebhook returns the configured Feishu bot webhook URL, or ""
// when notifications are disabled.
func FeishuBotWebhook() string {
	return strings.TrimSpace(os.Getenv("FEISHU_BOT_WEBHOOK"))
}

// PostFeishuText sends a plain-text message to the Feishu bot webhook.
// Notification failures are logged and returned so callers can avoid
// advancing persistent delivery state until Feishu accepts the message.
// Returns nil when FEISHU_BOT_WEBHOOK is unset (disabled).
func PostFeishuText(text string) error {
	url := FeishuBotWebhook()
	if url == "" {
		return nil
	}
	payload := map[string]any{
		"msg_type": "text",
		"content":  map[string]string{"text": text},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		SysError("feishu notify: marshal: " + err.Error())
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		SysError("feishu notify: new req: " + err.Error())
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		SysError("feishu notify: do: " + err.Error())
		return err
	}
	defer resp.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if readErr != nil {
		err = fmt.Errorf("feishu notify: read response: %w", readErr)
		SysError(err.Error())
		return err
	}
	if resp.StatusCode >= 400 {
		err = fmt.Errorf("feishu notify: status %d", resp.StatusCode)
		SysError(err.Error())
		return err
	}
	var result struct {
		Code          json.RawMessage `json:"code"`
		StatusCode    json.RawMessage `json:"StatusCode"`
		Message       string          `json:"msg"`
		StatusMessage string          `json:"StatusMessage"`
	}
	if len(bytes.TrimSpace(responseBody)) == 0 {
		return nil
	}
	if err = json.Unmarshal(responseBody, &result); err != nil {
		err = fmt.Errorf("feishu notify: invalid response: %w", err)
		SysError(err.Error())
		return err
	}
	code, err := feishuResponseCode(result.Code, result.StatusCode)
	if err != nil {
		SysError(err.Error())
		return err
	}
	if code != 0 {
		message := strings.TrimSpace(result.Message)
		if message == "" {
			message = strings.TrimSpace(result.StatusMessage)
		}
		err = fmt.Errorf("feishu notify: rejected code=%d message=%s", code, message)
		SysError(err.Error())
		return err
	}
	return nil
}

func feishuResponseCode(code json.RawMessage, statusCode json.RawMessage) (int64, error) {
	raw := code
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		raw = statusCode
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, nil
	}
	var numeric int64
	if err := json.Unmarshal(raw, &numeric); err == nil {
		return numeric, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, fmt.Errorf("feishu notify: invalid response code %s", string(raw))
	}
	numeric, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("feishu notify: invalid response code %q", text)
	}
	return numeric, nil
}
