import { cn } from '../../lib/cn';
import '../../styles/settings-surface.css';

/* The settings card and row every settings page shares: a card of paper with
 * a title and one-line description, and rows that read
 * `label + explanation | value + action`. Personal settings and project
 * settings both compose these. Spec: brand/foundations/yunmu-console.md
 * § Locked settings surfaces. */
export function SettingsCard({
  badge,
  children,
  className,
  description,
  title,
}: {
  badge?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
  description?: React.ReactNode;
  title: React.ReactNode;
}) {
  return (
    <section className={cn('set-card', className)}>
      <div className='set-card__hd'>
        <p className='set-card__t'>
          {title}
          {badge}
        </p>
        {description ? <p className='set-card__d'>{description}</p> : null}
      </div>
      {children}
    </section>
  );
}

export function SettingsRow({
  actions,
  children,
  className,
  hint,
  id,
  label,
  open,
  value,
}: {
  actions?: React.ReactNode;
  /** Rendered after the row's cells, e.g. the row's editor dialog. */
  children?: React.ReactNode;
  className?: string;
  hint?: React.ReactNode;
  id?: string;
  label: React.ReactNode;
  open?: boolean;
  value?: React.ReactNode;
}) {
  return (
    <div className={cn('set-row', open && 'is-open', className)} id={id}>
      <span className='set-row__lb'>{label}</span>
      <span className='set-row__vv'>
        {value ? <span className='set-row__vl'>{value}</span> : null}
        {actions}
      </span>
      {hint ? <span className='set-row__hint'>{hint}</span> : null}
      {children}
    </div>
  );
}
