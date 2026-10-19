import { Check } from 'lucide-react';
import { cn } from '../../lib/cn';

const AUTH_STEPS = ['Identity', 'Workspace', 'Complete'] as const;

type AuthProgressProps = {
  ariaLabel?: string;
  current?: number;
  steps?: readonly string[];
};

export function AuthProgress({
  ariaLabel = 'Sign-in progress',
  current = 0,
  steps = AUTH_STEPS,
}: AuthProgressProps) {
  return (
    <ol className='lazu-auth-progress' aria-label={ariaLabel}>
      {steps.map((step, index) => {
        const completed = index < current;
        const active = index === current;
        return (
          <li
            className={cn(completed && 'is-complete', active && 'is-current')}
            aria-current={active ? 'step' : undefined}
            key={step}
          >
            <span>
              {completed ? <Check aria-hidden='true' size={12} /> : index + 1}
            </span>
            <strong>{step}</strong>
          </li>
        );
      })}
    </ol>
  );
}
