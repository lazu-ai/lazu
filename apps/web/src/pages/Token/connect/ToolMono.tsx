import { cn } from '../../../lib/cn';
import type { ConnectTool } from './tools';

/** The two- or three-letter square that stands in for a tool's logo. */
export function ToolMono({
  active = false,
  size = 'md',
  tool,
}: {
  active?: boolean;
  size?: 'sm' | 'md' | 'lg' | 'xl';
  tool?: ConnectTool;
}) {
  return (
    <span
      aria-hidden='true'
      className={cn('tok-mono', `tok-mono--${size}`, active && 'is-active')}
    >
      {tool?.mono ?? '—'}
    </span>
  );
}
