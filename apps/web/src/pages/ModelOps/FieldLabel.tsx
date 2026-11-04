import { Info } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Tooltip } from '../../components/ui/tooltip';
import type { ReactNode } from 'react';

type FieldLabelProps = {
  /** The stored field name, shown on hover or focus of ⓘ, not in the label:
   *  the default view reads in the operator's language (canvas 4A). */
  code?: string;
  /** A short clause after the label, in the label's line. */
  hint?: ReactNode;
  htmlFor?: string;
  /** A field waiting on another one: the label greys with its input. */
  muted?: boolean;
  text: ReactNode;
  /** The longer explanation that goes in the tooltip with the field name. */
  tip?: ReactNode;
};

/** A form label with an ⓘ that names the stored field and explains it. */
export function FieldLabel({
  code,
  hint,
  htmlFor,
  muted,
  text,
  tip,
}: FieldLabelProps) {
  const { t } = useTranslation();
  return (
    <span className='mo-field-label'>
      <label
        className={`lazu-label${muted ? ' is-muted' : ''}`}
        htmlFor={htmlFor}
      >
        {text}
        {hint ? <span className='mo-field-label__hint'> · {hint}</span> : null}
      </label>
      {code || tip ? (
        <Tooltip
          className='mo-field-tip'
          content={
            <>
              {code ? <span className='mo-field-tip__code'>{code}</span> : null}
              {code && tip ? ' · ' : null}
              {tip}
            </>
          }
        >
          <button
            aria-label={t('page.console.channel.form.fieldInfo', {
              defaultValue: '字段说明：{{name}}',
              name: code || '',
            })}
            className='mo-field-label__info'
            data-owns-chrome=''
            type='button'
          >
            <Info aria-hidden='true' size={14} />
          </button>
        </Tooltip>
      ) : null}
    </span>
  );
}
