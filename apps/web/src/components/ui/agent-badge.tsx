import { extensionI18n } from '../../extensions/i18n';
import { cn } from '../../lib/cn';

/**
 * The client that sent a session or request, as a small ink monogram. Only
 * text — no vendor logos — so a third party's mark never stands in for our
 * judgement of who sent the traffic.
 */
const AGENTS: Record<string, { mark: string; name: string }> = {
  'claude-code': { mark: 'CC', name: 'Claude Code' },
  codex: { mark: 'CX', name: 'Codex' },
  opencode: { mark: 'OC', name: 'OpenCode' },
  'kilo-code': { mark: 'KC', name: 'Kilo Code' },
  'roo-code': { mark: 'RC', name: 'Roo Code' },
  cline: { mark: 'CL', name: 'Cline' },
  crush: { mark: 'CR', name: 'Crush' },
  goose: { mark: 'GS', name: 'Goose' },
  pi: { mark: 'pi', name: 'pi' },
  openclaw: { mark: 'OW', name: 'OpenClaw' },
  'gemini-cli': { mark: 'GM', name: 'Gemini CLI' },
  'qwen-code': { mark: 'QW', name: 'Qwen Code' },
  'kimi-cli': { mark: 'KM', name: 'Kimi CLI' },
  cursor: { mark: 'Cu', name: 'Cursor' },
  zed: { mark: 'ZD', name: 'Zed' },
  aider: { mark: 'AI', name: 'Aider' },
  continue: { mark: 'CT', name: 'Continue' },
  ...Object.assign(
    {},
    ...extensionI18n.map((extension) => extension.clientBadges ?? {}),
  ),
};

export function agentName(client: string | undefined | null): string {
  const key = (client || '').trim().toLowerCase();
  return AGENTS[key]?.name || client || '—';
}

export function isKnownAgent(client: string | undefined | null): boolean {
  return Boolean(AGENTS[(client || '').trim().toLowerCase()]);
}

type AgentBadgeProps = {
  client?: string | null;
  /** Dashed outline: traffic grouped by Key because it carried no session id. */
  ungrouped?: boolean;
  size?: 'sm' | 'md';
  className?: string;
};

export function AgentBadge({
  client,
  ungrouped,
  size = 'md',
  className,
}: AgentBadgeProps) {
  const key = (client || '').trim().toLowerCase();
  const agent = AGENTS[key];
  const mark = agent?.mark || (key ? key.slice(0, 2) : '?');
  return (
    <span
      className={cn('lz-agent-badge', className)}
      data-size={size}
      data-ghost={ungrouped || !agent || undefined}
      title={agent?.name || client || undefined}
      aria-label={agent?.name || client || undefined}
    >
      {mark}
    </span>
  );
}
