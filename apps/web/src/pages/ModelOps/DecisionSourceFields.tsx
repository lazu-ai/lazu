import { Controller, type UseFormReturn } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { Field, FieldMessage } from '../../components/ui/field';
import { Input } from '../../components/ui/input';
import { Select, SelectItem } from '../../components/ui/select';
import type { ChannelFormValues } from './SourceForm';

export function DecisionSourceFields({
  form,
  disabled,
}: {
  form: UseFormReturn<ChannelFormValues>;
  disabled: boolean;
}) {
  const { t } = useTranslation();
  const dialect = form.watch('decision_dialect');
  const numericFields = [
    ['max_rps', 'Requests per second'],
    ['max_in_flight', 'Concurrent upstream requests'],
    ['shared_max_rps', 'Shared requests per second'],
    ['shared_max_in_flight', 'Shared concurrent requests'],
  ] as const;
  return (
    <>
      <Field>
        <label className='lazu-label' htmlFor='decision-dialect'>
          {t('page.console.channel.form.field.decision_dialect', {
            defaultValue: 'Decision API',
          })}
        </label>
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
                {t('page.console.channel.form.decision.none', {
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
          <label className='lazu-label' htmlFor='cf-account-id'>
            {t('page.console.channel.form.field.cf_account_id', {
              defaultValue: 'Cloudflare account ID',
            })}
          </label>
          <Input
            id='cf-account-id'
            disabled={disabled}
            {...form.register('cf_account_id')}
          />
        </Field>
      )}
      {dialect && (
        <Field>
          <label className='lazu-label' htmlFor='decision-timeout'>
            {t('page.console.channel.form.field.decision_timeout_ms', {
              defaultValue: 'Decision timeout (ms)',
            })}
          </label>
          <Input
            id='decision-timeout'
            type='number'
            min={0}
            max={10000}
            disabled={disabled}
            {...form.register('decision_timeout_ms')}
          />
          <FieldMessage>
            {t('page.console.channel.form.decision.timeoutHelp', {
              defaultValue:
                '0 uses 5000ms. Total native request budget: 10 seconds.',
            })}
          </FieldMessage>
        </Field>
      )}
      <Field>
        <label className='lazu-label' htmlFor='shared-limit-group'>
          {t('page.console.channel.form.field.shared_limit_group', {
            defaultValue: 'Shared capacity group',
          })}
        </label>
        <Input
          id='shared-limit-group'
          disabled={disabled}
          {...form.register('shared_limit_group')}
        />
        <FieldMessage>
          {t('page.console.channel.form.decision.capacityHelp', {
            defaultValue:
              'Channels using the same upstream account share a group. 0 leaves a limit unset; the smallest positive shared limit applies.',
          })}
        </FieldMessage>
      </Field>
      {numericFields.map(([name, label]) => (
        <Field key={name}>
          <label className='lazu-label' htmlFor={`capacity-${name}`}>
            {t(`page.console.channel.form.field.${name}`, {
              defaultValue: label,
            })}
          </label>
          <Input
            id={`capacity-${name}`}
            type='number'
            min={0}
            disabled={disabled}
            {...form.register(name)}
          />
          <FieldMessage error>
            {form.formState.errors[name]?.message}
          </FieldMessage>
        </Field>
      ))}
    </>
  );
}
