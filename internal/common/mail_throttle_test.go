package common

import (
	"errors"
	"fmt"
	"net/textproto"
	"testing"
	"time"

	"github.com/resend/resend-go/v3"
)

func TestClassifyMailErrorSeparatesRateLimitFromQuota(t *testing.T) {
	rateLimited := classifyMailError(fmt.Errorf("Resend API error: %w", &resend.RateLimitError{
		Message:    "Too many requests. You can only make 10 requests per second.",
		RetryAfter: "1",
	}))
	if !errors.Is(rateLimited, ErrMailRateLimited) || errors.Is(rateLimited, ErrMailQuotaExceeded) {
		t.Fatalf("per-second 429 must classify as rate limited only: %v", rateLimited)
	}
	if got := MailRetryAfter(rateLimited); got != time.Second {
		t.Fatalf("Retry-After = %v, want 1s", got)
	}
	if !errors.Is(rateLimited, resend.ErrRateLimit) {
		t.Fatal("classification must keep the transport's own error reachable")
	}

	for _, message := range []string{
		"You have reached your daily email sending quota.",
		"You have reached your monthly email sending quota.",
	} {
		quota := classifyMailError(fmt.Errorf("Resend API error: %w", &resend.RateLimitError{Message: message}))
		if !errors.Is(quota, ErrMailQuotaExceeded) || errors.Is(quota, ErrMailRateLimited) {
			t.Fatalf("%q must classify as quota exceeded only: %v", message, quota)
		}
	}

	smtpThrottle := classifyMailError(&textproto.Error{Code: 421, Msg: "4.7.0 Too many messages, slow down"})
	if !errors.Is(smtpThrottle, ErrMailRateLimited) {
		t.Fatalf("SMTP 421 rate refusal must classify as rate limited: %v", smtpThrottle)
	}
	mailboxFull := classifyMailError(&textproto.Error{Code: 452, Msg: "4.2.2 Mailbox full, quota exceeded"})
	if errors.Is(mailboxFull, ErrMailRateLimited) || errors.Is(mailboxFull, ErrMailQuotaExceeded) {
		t.Fatalf("a recipient's full mailbox is not sender throttling: %v", mailboxFull)
	}

	plain := errors.New("[ERROR]: invalid from address")
	if got := classifyMailError(plain); got != plain {
		t.Fatalf("other errors must pass through unchanged, got %v", got)
	}
	if classifyMailError(nil) != nil {
		t.Fatal("nil must stay nil")
	}
	if text := rateLimited.Error(); text == "" || text != fmt.Errorf("Resend API error: %w", &resend.RateLimitError{
		Message: "Too many requests. You can only make 10 requests per second.", RetryAfter: "1",
	}).Error() {
		t.Fatalf("the archived error text must stay the transport's own: %q", text)
	}
}

// stubMailTransport replaces the transport and the retry sleep for one test.
func stubMailTransport(t *testing.T, results ...error) (calls *int, sleeps *[]time.Duration, records *[]MailRecord) {
	t.Helper()
	oldTransport, oldSleep, oldRecorder := mailTransport, mailRetrySleep, MailRecorder
	t.Cleanup(func() { mailTransport, mailRetrySleep, MailRecorder = oldTransport, oldSleep, oldRecorder })
	calls, sleeps, records = new(int), new([]time.Duration), new([]MailRecord)
	mailTransport = func(string, string, string, []EmailAttachment, map[string]string) (string, error) {
		i := *calls
		*calls++
		if i < len(results) && results[i] != nil {
			return "", results[i]
		}
		return "provider-id", nil
	}
	mailRetrySleep = func(d time.Duration) { *sleeps = append(*sleeps, d) }
	MailRecorder = func(r MailRecord) { *records = append(*records, r) }
	return calls, sleeps, records
}

func rateLimitedErr(retryAfter string) error {
	return classifyMailError(&resend.RateLimitError{Message: "Too many requests.", RetryAfter: retryAfter})
}

func TestSendMailRetriesRateLimitWithBackoffAndRecordsOnce(t *testing.T) {
	calls, sleeps, records := stubMailTransport(t, rateLimitedErr(""), rateLimitedErr("3"), nil)

	err := SendMail(MailMeta{Kind: MailKindNotice, RateLimitRetries: 3}, "subject", "a@example.com", "<p>hi</p>")
	if err != nil {
		t.Fatalf("SendMail = %v, want success after retries", err)
	}
	if *calls != 3 {
		t.Fatalf("transport called %d times, want 3", *calls)
	}
	// First wait is the base delay; the second honours the longer Retry-After.
	if want := []time.Duration{time.Second, 3 * time.Second}; fmt.Sprint(*sleeps) != fmt.Sprint(want) {
		t.Fatalf("retry waits = %v, want %v", *sleeps, want)
	}
	if len(*records) != 1 || (*records)[0].Err != nil || (*records)[0].ProviderID != "provider-id" {
		t.Fatalf("archive must get one successful record, got %+v", *records)
	}
}

func TestSendMailGivesUpAfterRetries(t *testing.T) {
	limited := rateLimitedErr("")
	calls, sleeps, records := stubMailTransport(t, limited, limited, limited, limited, limited)

	err := SendMail(MailMeta{RateLimitRetries: 2}, "subject", "a@example.com", "body")
	if !errors.Is(err, ErrMailRateLimited) {
		t.Fatalf("SendMail = %v, want the rate-limit error", err)
	}
	if *calls != 3 || len(*sleeps) != 2 {
		t.Fatalf("calls=%d sleeps=%v, want 3 calls and 2 waits", *calls, *sleeps)
	}
	if len(*records) != 1 || (*records)[0].Err == nil {
		t.Fatalf("archive must get one failed record, got %+v", *records)
	}
}

func TestSendMailDoesNotRetryQuotaOrWhenNotAsked(t *testing.T) {
	quota := classifyMailError(&resend.RateLimitError{Message: "You have reached your daily email sending quota."})
	calls, sleeps, _ := stubMailTransport(t, quota, nil)
	if err := SendMail(MailMeta{RateLimitRetries: 3}, "s", "a@example.com", "b"); !errors.Is(err, ErrMailQuotaExceeded) {
		t.Fatalf("SendMail = %v, want the quota error", err)
	}
	if *calls != 1 || len(*sleeps) != 0 {
		t.Fatalf("a quota refusal must not be retried: calls=%d sleeps=%v", *calls, *sleeps)
	}

	calls, sleeps, _ = stubMailTransport(t, rateLimitedErr(""), nil)
	if err := SendMail(MailMeta{}, "s", "a@example.com", "b"); !errors.Is(err, ErrMailRateLimited) {
		t.Fatalf("SendMail = %v, want the rate-limit error without retries", err)
	}
	if *calls != 1 || len(*sleeps) != 0 {
		t.Fatalf("zero RateLimitRetries must try once: calls=%d sleeps=%v", *calls, *sleeps)
	}
}

func TestMailRetryDelayIsCapped(t *testing.T) {
	if got := mailRetryDelay(10, nil); got != mailRetryMaxDelay {
		t.Fatalf("delay = %v, want the cap %v", got, mailRetryMaxDelay)
	}
	if got := mailRetryDelay(0, rateLimitedErr("3600")); got != mailRetryMaxDelay {
		t.Fatalf("a long Retry-After must be capped, got %v", got)
	}
}
