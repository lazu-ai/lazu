package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"
	"github.com/lazu-ai/lazu/internal/types"
)

// WorkerRequest Worker请求的数据结构
type WorkerRequest struct {
	URL     string            `json:"url"`
	Key     string            `json:"key"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage   `json:"body,omitempty"`
}

// DoWorkerRequest 通过Worker发送请求
func DoWorkerRequest(req *WorkerRequest) (*http.Response, error) {
	if !system_setting.EnableWorker() {
		return nil, fmt.Errorf("worker not enabled")
	}
	if !system_setting.WorkerAllowHttpImageRequestEnabled && !strings.HasPrefix(req.URL, "https") {
		return nil, fmt.Errorf("only support https url")
	}

	// SSRF防护：验证请求URL
	fetchSetting := system_setting.GetFetchSetting()
	if err := fetchSetting.ValidateURL(req.URL); err != nil {
		return nil, downloadRefused(req.URL, err)
	}

	workerUrl := system_setting.WorkerUrl
	if !strings.HasSuffix(workerUrl, "/") {
		workerUrl += "/"
	}

	// 序列化worker请求数据
	workerPayload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal worker payload: %v", err)
	}

	return GetInternalHttpClient().Post(workerUrl, "application/json", bytes.NewBuffer(workerPayload))
}

// DoDownloadRequest fetches a URL a client supplied (an image or file the
// upstream provider only accepts as bytes). The operator's fetch policy is
// applied before the request, on every redirect, and again in the dialer.
// A refusal comes back as a *types.LazuError with ErrorCodeInvalidRequest:
// a 400 that names the policy, is never retried on another channel and
// never counts against the channel's health.
func DoDownloadRequest(originUrl string, reason ...string) (resp *http.Response, err error) {
	if system_setting.EnableWorker() {
		common.SysLog(fmt.Sprintf("downloading file from worker: %s, reason: %s", common.MaskSensitiveInfo(originUrl), strings.Join(reason, ", ")))
		req := &WorkerRequest{
			URL: originUrl,
			Key: system_setting.WorkerValidKey,
		}
		return DoWorkerRequest(req)
	}

	// SSRF防护：验证请求URL（非Worker模式）
	fetchSetting := system_setting.GetFetchSetting()
	if err := fetchSetting.ValidateURL(originUrl); err != nil {
		return nil, downloadRefused(originUrl, err)
	}

	common.SysLog(fmt.Sprintf("downloading from origin: %s, reason: %s", common.MaskSensitiveInfo(originUrl), strings.Join(reason, ", ")))
	resp, err = GetHttpClient().Get(originUrl)
	if err != nil {
		// A redirect hop or the dialer may refuse after the first check
		// passed; the rejection is wrapped inside http.Client's *url.Error.
		if common.AsURLRejected(err) != nil {
			return nil, downloadRefused(originUrl, err)
		}
		return nil, err
	}
	return resp, nil
}

// downloadRefused turns a fetch-policy rejection into the customer-facing
// error. The log line keeps the detail (which names the host the caller
// supplied, never an address an internal name resolved to); the client
// sees only the public reason.
func downloadRefused(originUrl string, err error) error {
	rejected := common.AsURLRejected(err)
	if rejected == nil {
		return fmt.Errorf("request reject: %w", err)
	}
	common.SysLog(fmt.Sprintf("download refused by fetch policy: %s: %s", common.MaskSensitiveInfo(originUrl), err.Error()))
	return types.NewError(
		fmt.Errorf("download refused by fetch policy: %w", rejected),
		types.ErrorCodeInvalidRequest,
		types.D{"reason": rejected.PublicReason()},
		types.ErrOptionWithSkipRetry(),
	)
}
