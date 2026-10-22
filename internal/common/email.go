package common

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"maps"
	"net/smtp"
	"slices"
	"strings"
	"time"

	"github.com/resend/resend-go/v3"
)

func generateMessageID(from string) (string, error) {
	split := strings.Split(strings.TrimSpace(from), "@")
	if len(split) != 2 || split[0] == "" || split[1] == "" {
		return "", fmt.Errorf("invalid SMTP account")
	}
	domain := split[1]
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), GetRandomString(12), domain), nil
}

// EmailAttachment represents an email attachment
type EmailAttachment struct {
	Filename string
	Content  []byte
}

func SendEmail(subject string, receiver string, content string) error {
	return SendEmailWithAttachment(subject, receiver, content, nil)
}

// Mail kinds recorded in the delivery archive (运营 › 邮件 › 发送记录).
const (
	MailKindWeeklyDigest  = "weekly_digest"
	MailKindMonthlyDigest = "monthly_digest"
	MailKindPayment       = "payment"
	MailKindBalance       = "balance"
	MailKindKeyUsage      = "key_usage"
	MailKindNotice        = "notice"
	MailKindAuth          = "auth"
	MailKindTeam          = "team"
	MailKindOps           = "ops"
	MailKindCopy          = "copy"
	MailKindOther         = "other"
)

// MailMeta describes a mail for the delivery archive and the transport.
type MailMeta struct {
	Kind   string
	UserID int
	// DeliveryID is set when the caller already claimed an archive row
	// (usage digests); the recorder then updates that row instead of adding
	// one.
	DeliveryID uint64
	Template   string
	Lang       string
	Period     string
	// Sensitive mail carries a credential (sign-in code, reset or invite
	// link). Only its subject and status are archived, never its body.
	Sensitive bool
	Headers   map[string]string
	// AlertText is the plain-text body chat sinks receive for an ops mail.
	// Empty sends the subject alone.
	AlertText string
	// NotAnAlert keeps an ops mail away from chat sinks: the test mail checks
	// mail delivery, not the alert channels.
	NotAnAlert bool
}

// MailRecord is what the archive receives after every send attempt.
type MailRecord struct {
	Meta       MailMeta
	Subject    string
	Receiver   string
	HTML       string
	ProviderID string
	Err        error
}

// MailRecorder archives every mail. The model package installs it at
// start-up; common cannot import model.
var MailRecorder func(MailRecord)

// SendMail sends one mail with its archive metadata and optional headers.
func SendMail(meta MailMeta, subject string, receiver string, content string) error {
	providerID, err := sendEmail(subject, receiver, content, nil, meta.Headers)
	if meta.Kind == "" {
		meta.Kind = MailKindOther
	}
	if meta.Kind == MailKindOps && !meta.NotAnAlert {
		PushOpsAlert(OpsAlert{Subject: subject, Text: meta.AlertText})
	}
	if recorder := MailRecorder; recorder != nil {
		recorder(MailRecord{Meta: meta, Subject: subject, Receiver: receiver, HTML: content, ProviderID: providerID, Err: err})
	}
	return err
}

// EmailTransportConfigured reports whether identity mail has a usable
// delivery path. It intentionally checks the same required values as
// SendEmailWithAttachment instead of treating a partially filled SMTP form as
// available.
func EmailTransportConfigured() bool {
	if strings.TrimSpace(AppConfig.Resend.APIKey) != "" {
		return true
	}
	return strings.TrimSpace(SMTPServer) != "" &&
		strings.TrimSpace(SMTPAccount) != "" &&
		strings.TrimSpace(SMTPToken) != ""
}

func SendEmailWithAttachment(subject string, receiver string, content string, attachments []EmailAttachment) error {
	providerID, err := sendEmail(subject, receiver, content, attachments, nil)
	if recorder := MailRecorder; recorder != nil {
		recorder(MailRecord{Meta: MailMeta{Kind: MailKindOther}, Subject: subject, Receiver: receiver, HTML: content, ProviderID: providerID, Err: err})
	}
	return err
}

func sendEmail(subject string, receiver string, content string, attachments []EmailAttachment, headers map[string]string) (string, error) {
	// Prefer the Resend API
	if AppConfig.Resend.APIKey != "" {
		return sendEmailViaResend(subject, receiver, content, attachments, headers)
	}
	// Fall back to SMTP (attachments not supported for SMTP fallback)
	return "", sendEmailViaSMTP(subject, receiver, content, headers)
}

// ResendEmailStatus reads the last delivery event Resend recorded for a mail
// ("delivered", "bounced", "complained", "delivery_delayed" …).
func ResendEmailStatus(providerID string) (string, error) {
	if AppConfig.Resend.APIKey == "" || providerID == "" {
		return "", fmt.Errorf("resend is not configured")
	}
	email, err := resend.NewClient(AppConfig.Resend.APIKey).Emails.Get(providerID)
	if err != nil {
		return "", err
	}
	return email.LastEvent, nil
}

func sendEmailViaResend(subject string, receiver string, content string, attachments []EmailAttachment, headers map[string]string) (string, error) {
	client := resend.NewClient(AppConfig.Resend.APIKey)

	// Supports multiple recipients (semicolon-separated)
	recipients := strings.Split(receiver, ";")
	for i, r := range recipients {
		recipients[i] = strings.TrimSpace(r)
	}

	from := AppConfig.Resend.From
	if from == "" {
		from = fmt.Sprintf("%s <noreply@%s>", SystemName, SiteHost())
	}

	params := &resend.SendEmailRequest{
		From:    from,
		To:      recipients,
		Subject: subject,
		Html:    content,
	}

	// Add attachments if any
	if len(attachments) > 0 {
		var resendAttachments []*resend.Attachment
		for _, att := range attachments {
			resendAttachments = append(resendAttachments, &resend.Attachment{
				Filename: att.Filename,
				Content:  att.Content,
			})
		}
		params.Attachments = resendAttachments
	}
	if len(headers) > 0 {
		params.Headers = headers
	}

	sent, err := client.Emails.Send(params)
	if err != nil {
		SysError(fmt.Sprintf("Resend failed to send email to %s: %v", receiver, err))
		return "", fmt.Errorf("Resend API error: %w", err)
	}
	if sent == nil {
		return "", nil
	}
	return sent.Id, nil
}

