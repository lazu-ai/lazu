package ledger

import "context"

// PublishRecoveryTelemetry is used only after stopped-writer recovery has
// committed the original financial operation in core. Both receipt and rich
// facts replay under their unchanged identities; consumers deduplicate them.
func (e *Engine) PublishRecoveryTelemetry(ctx context.Context, event Event) error {
	receipt := ""
	var err error
	if event.Kind != "release" && e.TelemetryEncode != nil && e.TelemetryStream != "" {
		receipt, err = e.TelemetryEncode(event)
		if err != nil {
			return err
		}
	}
	if e.TelemetryStream == "" {
		return e.CheckContinuity(ctx)
	}
	_, err = e.eval(ctx, `
 local function publish(kind,payload)
  if payload=='' then return end
  local before=redis.call('XLEN',KEYS[1]);redis.call('XADD',KEYS[1],'MAXLEN','=',100000,'*','type',kind,'payload',payload)
  local dropped=before+1-redis.call('XLEN',KEYS[1]);if dropped>0 then redis.call('INCRBY',KEYS[1]..':dropped',dropped) end
 end
 publish('financial_settlement',ARGV[1]);publish('relay_result',ARGV[2]);return 'ok'
 `, []string{e.TelemetryStream}, receipt, event.TelemetryEnvelope)
	return err
}
