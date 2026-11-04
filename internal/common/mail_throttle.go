package common

import (
	"errors"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/resend/resend-go/v3"
)

// Mail transports refuse a send for two different throttling reasons, and
// callers treat them differently:
//
//   - ErrMailRateLimited: too many requests in a short window. Waiting a
//     moment and sending again works.
//   - ErrMailQuotaExceeded: the account's daily or monthly sending quota is
//     used up. Every further send fails until the quota resets, so a bulk run
//     should stop instead of trying the remaining recipients.
//
// SendMail returns errors that match one of these with errors.Is when the
// transport reported either; other failures match neither.
var (
	ErrMailRateLimited   = errors.New("mail transport rate limit")
	ErrMailQuotaExceeded = errors.New("mail transport sending quota exhausted")
)

// mailThrottleError keeps the transport's own error text (it is what the
// delivery archive shows) and adds the throttling kind and any wait the
// transport asked for.
type mailThrottleError struct {
	quota      bool
	retryAfter time.Duration
	err        error
}

func (e *mailThrottleError) Error() string { return e.err.Error() }
func (e *mailThrottleError) Unwrap() error { return e.err }
func (e *mailThrottleError) Is(target error) bool {
	if e.quota {
		return target == ErrMailQuotaExceeded
	}
	return target == ErrMailRateLimited
}

// MailRetryAfter is the wait the transport asked for before the next
// request, or zero when it named none.
func MailRetryAfter(err error) time.Duration {
	var throttle *mailThrottleError
	if errors.As(err, &throttle) {
		return throttle.retryAfter
	}
	return 0
}

// classifyMailError marks a transport error as rate limited or over quota
// when it is one, and returns every other error unchanged.
func classifyMailError(err error) error {
	if err == nil {
		return nil
	}
	var resendLimit *resend.RateLimitError
	if errors.As(err, &resendLimit) {
		// The HTTP API answers 429 both for too many requests per second and
		// for an exhausted daily or monthly quota; only the message tells
		// them apart, and the quota messages are the ones that name a quota.
		return &mailThrottleError{
			quota:      strings.Contains(strings.ToLower(resendLimit.Message), "quota"),
			retryAfter: parseRetryAfter(resendLimit.RetryAfter),
			err:        err,
		}
	}
	var smtpErr *textproto.Error
	if errors.As(err, &smtpErr) && isSMTPRateLimit(smtpErr) {
		return &mailThrottleError{err: err}
	}
	return err
}

// isSMTPRateLimit reports a temporary SMTP refusal that names a rate limit.
// Mailbox-full replies (452 "quota exceeded") concern the recipient, not
// the sender, and are not treated as throttling.
func isSMTPRateLimit(err *textproto.Error) bool {
	if err.Code < 400 || err.Code >= 500 {
		return false
	}
	msg := strings.ToLower(err.Msg)
	for _, phrase := range []string{"rate limit", "rate-limit", "ratelimit", "too many"} {
		if strings.Contains(msg, phrase) {
			return true
		}
	}
	return false
}

func parseRetryAfter(value string) time.Duration {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

// Retry delays for MailMeta.RateLimitRetries. A rate-limit window is
// typically one second long; the transport's Retry-After wins when it asks
// for longer, up to mailRetryMaxDelay.
var (
	mailRetryBaseDelay = time.Second
	mailRetryMaxDelay  = 10 * time.Second
	mailRetrySleep     = time.Sleep
)

func mailRetryDelay(attempt int, err error) time.Duration {
	delay := mailRetryBaseDelay << attempt
	if after := MailRetryAfter(err); after > delay {
		delay = after
	}
	if delay > mailRetryMaxDelay {
		delay = mailRetryMaxDelay
	}
	return delay
}
