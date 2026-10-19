package controller

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/types"
)

// Per-target guards for the doors the per-IP limits cannot hold on their own:
// a password guesser with many addresses still aims at one account, and a
// mail bomber with many addresses still aims at one mailbox.

const (
	loginFailureScope  = "login_failure"
	loginFailureLimit  = 10
	loginFailureWindow = 15 * time.Minute

	mailRecipientMinuteScope = "mail_recipient_minute"
	mailRecipientHourScope   = "mail_recipient_hour"
	mailRecipientHourLimit   = 5
)

func abortTooManyAttempts(c *gin.Context, remaining time.Duration) {
	retryAfter := common.RetryAfterSeconds(remaining)
	c.Header("Retry-After", strconv.FormatInt(retryAfter, 10))
	httperr.Code(c, types.ErrorCodeTooManyAttempts, types.D{"retry_after_seconds": retryAfter})
}

// loginLocked refuses the attempt while the account name has used up its
// failures. The name is the one submitted, registered or not, so the answer
// says nothing about which accounts exist.
func loginLocked(c *gin.Context, account string) bool {
	count, remaining := common.AttemptCount(loginFailureScope, account)
	if count < loginFailureLimit {
		return false
	}
	abortTooManyAttempts(c, remaining)
	return true
}

func recordLoginFailure(account string) {
	common.RecordAttempt(loginFailureScope, account, loginFailureWindow)
}

func clearLoginFailures(account string) {
	common.ClearAttempts(loginFailureScope, account)
}

// allowMailTo spends one send to the recipient: at most one a minute and
// mailRecipientHourLimit an hour, whoever asks and from wherever.
func allowMailTo(c *gin.Context, email string) bool {
	if count, remaining := common.AttemptCount(mailRecipientMinuteScope, email); count >= 1 {
		abortTooManyAttempts(c, remaining)
		return false
	}
	if count, remaining := common.AttemptCount(mailRecipientHourScope, email); count >= mailRecipientHourLimit {
		abortTooManyAttempts(c, remaining)
		return false
	}
	common.RecordAttempt(mailRecipientMinuteScope, email, time.Minute)
	common.RecordAttempt(mailRecipientHourScope, email, time.Hour)
	return true
}
