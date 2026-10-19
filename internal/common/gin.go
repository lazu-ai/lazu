package common

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"
)

const (
	KeyRequestBody      = "key_request_body"
	keyRequestBodyError = "key_request_body_error"
	MaxMultipartMemory  = 110 << 20 // 110MB headroom so users can match OpenAI's 100MB reference uploads
)

func GetRequestBody(c *gin.Context) ([]byte, error) {
	if readError, ok := c.Get(keyRequestBodyError); ok {
		return nil, readError.(error)
	}
	requestBody, _ := c.Get(KeyRequestBody)
	if requestBody != nil {
		return requestBody.([]byte), nil
	}
	// bytes.Buffer grows geometrically for large bodies. Reserve only a small
	// bounded prefix from Content-Length so a forged length cannot force a
	// full-size allocation before bytes arrive. Headroom avoids doubling the
	// buffer just to discover EOF at an exactly full capacity.
	var buffer bytes.Buffer
	if c.Request.ContentLength > 0 {
		buffer.Grow(int(min(c.Request.ContentLength, 1<<20)) + bytes.MinRead)
	}
	_, err := buffer.ReadFrom(c.Request.Body)
	_ = c.Request.Body.Close()
	if err != nil {
		c.Set(keyRequestBodyError, err)
		return nil, err
	}
	body := buffer.Bytes()
	c.Set(KeyRequestBody, body)
	return body, nil
}

func UnmarshalBodyReusable(c *gin.Context, v any) error {
	requestBody, err := GetRequestBody(c)
	if err != nil {
		return err
	}
	//if DebugEnabled {
	//	println("UnmarshalBodyReusable request body:", string(requestBody))
	//}
	contentType := c.Request.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		err = Unmarshal(requestBody, &v)
	} else if strings.Contains(contentType, gin.MIMEPOSTForm) {
		err = parseFormData(requestBody, &v)
	} else if strings.Contains(contentType, gin.MIMEMultipartPOSTForm) {
		err = parseMultipartFormData(c, requestBody, &v)
	} else {
		// Other content types do not carry a structured request body with a model
		// field, so there is nothing to decode into v.
	}
	if err != nil {
		return err
	}
	// Reset request body
	c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
	return nil
}

func SetContextKey(c *gin.Context, key constant.ContextKey, value any) {
	c.Set(string(key), value)
}

func GetContextKey(c *gin.Context, key constant.ContextKey) (any, bool) {
	return c.Get(string(key))
}

func GetContextKeyString(c *gin.Context, key constant.ContextKey) string {
	return c.GetString(string(key))
}

func GetContextKeyInt(c *gin.Context, key constant.ContextKey) int {
	return c.GetInt(string(key))
}

func GetContextKeyMicroUSD(c *gin.Context, key constant.ContextKey) billing.MicroUSD {
	value, exists := c.Get(string(key))
	if !exists {
		return 0
	}
	switch typed := value.(type) {
	case billing.MicroUSD:
		return typed
	case int:
		return billing.MicroUSD(typed)
	case int64:
		return billing.MicroUSD(typed)
	case float64:
		return billing.MicroUSD(typed)
	default:
		return 0
	}
}

func GetContextKeyBool(c *gin.Context, key constant.ContextKey) bool {
	return c.GetBool(string(key))
}

func GetContextKeyStringSlice(c *gin.Context, key constant.ContextKey) []string {
	return c.GetStringSlice(string(key))
}

func GetContextKeyStringMap(c *gin.Context, key constant.ContextKey) map[string]any {
	return c.GetStringMap(string(key))
}

func GetContextKeyTime(c *gin.Context, key constant.ContextKey) time.Time {
	return c.GetTime(string(key))
}

func GetContextKeyType[T any](c *gin.Context, key constant.ContextKey) (T, bool) {
	if value, ok := c.Get(string(key)); ok {
		if v, ok := value.(T); ok {
			return v, true
		}
	}
	var t T
	return t, false
}

func ApiSuccess(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    data,
	})
}

func parseFormData(data []byte, v any) error {
	values, err := url.ParseQuery(string(data))
	if err != nil {
		return err
	}
	formMap := make(map[string]any)
	for key, vals := range values {
		if len(vals) == 1 {
			formMap[key] = vals[0]
		} else {
			formMap[key] = vals
		}
	}
	jsonData, err := json.Marshal(formMap)
	if err != nil {
		return err
	}

	return json.Unmarshal(jsonData, v)
}

func parseMultipartFormData(c *gin.Context, data []byte, v any) error {
	contentType := c.Request.Header.Get("Content-Type")
	boundary := ""
	if idx := strings.Index(contentType, "boundary="); idx != -1 {
		boundary = contentType[idx+9:]
	}

	if boundary == "" {
		return json.Unmarshal(data, v) // Fallback to JSON
	}

	reader := multipart.NewReader(bytes.NewReader(data), boundary)
	form, err := reader.ReadForm(32 << 20) // 32 MB max memory
	if err != nil {
		return err
	}
	defer form.RemoveAll()
	formMap := make(map[string]any)
	for key, vals := range form.Value {
		if len(vals) == 1 {
			formMap[key] = vals[0]
		} else {
			formMap[key] = vals
		}
	}
	jsonData, err := json.Marshal(formMap)
	if err != nil {
		return err
	}

	return json.Unmarshal(jsonData, v)
}

// RequestedModelName resolves the model a request asked for from whichever
// context key is already populated. The relay sets "model" and
// ContextKeyOriginalModel only once a channel has been selected, so errors
// raised earlier in the auth/distribute chain must fall back to
// ContextKeyRequestModel, which the distributor sets right after parsing the
// body. Returns "" when the request never named a model.
func RequestedModelName(c *gin.Context) string {
	if c == nil {
		return ""
	}
	for _, candidate := range []string{
		c.GetString("model"),
		GetContextKeyString(c, constant.ContextKeyOriginalModel),
		GetContextKeyString(c, constant.ContextKeyRequestModel),
	} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}
