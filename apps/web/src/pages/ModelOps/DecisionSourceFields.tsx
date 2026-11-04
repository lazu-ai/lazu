import { Controller, type UseFormReturn } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { FieldLabel } from './FieldLabel';
import { Field, FieldMessage } from '../../components/ui/field';
import { Input } from '../../components/ui/input';
import { Select, SelectItem } from '../../components/ui/select';
import type { ChannelFormValues } from './SourceForm';

const ns = 'page.console.channel.form';

type FieldsProps = {
  form: UseFormReturn<ChannelFormValues>;
  disabled: boolean;
};

/** 限流: this source's own limits, then the group it shares an upstream
 *  account with and that group's limits. Empty or 0 leaves a limit unset. */
export function CapacityFields({ form, disabled }: FieldsProps) {
  const { t } = useTranslation();
  const group = form.watch('shared_limit_group') || '';
  const unlimited = t(`${ns}.placeholder.unlimited`, { defaultValue: '不限' });
  const numberField = (
    name:
      | 'max_rps'
      | 'max_in_flight'
      | 'shared_max_rps'
      | 'shared_max_in_flight',
    label: string,
    shared = false,
  ) => {
    /* The shared limits mean nothing without a group to share them, so they
       wait for one, unless a saved value is already there to edit. */
    const idle = shared && !group.trim() && !Number(form.getValues(name));
    return (
      <Field key={name}>
        <FieldLabel
          code={name}
          htmlFor={`capacity-${name}`}
          muted={idle}
          text={t(`${ns}.field.${name}`, { defaultValue: label })}
        />
        <Input
          disabled={disabled || idle}
          id={`capacity-${name}`}
          min={0}
          placeholder={unlimited}
          type='number'
          {...form.register(name)}
        />
        <FieldMessage error>
          {form.formState.errors[name]?.message}
        </FieldMessage>
      </Field>
    );
  };
  return (
    <div className='mo-form__grid'>
      {numberField('max_rps', '每秒请求数')}
      {numberField('max_in_flight', '上游并发请求数')}
      <Field className='mo-form__wide'>
        <FieldLabel
          code='shared_limit_group'
          hint={t(`${ns}.decision.groupHint`, {
            defaultValue: '同一上游账户的来源填同名，共用下面两项',
          })}
          htmlFor='shared-limit-group'
          text={t(`${ns}.field.shared_limit_group`, {
            defaultValue: '共享容量组',
          })}
          tip={t(`${ns}.decision.capacityHelp`, {
            defaultValue:
              'Channels using the same upstream account share a group. 0 leaves a limit unset; the smallest positive shared limit applies.',
          })}
        />
        <Input
          disabled={disabled}
          id='shared-limit-group'
          placeholder={t(`${ns}.placeholder.shared_limit_group`, {
            defaultValue: '例如 main-account',
          })}
          {...form.register('shared_limit_group')}
        />
      </Field>
      {numberField('shared_max_rps', '共享每秒请求数', true)}
      {numberField('shared_max_in_flight', '共享并发请求数', true)}
    </div>
  );
}

/** The decision API a source can consult before it is sent a request. */
export function DecisionFields({ form, disabled }: FieldsProps) {
  const { t } = useTranslation();
  const dialect = form.watch('decision_dialect');
  return (
    <>
      <Field>
        <FieldLabel
          code='decision_dialect'
          htmlFor='decision-dialect'
          text={t(`${ns}.field.decision_dialect`, {
            defaultValue: 'Decision API',
          })}
        />
        <Controller
          name='decision_dialect'
          control={form.control}
          render={({ field }) => (
            <Select
              id='decision-dialect'
              disabled={disabled}
              value={field.value || 'none'}
              onValueChange={(value) =>
                field.onChange(value === 'none' ? '' : value)
              }
            >
              <SelectItem value='none'>
                {t(`${ns}.decision.none`, {
                  defaultValue: 'No decision API',
                })}
              </SelectItem>
              <SelectItem value='typesafe'>TypeSafe</SelectItem>
              <SelectItem value='openai'>OpenAI Decisions</SelectItem>
              <SelectItem value='openrouter'>OpenRouter</SelectItem>
              <SelectItem value='cloudflare_gateway'>
                Cloudflare AI Gateway
              </SelectItem>
              <SelectItem value='cloudflare_workers_ai'>
                Cloudflare Workers AI
              </SelectItem>
            </Select>
          )}
        />
      </Field>
      {dialect.startsWith('cloudflare_') && (
        <Field>
          <FieldLabel
            code='cf_account_id'
            htmlFor='cf-account-id'
            text={t(`${ns}.field.cf_account_id`, {
              defaultValue: 'Cloudflare account ID',
            })}
          />
          <Input
            id='cf-account-id'
            disabled={disabled}
            {...form.register('cf_account_id')}
          />
        </Field>
      )}
      {dialect && (
        <Field>
          <FieldLabel
            code='decision_timeout_ms'
            htmlFor='decision-timeout'
            text={t(`${ns}.field.decision_timeout_ms`, {
              defaultValue: 'Decision timeout (ms)',
            })}
          />
          <Input
            id='decision-timeout'
            type='number'
            min={0}
            max={10000}
            disabled={disabled}
            {...form.register('decision_timeout_ms')}
          />
          <FieldMessage>
            {t(`${ns}.decision.timeoutHelp`, {
              defaultValue:
                '0 uses 5000ms. Total native request budget: 10 seconds.',
            })}
          </FieldMessage>
        </Field>
      )}
    </>
  );
}
