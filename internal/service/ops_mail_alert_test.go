package service

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
)

// A deployment can route alerts to a chat and have no admin address at all;
// SendOpsMail must still hand the alert to the chat sinks.
func TestSendOpsMailWithoutRecipientStillAlerts(t *testing.T) {
	previousDB := model.CoreDB()
	model.SetCoreDB(nil)
	t.Cleanup(func() { model.SetCoreDB(previousDB) })
	setting := email_setting.GetEmailSetting()
	previousAdmin := setting.AdminEmail
	setting.AdminEmail = ""
	t.Cleanup(func() { setting.AdminEmail = previousAdmin })

	var got []common.OpsAlert
	common.RegisterOpsAlertSink(func(a common.OpsAlert) {
		if a.Subject == "[Lazu ops] 无收件人测试" {
			got = append(got, a)
		}
	})

	err := SendOpsMail("无收件人测试", common.Mail{Lead: "磁盘使用率 91%", Blocks: []common.MailBlock{{Facts: []common.MailFact{{Label: "主机", Value: "db"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "磁盘使用率 91%\n主机: db" {
		t.Fatalf("alerts = %+v", got)
	}
}
