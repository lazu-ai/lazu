package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/types"
)

func withAutomaticDisable(t *testing.T, enabled bool) {
	t.Helper()
	previous := common.AutomaticDisableChannelEnabled
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = previous })
	common.AutomaticDisableChannelEnabled = enabled
}

// A provider account that cannot pay refuses every request until it is topped
// up, whatever status it reports that with, so the channel leaves rotation.
func TestShouldDisableChannelOnUpstreamBillingFailure(t *testing.T) {
	withAutomaticDisable(t, true)
	for _, status := range []int{http.StatusPaymentRequired, http.StatusBadGateway, http.StatusTooManyRequests} {
		err := types.NewErrorWithStatusCode(errors.New("credit exhausted"), types.ErrorCodeUpstreamBillingFailed, status)
		if !ShouldDisableChannel(constant.ChannelTypeOpenAI, err) {
			t.Errorf("status %d: billing failure did not disable the channel", status)
		}
	}
}

func TestShouldDisableChannelRespectsTheGlobalSwitch(t *testing.T) {
	withAutomaticDisable(t, false)
	err := types.NewErrorWithStatusCode(errors.New("credit exhausted"), types.ErrorCodeUpstreamBillingFailed, http.StatusPaymentRequired)
	if ShouldDisableChannel(constant.ChannelTypeOpenAI, err) {
		t.Fatal("disabled a channel with automatic disabling turned off")
	}
}

// Transient provider trouble stays with the retry policy and the route
// breaker; it must not take a channel out until an operator restores it.
func TestShouldDisableChannelIgnoresTransientFailures(t *testing.T) {
	withAutomaticDisable(t, true)
	for _, tc := range []struct {
		code   types.ErrorCode
		status int
	}{
		{types.ErrorCodeUpstreamRateLimited, http.StatusTooManyRequests},
		{types.ErrorCodeUpstreamTimeout, http.StatusGatewayTimeout},
		{types.ErrorCodeUpstreamOverloaded, http.StatusServiceUnavailable},
	} {
		err := types.NewErrorWithStatusCode(errors.New("busy"), tc.code, tc.status)
		if ShouldDisableChannel(constant.ChannelTypeOpenAI, err) {
			t.Errorf("%s disabled the channel", tc.code)
		}
	}
}
