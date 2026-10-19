import type { AgentSessionRequest } from '../../../hooks/usage-logs/useAgentSessions';

type TimedRequest = Pick<
  AgentSessionRequest,
  'created_at' | 'duration_seconds'
>;

// Log timestamps mark completion. Durations are whole seconds, so a zero
// duration has insufficient resolution and must not invent a running interval.
export function requestInterval(request: TimedRequest) {
  return {
    from: request.created_at - Math.max(0, request.duration_seconds),
    to: request.created_at,
  };
}

export function requestConcurrency(requests: TimedRequest[]) {
  const changes = new Map<number, number>();
  for (const request of requests) {
    const { from, to } = requestInterval(request);
    if (to <= from) continue;
    changes.set(from, (changes.get(from) ?? 0) + 1);
    changes.set(to, (changes.get(to) ?? 0) - 1);
  }
  const edges = [...changes].sort(([a], [b]) => a - b);
  const steps: { count: number; from: number; to: number }[] = [];
  let count = 0;
  edges.forEach(([from, delta], index) => {
    count += delta;
    const next = edges[index + 1];
    if (next) steps.push({ count, from, to: next[0] });
  });
  return steps;
}
