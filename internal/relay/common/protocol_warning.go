package common

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// ProtocolWarningHeader tells the client what the attempt could not keep as
// asked: a stateless bridge, an endpoint bridge for a feature, a degradation
// (textgen/degrade.go), a sampling field the model refuses. Every warning of
// the plan, comma separated; the request detail keeps them one by one.
const ProtocolWarningHeader = "X-Lazu-Warning"

// SetProtocolWarningHeader writes the plan's warnings as the response header,
// replacing what an earlier attempt wrote; a plan without warnings clears it.
func SetProtocolWarningHeader(c *gin.Context, plan *textgen.RelayPlan) {
	if c == nil {
		return
	}
	c.Writer.Header().Del(ProtocolWarningHeader)
	if plan == nil || len(plan.Warnings) == 0 {
		return
	}
	messages := make([]string, 0, len(plan.Warnings))
	for _, warning := range plan.Warnings {
		messages = append(messages, warning.Message)
	}
	c.Header(ProtocolWarningHeader, strings.Join(messages, ", "))
}

// SamplingRefusedPrefix starts the warning for a sampling field the channel
// left out because the model's catalog row refuses it:
// "sampling_refused:temperature".
const SamplingRefusedPrefix = "sampling_refused:"

// WarnSamplingRefused records on the attempt's plan that the channel left
// fields out of the upstream request because the model's catalog row refuses
// them (unsupported_params), one warning per field, and rewrites the header
// so the client sees it. The plan is the controller's own (RelayInfo and the
// request detail read the same pointer), so the detail's protocol_warnings
// carries them too. Nothing happens without a plan: a request the planner
// did not see has no warnings to add to.
func WarnSamplingRefused(c *gin.Context, info *RelayInfo, fields []string) {
	if info == nil || info.ProtocolPlan == nil || len(fields) == 0 {
		return
	}
	for _, field := range fields {
		message := SamplingRefusedPrefix + field
		already := false
		for _, warning := range info.ProtocolPlan.Warnings {
			if warning.Message == message {
				already = true
				break
			}
		}
		if already {
			continue
		}
		issue := textgen.Issue{Path: field, Message: message}
		if spec, ok := textgen.ParameterRegistry[field]; ok {
			issue.Feature = spec.RequiredFeature
		}
		info.ProtocolPlan.Warnings = append(info.ProtocolPlan.Warnings, issue)
	}
	SetProtocolWarningHeader(c, info.ProtocolPlan)
}
