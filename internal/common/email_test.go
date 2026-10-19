package common

import (
	"strings"
	"sync"
	"testing"
)

func TestEmailTransportConfiguredRequiresACompleteTransport(t *testing.T) {
	previousResend := AppConfig.Resend.APIKey
	previousServer := SMTPServer
	previousAccount := SMTPAccount
	previousToken := SMTPToken
	AppConfig.Resend.APIKey = ""
	SMTPServer = ""
	SMTPAccount = ""
	SMTPToken = ""
	t.Cleanup(func() {
		AppConfig.Resend.APIKey = previousResend
		SMTPServer = previousServer
		SMTPAccount = previousAccount
		SMTPToken = previousToken
	})

	if EmailTransportConfigured() {
		t.Fatal("an empty mail configuration must not be advertised as available")
	}
	SMTPServer = "smtp.example.com"
	SMTPAccount = "mailer@example.com"
	if EmailTransportConfigured() {
		t.Fatal("SMTP without a token is incomplete")
	}
	SMTPToken = "secret"
	if !EmailTransportConfigured() {
		t.Fatal("a complete SMTP configuration should be available")
	}
	SMTPToken = ""
	AppConfig.Resend.APIKey = "resend-key"
	if !EmailTransportConfigured() {
		t.Fatal("a Resend key should make the transport available")
	}
}

func TestSMTPFromFallbackIsLocalAndConcurrentSafe(t *testing.T) {
	previousFrom := SMTPFrom
	previousAccount := SMTPAccount
	previousServer := SMTPServer
	SMTPFrom = ""
	SMTPAccount = "not-an-email"
	SMTPServer = "smtp.invalid"
	t.Cleanup(func() {
		SMTPFrom = previousFrom
		SMTPAccount = previousAccount
		SMTPServer = previousServer
	})

	const workers = 16
	errors := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errors <- sendEmailViaSMTP("subject", "receiver@example.com", "content", nil)
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err == nil || !strings.Contains(err.Error(), "invalid SMTP account") {
			t.Fatalf("expected local sender validation error, got %v", err)
		}
	}
	if SMTPFrom != "" {
		t.Fatalf("SMTP fallback mutated global SMTPFrom to %q", SMTPFrom)
	}
}
