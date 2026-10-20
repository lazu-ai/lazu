// Package wirepatch is the wire dialect patch layer: the one place where a
// vendor's deviation from the protocol it claims to speak is corrected on the
// compiled outbound body.
//
// Position in the pipeline (docs/wire-dialect-patch.md): codec compiles the
// outbound body → RemoveDisabledFields → operator ParamOverride → wirepatch →
// transport. A patch is applied exactly once per attempt, after ParamOverride,
// so it also covers bytes the operator introduced.
//
// A quirk is a registered Patch: one ID, the dialects it applies to, a one-line
// reason, a source link, one function and one regression test (AGENTS.md,
// Protocol Layer rule 6). Codecs and channel adaptors contain no vendor
// conditionals; if a vendor needs one, it is an entry here.
package wirepatch

import (
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/packages/domain/providercap"
)

// Context is what a patch may consult besides the body. It is deliberately
// small: a patch fixes wire shape for a dialect and must not depend on the
// model, the tenant or the attempt.
type Context struct {
	Dialect string
}

// Patch is one named vendor quirk.
type Patch struct {
	// ID is stable and unique (snake_case); it is the name the PR, the test
	// and the request detail refer to.
	ID string
	// Dialects lists the upstream dialects the patch runs for
	// (providercap.Dialect* constants).
	Dialects []string
	// Reason is one line: what the vendor rejects or mangles without it.
	Reason string
	// Source is a link to the evidence: vendor doc, issue, or reference
	// implementation.
	Source string
	// Apply returns body unchanged (the same slice) when nothing matches. It
	// must not re-encode the whole body: splice in place so the bytes outside
	// the quirk, and with them the prompt cache prefix, stay identical.
	Apply func(body []byte, ctx Context) []byte
}

// registry is ordered: patches run in registration order for a dialect.
var registry = []Patch{
	deepseekToolPatterns,
}

// Patches returns the registered patches in application order.
func Patches() []Patch {
	return append([]Patch(nil), registry...)
}

func (p Patch) appliesTo(dialect string) bool {
	for _, d := range p.Dialects {
		if d == dialect {
			return true
		}
	}
	return false
}

// Apply runs the registered patches for dialect, in order, on body. An empty
// or unknown dialect, or a dialect with no patches, returns body as it is.
func Apply(dialect string, body []byte) []byte {
	if dialect == "" || len(body) == 0 {
		return body
	}
	ctx := Context{Dialect: dialect}
	for _, p := range registry {
		if p.appliesTo(dialect) {
			body = p.Apply(body, ctx)
		}
	}
	return body
}

// ResolveDialect names the upstream dialect of the attempt: the plan's when
// one exists, else the channel's configured dialect normalized for its API
// type. It returns "" when the relay info carries no channel yet, which makes
// Apply a no-op.
func ResolveDialect(info *relaycommon.RelayInfo) string {
	if info == nil {
		return ""
	}
	if info.ProtocolPlan != nil && info.ProtocolPlan.UpstreamProtocol.Dialect != "" {
		return info.ProtocolPlan.UpstreamProtocol.Dialect
	}
	if info.ChannelMeta == nil {
		return ""
	}
	return providercap.NormalizeDialect(info.ApiType, info.ChannelOtherSettings.ProtocolDialect)
}
