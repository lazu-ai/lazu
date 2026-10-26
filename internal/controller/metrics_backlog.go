package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/model"
)

type backlogStream struct {
	label, stream, group, dead string
}

func backlogStreams() []backlogStream {
	return []backlogStream{
		{label: "financial", stream: ledger.Stream, group: ledger.Group, dead: ledger.DeadStream},
		{label: "telemetry", stream: common.RelayEventStream, group: common.RelayEventGroup, dead: common.RelayEventStream + ":dead"},
	}
}

// writeStreamBacklogMetrics reports how far the settlement and telemetry
// writers are behind: entries in each stream, entries delivered but not yet
// acknowledged, entries not yet delivered (lag), dead letters, and envelopes
// waiting in this node's ledger spool. Read at scrape time with a short
// timeout, so a slow Redis costs the scrape, never a request.
func writeStreamBacklogMetrics(b *strings.Builder) {
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		type row struct {
			label                       string
			length, pending, lag, dead  int64
			hasLength, hasGroup, hasLag bool
			hasDead                     bool
		}
		var rows []row
		for _, s := range backlogStreams() {
			r := row{label: s.label}
			if n, err := common.RDB.XLen(ctx, s.stream).Result(); err == nil {
				r.length, r.hasLength = n, true
			}
			if pending, lag, ok, hasLag := streamGroupBacklog(ctx, s.stream, s.group); ok {
				r.pending, r.lag, r.hasGroup, r.hasLag = pending, lag, true, hasLag
			}
			if n, err := common.RDB.XLen(ctx, s.dead).Result(); err == nil {
				r.dead, r.hasDead = n, true
			}
			rows = append(rows, r)
		}
		writePrometheusHelpType(b, "lazu_stream_length", "Entries in the writer stream.", "gauge")
		for _, r := range rows {
			if r.hasLength {
				writePrometheusSample(b, "lazu_stream_length", map[string]string{"stream": r.label}, float64(r.length))
			}
		}
		writePrometheusHelpType(b, "lazu_stream_pending", "Entries delivered to a writer but not yet acknowledged.", "gauge")
		for _, r := range rows {
			if r.hasGroup {
				writePrometheusSample(b, "lazu_stream_pending", map[string]string{"stream": r.label}, float64(r.pending))
			}
		}
		writePrometheusHelpType(b, "lazu_stream_lag", "Entries not yet delivered to any writer.", "gauge")
		for _, r := range rows {
			if r.hasLag {
				writePrometheusSample(b, "lazu_stream_lag", map[string]string{"stream": r.label}, float64(r.lag))
			}
		}
		writePrometheusHelpType(b, "lazu_stream_dead_letters", "Entries moved to the dead-letter stream.", "gauge")
		for _, r := range rows {
			if r.hasDead {
				writePrometheusSample(b, "lazu_stream_dead_letters", map[string]string{"stream": r.label}, float64(r.dead))
			}
		}
	}
	if engine := model.GatewayLedger(); engine != nil && engine.SpoolDir != "" {
		files, err := filepath.Glob(filepath.Join(engine.SpoolDir, "*.json"))
		if err == nil || os.IsNotExist(err) {
			writePrometheusHelpType(b, "lazu_ledger_spool_files", "Settlement envelopes waiting in this node's spool.", "gauge")
			writePrometheusSample(b, "lazu_ledger_spool_files", nil, float64(len(files)))
		}
	}
}

// streamGroupBacklog reads pending and lag for one consumer group from the
// raw XINFO GROUPS reply; the client's typed reply has no lag field. lag is
// absent before Redis 7 and when Redis cannot compute it.
func streamGroupBacklog(ctx context.Context, stream, group string) (pending, lag int64, ok, hasLag bool) {
	reply, err := common.RDB.Do(ctx, "XINFO", "GROUPS", stream).Result()
	if err != nil {
		return 0, 0, false, false
	}
	groups, _ := reply.([]interface{})
	for _, raw := range groups {
		fields, _ := raw.([]interface{})
		values := map[string]interface{}{}
		for i := 0; i+1 < len(fields); i += 2 {
			values[fmt.Sprint(fields[i])] = fields[i+1]
		}
		if fmt.Sprint(values["name"]) != group {
			continue
		}
		if p, isInt := values["pending"].(int64); isInt {
			pending = p
		}
		if l, isInt := values["lag"].(int64); isInt {
			lag, hasLag = l, true
		}
		return pending, lag, true, hasLag
	}
	return 0, 0, false, false
}
