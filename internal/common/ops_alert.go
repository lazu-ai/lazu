package common

import "strings"

// OpsAlert is one operational alert as a chat sink sees it. Every mail of
// kind ops becomes one when it is sent, and so does an alert that found no
// mail recipient, so a deployment with a chat sink and no admin address
// still hears about trouble.
type OpsAlert struct {
	Subject string
	Text    string
}

var opsAlertSinks []func(OpsAlert)

// RegisterOpsAlertSink adds a destination for ops alerts; register from
// init(). A sink swallows its own errors and returns within seconds: mail
// delivery never waits on a chat sink or fails because of one.
func RegisterOpsAlertSink(sink func(OpsAlert)) {
	opsAlertSinks = append(opsAlertSinks, sink)
}

// PushOpsAlert hands an alert to every registered sink.
func PushOpsAlert(alert OpsAlert) {
	for _, sink := range opsAlertSinks {
		sink(alert)
	}
}

// MailAlertText is the plain-text form of an ops-tier mail for chat sinks:
// the lead, then each block's text, facts, steps and table rows, one per
// line. Raw HTML blocks and actions are left out.
func MailAlertText(mail Mail) string {
	var lines []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			lines = append(lines, s)
		}
	}
	add(mail.Lead)
	for _, block := range mail.Blocks {
		add(block.Text)
		for _, fact := range block.Facts {
			add(fact.Label + ": " + fact.Value)
		}
		for _, step := range block.Steps {
			add(strings.TrimSpace(step.Text + " " + step.Mono))
		}
		add(block.Code)
		add(block.Snippet)
		if block.Table != nil {
			if len(block.Table.Head) > 0 {
				add(strings.Join(block.Table.Head, " | "))
			}
			for _, row := range block.Table.Rows {
				add(strings.Join(row, " | "))
			}
		}
		add(block.TextAfter)
	}
	return strings.Join(lines, "\n")
}
