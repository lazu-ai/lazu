package common

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

func TestWarnSamplingRefusedAddsToThePlanAndTheHeader(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	plan := &textgen.RelayPlan{Warnings: []textgen.Issue{{Message: "degraded:json_object_to_instruction"}}}
	info := &RelayInfo{ProtocolPlan: plan}
	SetProtocolWarningHeader(c, plan)

	WarnSamplingRefused(c, info, []string{"temperature", "top_p"})
	if got := recorder.Header().Get(ProtocolWarningHeader); got != "degraded:json_object_to_instruction, sampling_refused:temperature, sampling_refused:top_p" {
		t.Fatalf("header = %q", got)
	}
	if len(plan.Warnings) != 3 || plan.Warnings[1].Feature != textgen.FeatureTemperature || plan.Warnings[1].Path != "temperature" || plan.Warnings[2].Feature != textgen.FeatureTopP {
		t.Fatalf("plan warnings = %+v", plan.Warnings)
	}

	// said once per field, however many times the channel reports it
	WarnSamplingRefused(c, info, []string{"temperature"})
	if len(plan.Warnings) != 3 {
		t.Fatalf("duplicate warning added: %+v", plan.Warnings)
	}
}

func TestWarnSamplingRefusedNeedsAPlan(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	WarnSamplingRefused(c, &RelayInfo{}, []string{"temperature"})
	WarnSamplingRefused(c, nil, []string{"temperature"})
	WarnSamplingRefused(nil, &RelayInfo{ProtocolPlan: &textgen.RelayPlan{}}, []string{"temperature"})
	if got := recorder.Header().Get(ProtocolWarningHeader); got != "" {
		t.Fatalf("header without a plan = %q", got)
	}
	plan := &textgen.RelayPlan{}
	WarnSamplingRefused(c, &RelayInfo{ProtocolPlan: plan}, nil)
	if len(plan.Warnings) != 0 {
		t.Fatalf("no fields, no warnings: %+v", plan.Warnings)
	}
}

func TestSetProtocolWarningHeaderIsScopedToThePlan(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	SetProtocolWarningHeader(c, &textgen.RelayPlan{Warnings: []textgen.Issue{{Message: "stateless_bridge"}}})
	if got := recorder.Header().Get(ProtocolWarningHeader); got != "stateless_bridge" {
		t.Fatalf("header = %q", got)
	}
	SetProtocolWarningHeader(c, &textgen.RelayPlan{Native: true})
	if got := recorder.Header().Get(ProtocolWarningHeader); got != "" {
		t.Fatalf("a plan without warnings must clear the header, got %q", got)
	}
}
