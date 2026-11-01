import { XCircle } from 'lucide-react';
import React from 'react';
import { cn } from '../../lib/cn';

type FieldProps = React.HTMLAttributes<HTMLDivElement>;
type LabelProps = React.LabelHTMLAttributes<HTMLLabelElement>;
type FieldMessageProps = React.HTMLAttributes<HTMLDivElement> & {
  error?: boolean;
};

type MessageKind = 'hint' | 'error';

type FieldContextValue = {
  /** Ids of the messages currently rendered in this field, hint first. */
  describedBy?: string;
  invalid: boolean;
  messageId: (kind: MessageKind) => string;
  register: (kind: MessageKind) => () => void;
};

const FieldContext = React.createContext<FieldContextValue | null>(null);

/**
 * Wires a control to the messages rendered in the same `Field`: the control
 * gets `aria-describedby` pointing at them and `aria-invalid` while an error
 * message is showing. Props the caller passes win.
 */
export function useFieldControlProps({
  'aria-describedby': describedBy,
  'aria-invalid': invalid,
}: {
  'aria-describedby'?: string;
  'aria-invalid'?: React.AriaAttributes['aria-invalid'];
}) {
  const field = React.useContext(FieldContext);
  if (!field) {
    return { 'aria-describedby': describedBy, 'aria-invalid': invalid };
  }
  const ids = [describedBy, field.describedBy].filter(Boolean).join(' ');
  return {
    'aria-describedby': ids || undefined,
    'aria-invalid': invalid ?? (field.invalid ? true : undefined),
  };
}

export function Field({ children, className, ...props }: FieldProps) {
  const baseId = React.useId();
  const [present, setPresent] = React.useState<Record<MessageKind, number>>({
    hint: 0,
    error: 0,
  });
  const register = React.useCallback((kind: MessageKind) => {
    setPresent((current) => ({ ...current, [kind]: current[kind] + 1 }));
    return () =>
      setPresent((current) => ({ ...current, [kind]: current[kind] - 1 }));
  }, []);
  const value = React.useMemo<FieldContextValue>(() => {
    const messageId = (kind: MessageKind) => `${baseId}-${kind}`;
    const ids = (['hint', 'error'] as const)
      .filter((kind) => present[kind] > 0)
      .map(messageId);
    return {
      describedBy: ids.length ? ids.join(' ') : undefined,
      invalid: present.error > 0,
      messageId,
      register,
    };
  }, [baseId, present, register]);

  return (
    <FieldContext.Provider value={value}>
      <div className={cn('lazu-field', className)} {...props}>
        {children}
      </div>
    </FieldContext.Provider>
  );
}

export function Label({ children, className, ...props }: LabelProps) {
  return (
    <label className={cn('lazu-label', className)} {...props}>
      {children}
    </label>
  );
}

export function FieldMessage({
  children,
  className,
  error = false,
  id,
  ...props
}: FieldMessageProps) {
  const field = React.useContext(FieldContext);
  const kind: MessageKind = error ? 'error' : 'hint';
  const shown = Boolean(children);
  const register = field?.register;
  React.useLayoutEffect(() => {
    if (!shown || !register || id) {
      return undefined;
    }
    return register(kind);
  }, [id, kind, register, shown]);

  if (!children) {
    return null;
  }

  return (
    <div
      className={cn(
        'lazu-field-message',
        error && 'lazu-field-message--error',
        className,
      )}
      id={id ?? field?.messageId(kind)}
      role={error ? 'alert' : undefined}
      {...props}
    >
      {error ? (
        <XCircle aria-hidden='true' className='lazu-field-message__icon' />
      ) : null}
      {children}
    </div>
  );
}