func sendEmailViaSMTP(subject string, receiver string, content string, headers map[string]string) error {
	// Snapshot configuration once. In particular, the compatibility fallback
	// must remain local: auth notifications are sent from goroutines, so lazily
	// assigning SMTPFrom here races with other deliveries and test cleanup.
	smtpFrom := strings.TrimSpace(SMTPFrom)
	smtpAccount := strings.TrimSpace(SMTPAccount)
	smtpToken := SMTPToken
	smtpServer := strings.TrimSpace(SMTPServer)
	smtpPort := SMTPPort
	smtpSSLEnabled := SMTPSSLEnabled
	if smtpFrom == "" {
		smtpFrom = smtpAccount
	}
	displayName := SystemName
	if AppConfig.SMTP.FromName != "" {
		displayName = AppConfig.SMTP.FromName
	}
	id, err2 := generateMessageID(smtpFrom)
	if err2 != nil {
		return err2
	}
	if smtpServer == "" && smtpAccount == "" {
		return fmt.Errorf("email service not configured (Resend or SMTP)")
	}
	encodedSubject := fmt.Sprintf("=?UTF-8?B?%s?=", base64.StdEncoding.EncodeToString([]byte(subject)))
	extraHeaders := ""
	for _, name := range slices.Sorted(maps.Keys(headers)) {
		value := strings.NewReplacer("\r", "", "\n", "").Replace(headers[name])
		extraHeaders += name + ": " + value + "\r\n"
	}
	mail := []byte(fmt.Sprintf("To: %s\r\n"+
		"From: %s <%s>\r\n"+
		"Subject: %s\r\n"+
		"Date: %s\r\n"+
		"Message-ID: %s\r\n"+
		"%s"+
		"Content-Type: text/html; charset=UTF-8\r\n\r\n%s\r\n",
		receiver, displayName, smtpFrom, encodedSubject, time.Now().Format(time.RFC1123Z), id, extraHeaders, content))
	auth := smtp.PlainAuth("", smtpAccount, smtpToken, smtpServer)
	addr := fmt.Sprintf("%s:%d", smtpServer, smtpPort)
	to := strings.Split(receiver, ";")
	var err error

	tlsConfig := &tls.Config{
		ServerName: smtpServer,
	}

	if smtpPort == 465 || smtpSSLEnabled {
		// Port 465: implicit TLS (direct TLS connection)
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("TLS dial failed: %w", err)
		}
		client, err := smtp.NewClient(conn, smtpServer)
		if err != nil {
			return fmt.Errorf("SMTP client creation failed: %w", err)
		}
		defer client.Close()
		if err = client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP auth failed: %w", err)
		}
		if err = client.Mail(smtpFrom); err != nil {
			return fmt.Errorf("SMTP MAIL command failed: %w", err)
		}
		receiverEmails := strings.Split(receiver, ";")
		for _, rcpt := range receiverEmails {
			if err = client.Rcpt(rcpt); err != nil {
				return fmt.Errorf("SMTP RCPT command failed: %w", err)
			}
		}
		w, err := client.Data()
		if err != nil {
			return fmt.Errorf("SMTP DATA command failed: %w", err)
		}
		_, err = w.Write(mail)
		if err != nil {
			return fmt.Errorf("SMTP write failed: %w", err)
		}
		err = w.Close()
		if err != nil {
			return fmt.Errorf("SMTP close failed: %w", err)
		}
	} else if smtpPort == 587 {
		// Port 587: STARTTLS (plaintext first, then upgrade to TLS)
		client, err := smtp.Dial(addr)
		if err != nil {
			return fmt.Errorf("SMTP dial failed: %w", err)
		}
		defer client.Close()
		// EHLO
		if err = client.Hello("localhost"); err != nil {
			return fmt.Errorf("SMTP HELLO failed: %w", err)
		}
		// STARTTLS
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err = client.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("SMTP STARTTLS failed: %w", err)
			}
		}
		// Auth
		if err = client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP auth failed: %w", err)
		}
		if err = client.Mail(smtpFrom); err != nil {
			return fmt.Errorf("SMTP MAIL command failed: %w", err)
		}
		receiverEmails := strings.Split(receiver, ";")
		for _, rcpt := range receiverEmails {
			if err = client.Rcpt(rcpt); err != nil {
				return fmt.Errorf("SMTP RCPT command failed: %w", err)
			}
		}
		w, err := client.Data()
		if err != nil {
			return fmt.Errorf("SMTP DATA command failed: %w", err)
		}
		_, err = w.Write(mail)
		if err != nil {
			return fmt.Errorf("SMTP write failed: %w", err)
		}
		err = w.Close()
		if err != nil {
			return fmt.Errorf("SMTP close failed: %w", err)
		}
	} else if isOutlookServer(smtpAccount) || slices.Contains(EmailLoginAuthServerList, smtpServer) {
		auth = LoginAuth(smtpAccount, smtpToken)
		err = smtp.SendMail(addr, auth, smtpFrom, to, mail)
	} else {
		err = smtp.SendMail(addr, auth, smtpFrom, to, mail)
	}
	if err != nil {
		SysError(fmt.Sprintf("failed to send email to %s: %v", receiver, err))
	}
	return err
}
