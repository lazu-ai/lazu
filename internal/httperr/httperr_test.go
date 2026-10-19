package httperr

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

func write(t *testing.T, err error, openAI bool) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/x", nil)
	c.Set(common.RequestIdKey, "req_test")
	if openAI {
		WriteOpenAI(c, err)
	} else {
		Write(c, err)
	}
	var body map[string]any
	if decodeErr := json.Unmarshal(w.Body.Bytes(), &body); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	return w, body
}

func errorObject(body map[string]any) map[string]any {
	object, _ := body["error"].(map[string]any)
	return object
}

func TestRawErrorsNeverReachTheClient(t *testing.T) {
	w, body := write(t, errors.New(`pq: relation "users" does not exist`), false)
	if w.Code != http.StatusInternalServerError || body["success"] != false {
		t.Fatalf("status %d body %v", w.Code, body)
	}
	e := errorObject(body)
	if e["code"] != "internal_error" || e["lazu"] != "LZ-9001" || e["request_id"] != "req_test" {
		t.Fatalf("error object %v", e)
	}
	if strings.Contains(w.Body.String(), "relation") {
		t.Fatalf("cause leaked: %s", w.Body.String())
	}
	if w.Header().Get(HeaderLazuError) != "LZ-9001" {
		t.Fatalf("missing %s header", HeaderLazuError)
	}
}

func TestCodedErrorsKeepCodeStatusAndDetails(t *testing.T) {
	w, body := write(t, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "email"}), false)
	e := errorObject(body)
	if w.Code != http.StatusBadRequest || e["code"] != "invalid_request" || e["param"] != "email" || e["type"] != "invalid_request_error" {
		t.Fatalf("status %d error %v", w.Code, e)
	}
	if details, _ := e["details"].(map[string]any); details["field"] != "email" {
		t.Fatalf("details %v", e["details"])
	}
}

func TestRecordNotFoundAndInternalRows(t *testing.T) {
	w, body := write(t, gorm.ErrRecordNotFound, false)
	if w.Code != http.StatusNotFound || errorObject(body)["code"] != "resource_not_found" {
		t.Fatalf("not found: %d %v", w.Code, body)
	}
	w, body = write(t, types.Err(types.ErrorCodeQueryDataError), true)
	e := errorObject(body)
	if w.Code != http.StatusInternalServerError || e["code"] != "internal_error" || body["success"] != nil {
		t.Fatalf("internal row through OpenAI envelope: %d %v", w.Code, body)
	}
}

func TestOperatorDetailsNeverReachEitherEnvelope(t *testing.T) {
	for _, code := range []types.ErrorCode{types.ErrorCodeUpstreamAuthFailed, types.ErrorCodeChannelInvalidKey, types.ErrorCodeUpstreamBillingFailed, types.ErrorCodeQueryDataError} {
		for _, err := range []error{
			types.Err(code, types.D{"reason": "private diagnostic", "field": "private field"}),
			types.NewError(errors.New("private cause"), code, types.ErrOptionWithDetails(types.D{"reason": "private diagnostic"})),
		} {
			for _, openAI := range []bool{true, false} {
				w, body := write(t, err, openAI)
				e := errorObject(body)
				if e["details"] != nil || e["param"] != nil || strings.Contains(w.Body.String(), "private") {
					t.Fatalf("%s: private diagnostic leaked: %s", code, w.Body)
				}
			}
		}
	}
}

func TestOperatorConsoleValidationDetailsRequireAuthenticatedRole(t *testing.T) {
	for _, tc := range []struct {
		path    string
		role    int
		visible bool
	}{
		{"/api/channel/", common.RoleRootUser, true},
		{"/api/channel/", common.RoleCommonUser, false},
		{"/api/channel/", 0, false},
		{"/v1/messages", common.RoleRootUser, false},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, tc.path, nil)
		c.Set("role", tc.role)
		// A caller-supplied role header must never grant diagnostic access.
		c.Request.Header.Set("X-Lazu-Role", "100")
		_, body := Resolve(c, types.Err(types.ErrorCodeChannelLaneInvalid, types.D{"lane": "invalid-lane"}))
		if (body.Details != nil) != tc.visible {
			t.Fatalf("path=%s role=%d details=%v", tc.path, tc.role, body.Details)
		}
		_, body = Resolve(c, types.Err(types.ErrorCodeInternalError, types.D{"reason": "private"}))
		if body.Details != nil {
			t.Fatal("internal details exposed to operator")
		}
	}
}
