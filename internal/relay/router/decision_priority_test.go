package router

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
)

func TestDecisionRoutingRejectsOtherProviderDialects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupRouterTestDB(t)
	defer cleanup()
	for index, dialect := range []string{"typesafe", "openai", "cloudflare_workers_ai"} {
		setting := `{"decision_dialect":"` + dialect + `","cf_account_id":"fixture"}`
		channel := model.Channel{Id: 9800 + index, Type: constant.ChannelTypeOpenAI, Key: "synthetic", Status: common.ChannelStatusEnabled,
			Models: common.OpenAIDecisionModel + ",gpt-6-luna,jev-1.13,clef", Group: model.ChannelLaneDirect, Setting: &setting}
		if err := model.CoreDB().Create(&channel).Error; err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{common.OpenAIDecisionModel, "gpt-6-luna", "jev-1.13", "clef"} {
			if err := model.CoreDB().Create(&model.Ability{ChannelId: channel.Id, Group: model.ChannelLaneDirect, Model: name, Enabled: true}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, test := range []struct {
		name string
		id   int
	}{{common.OpenAIDecisionModel, 9801}, {"jev-1.13", 9800}, {"clef", 9802}, {"gpt-6-luna", 0}} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/decisions", nil)
			selected, _, err := SelectProtocolCompatibleChannel(c, model.ChannelLaneDirect, test.name, nil)
			if err != nil {
				t.Fatal(err)
			}
			if test.id == 0 {
				if selected != nil {
					t.Fatal("ordinary text model routed through decisions")
				}
			} else if selected == nil || selected.Id != test.id {
				t.Fatalf("selected %v, want channel %d", selected, test.id)
			}
		})
	}
}
