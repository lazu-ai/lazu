package model

import (
	"fmt"
	"strconv"
	"strings"
)

func redisDurabilityAlerts(memory, persistence string) map[string]string {
	parse := func(info string) map[string]string {
		values := map[string]string{}
		for _, line := range strings.Split(info, "\n") {
			key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
			if ok {
				values[key] = value
			}
		}
		return values
	}
	m, p := parse(memory), parse(persistence)
	alerts := map[string]string{}
	used, usedErr := strconv.ParseInt(m["used_memory"], 10, 64)
	limit, limitErr := strconv.ParseInt(m["maxmemory"], 10, 64)
	if usedErr == nil && limitErr == nil && limit > 0 && float64(used)/float64(limit) > .7 {
		alerts["redis-memory"] = fmt.Sprintf("Redis used_memory=%d maxmemory=%d usage=%.1f%% exceeds 70%%; AOF rewrite also needs memory headroom", used, limit, float64(used)*100/float64(limit))
	}
	if p["aof_enabled"] != "1" || p["aof_last_write_status"] != "ok" || p["aof_last_bgrewrite_status"] != "ok" {
		alerts["redis-aof"] = fmt.Sprintf("Redis AOF durability needs attention: enabled=%s last_write_status=%s last_bgrewrite_status=%s", p["aof_enabled"], p["aof_last_write_status"], p["aof_last_bgrewrite_status"])
	}
	return alerts
}
