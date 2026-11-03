package service

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
)

// fakeNoticeSender stands in for the transport during one test. fail
// decides the result of the n-th call (0-based).
type fakeNoticeSender struct {
	mu          sync.Mutex
	calls       int
	inFlight    int
	maxInFlight int
	metas       []common.MailMeta
	fail        func(n int) error
}

func (f *fakeNoticeSender) send(meta common.MailMeta, _ string, _ string, _ string) error {
	f.mu.Lock()
	n := f.calls
	f.calls++
	f.inFlight++
	if f.inFlight > f.maxInFlight {
		f.maxInFlight = f.inFlight
	}
	f.metas = append(f.metas, meta)
	f.mu.Unlock()

	time.Sleep(time.Millisecond) // let sends overlap when concurrency allows
	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()
	if f.fail != nil {
		return f.fail(n)
	}
	return nil
}

// stubBulkMail installs the fake sender and a pacer on a frozen clock that
// records the waits instead of sleeping.
func stubBulkMail(t *testing.T, ratePerSecond float64, fail func(int) error) (*fakeNoticeSender, *[]time.Duration) {
	t.Helper()
	sender := &fakeNoticeSender{fail: fail}
	var mu sync.Mutex
	waits := &[]time.Duration{}
	start := time.Unix(1_700_000_000, 0)

	oldSend, oldPacer := sendNoticeMail, bulkMailPacer
	oldRate := email_setting.GetEmailSetting().BulkSendRatePerSecond
	t.Cleanup(func() {
		sendNoticeMail, bulkMailPacer = oldSend, oldPacer
		email_setting.GetEmailSetting().BulkSendRatePerSecond = oldRate
	})
	sendNoticeMail = sender.send
	bulkMailPacer = &mailPacer{
		now: func() time.Time { return start },
		sleep: func(d time.Duration) {
			mu.Lock()
			*waits = append(*waits, d)
			mu.Unlock()
		},
	}
	email_setting.GetEmailSetting().BulkSendRatePerSecond = ratePerSecond
	return sender, waits
}

func testRecipients(n int) []emailRecipient {
	out := make([]emailRecipient, n)
	for i := range out {
		out[i] = emailRecipient{Id: i + 1, Username: fmt.Sprintf("user%d", i+1), Email: fmt.Sprintf("user%d@example.com", i+1)}
	}
	return out
}

func testNotice() platformNotice {
	return platformNotice{Title: "Model update", Body: "Hello {{display_name}}"}
}

func TestBulkMailIsPacedAndKeepsConcurrencyLow(t *testing.T) {
	sender, waits := stubBulkMail(t, 5, nil)

	stats, err := sendEmailsToRecipients(testNotice(), testRecipients(5), &bulkMailRun{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Attempted != 5 || stats.Sent != 5 || stats.Failed != 0 || stats.Unsent != 0 || stats.Stopped != "" {
		t.Fatalf("stats = %+v, want five sent", stats)
	}
	// The clock is frozen, so each wait is the distance to that send's slot:
	// 5 per second spaces them 200ms apart.
	want := []time.Duration{200 * time.Millisecond, 400 * time.Millisecond, 600 * time.Millisecond, 800 * time.Millisecond}
	if fmt.Sprint(*waits) != fmt.Sprint(want) {
		t.Fatalf("pacer waits = %v, want %v", *waits, want)
	}
	if sender.maxInFlight > bulkMailConcurrency {
		t.Fatalf("%d sends in flight, want at most %d", sender.maxInFlight, bulkMailConcurrency)
	}
	for _, meta := range sender.metas {
		if meta.RateLimitRetries != bulkMailRateLimitRetries || meta.Kind != common.MailKindNotice {
			t.Fatalf("bulk mail must ask for rate-limit retries: %+v", meta)
		}
	}
}

func TestBulkMailPacerFallsBackToDefaultRate(t *testing.T) {
	_, waits := stubBulkMail(t, 0, nil)
	if _, err := sendEmailsToRecipients(testNotice(), testRecipients(2), &bulkMailRun{}); err != nil {
		t.Fatal(err)
	}
	want := time.Duration(float64(time.Second) / email_setting.DefaultBulkSendRatePerSecond)
	if len(*waits) != 1 || (*waits)[0] != want {
		t.Fatalf("pacer waits = %v, want [%v]", *waits, want)
	}
}

func TestBulkMailCountsFailuresAndKeepsGoing(t *testing.T) {
	_, _ = stubBulkMail(t, 1000, func(n int) error {
		if n%2 == 1 {
			return errors.New("[ERROR]: invalid recipient")
		}
		return nil
	})
	recipients := testRecipients(6)
	recipients[2].Email = "" // no address: skipped, never sent

	stats, err := sendEmailsToRecipients(testNotice(), recipients, &bulkMailRun{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Skipped != 1 || stats.Attempted != 5 || stats.Sent+stats.Failed != 5 || stats.Failed != 2 {
		t.Fatalf("stats = %+v, want 1 skipped, 5 attempted, 2 failed", stats)
	}
	if stats.Stopped != "" || stats.Unsent != 0 {
		t.Fatalf("an ordinary failure must not stop the run: %+v", stats)
	}
}

func TestBulkMailStopsWhenQuotaIsUsedUp(t *testing.T) {
	quota := fmt.Errorf("quota: %w", common.ErrMailQuotaExceeded)
	sender, _ := stubBulkMail(t, 1000, func(n int) error {
		if n >= 2 {
			return quota
		}
		return nil
	})
	run := &bulkMailRun{}
	recipients := testRecipients(20)

	stats, err := sendEmailsToRecipients(testNotice(), recipients, run)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Stopped != EmailSendStoppedQuota {
		t.Fatalf("stats = %+v, want the run stopped for quota", stats)
	}
	if stats.Sent != 2 || stats.Failed < 1 || stats.Attempted != stats.Sent+stats.Failed {
		t.Fatalf("stats = %+v, want 2 sent and the quota failures counted", stats)
	}
	// Only sends already in flight when the quota ran out may still fail.
	if stats.Failed > bulkMailConcurrency || sender.calls > 2+bulkMailConcurrency {
		t.Fatalf("the run kept sending after the quota ran out: %+v, %d calls", stats, sender.calls)
	}
	if stats.Attempted+stats.Skipped+stats.Unsent != len(recipients) {
		t.Fatalf("stats = %+v do not account for all %d readers", stats, len(recipients))
	}

	// A later batch of the same run is not tried at all.
	calls := sender.calls
	next, err := sendEmailsToRecipients(testNotice(), testRecipients(7), run)
	if err != nil {
		t.Fatal(err)
	}
	if sender.calls != calls || next.Unsent != 7 || next.Attempted != 0 || next.Stopped != EmailSendStoppedQuota {
		t.Fatalf("later batch = %+v with %d new calls, want 7 unsent and none sent", next, sender.calls-calls)
	}

	var total EmailSendStats
	total.add(stats)
	total.add(next)
	if total.Unsent != stats.Unsent+7 || total.Stopped != EmailSendStoppedQuota {
		t.Fatalf("summed stats = %+v", total)
	}
}
