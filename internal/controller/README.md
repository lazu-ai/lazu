# Controller Boundaries

New controller code should keep request handling split across four layers:

- Transport parsing: read path/query/body/header values from `gin.Context`, normalize input, and return validation errors.
- Policy: authorize the action, apply feature gates, rate limits, token/vendor constraints, and request-scoped safety checks.
- Service: execute durable business work through `internal/service`, `internal/model`, or relay-specific packages. Non-critical logging, cache refresh, notifications, and uploads should run through `common.SafeGo` or `common.SafeCtxGo`.
- Response shaping: convert service results to API JSON, headers, status codes, or stream frames without adding new business decisions.

Controllers should not embed long-running background loops or raw goroutines. Use `common.SafeGo`, `common.SafeCtxGo`, or `common.RelayCtxGo` so panics are recovered and logged consistently.
