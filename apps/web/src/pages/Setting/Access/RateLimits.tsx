import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import {
  RATE_LIMIT_OPTION_KEYS,
  RATE_LIMIT_QUERY_KEY,
  formatLimit,
  useRateLimitOptionsQuery,
  type RateLimitOptions,
} from './rateLimitOptions';
import {
  Button,
  ErrorState,
  Input,
  PendingBlock,
  PendingValue,
  Switch,
} from '../../../components/ui';
import { coerceItemsPayload } from '../../../components/ui/table-tools';
import { notify } from '../../../components/ui/toast';
import { API } from '../../../helpers';
import { apiErrorHandler, unwrap } from '../../../lib/api-error';
import { queryKeys } from '../../../lib/query-keys';
import './rate-limits.css';

const ns = 'page.console.settingV2.access.rateLimits';

type Draft = Record<Exclude<keyof RateLimitOptions, 'enabled'>, string> & {
  enabled: boolean;
};

const toDraft = (options: RateLimitOptions): Draft => ({
  defaultRpm: String(options.defaultRpm),
  defaultTpm: String(options.defaultTpm),
  enabled: options.enabled,
  unpaidRpm: String(options.unpaidRpm),
  unpaidTpm: String(options.unpaidTpm),
});

const LIMIT_FIELDS = [
  'defaultRpm',
  'defaultTpm',
  'unpaidRpm',
  'unpaidTpm',
] as const;

const parseLimit = (value: string) => {
  const trimmed = value.replace(/,/g, '').trim();
  if (!trimmed) return 0;
  const number = Number(trimmed);
  return Number.isSafeInteger(number) && number >= 0 ? number : null;
};

type OverrideAccount = {
  id: string;
  internal_id: number;
  name: string;
  remark: string;
  rpm: number;
  tpm: number;
};

/** 设置 › 速率限制: the switch, the two ceilings, and every account that has its
 *  own (set on that account's drawer). */
export default function RateLimits() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const options = useRateLimitOptionsQuery();
  const [draft, setDraft] = useState<Draft | null>(null);

  useEffect(() => {
    if (options.data) setDraft(toDraft(options.data));
  }, [options.data]);

  const overrides = useQuery({
    queryKey: queryKeys.console.table('rate-limit-overrides', {}),
    queryFn: async () => {
      const response = await API.get('/api/user/', {
        params: {
          exclude_deleted: 'true',
          filter: 'rate_limit_override',
          p: 1,
          page_size: 100,
        },
      });
      unwrap(response);
      const page = coerceItemsPayload<Record<string, unknown>>(
        response.data,
        1,
        100,
      );
      return {
        items: page.items.map(
          (row): OverrideAccount => ({
            id: String(row.id || ''),
            internal_id: Number(row.internal_id) || 0,
            name: String(row.display_name || row.username || row.email || ''),
            remark: String(row.remark || ''),
            rpm: Number(row.rpm_limit) || 0,
            tpm: Number(row.tpm_limit) || 0,
          }),
        ),
        total: page.total,
      };
    },
  });

  const baseline = options.data ? toDraft(options.data) : null;
  const changed = (
    Object.keys(RATE_LIMIT_OPTION_KEYS) as Array<keyof Draft>
  ).filter((key) =>
    draft && baseline
      ? key === 'enabled'
        ? draft.enabled !== baseline.enabled
        : parseLimit(draft[key]) !== parseLimit(baseline[key])
      : false,
  );
  const invalid = draft
    ? LIMIT_FIELDS.filter((key) => parseLimit(draft[key]) == null)
    : [];

  const save = useMutation({
    mutationFn: async () => {
      if (!draft) return;
      for (const key of changed) {
        const value =
          key === 'enabled'
            ? String(draft.enabled)
            : String(parseLimit(draft[key]) ?? 0);
        unwrap(
          await API.put('/api/option/', {
            key: RATE_LIMIT_OPTION_KEYS[key],
            value,
          }),
        );
      }
    },
    onSuccess: () => {
      notify.success(t('common.saved', { defaultValue: '已保存' }));
      void queryClient.invalidateQueries({ queryKey: RATE_LIMIT_QUERY_KEY });
      // The upstream settings form shows the same switch.
      void queryClient.invalidateQueries({
        queryKey: [...queryKeys.console.root, 'settings'],
      });
    },
    onError: apiErrorHandler,
  });

  const unlimited = t(`${ns}.unlimited`, { defaultValue: '不限' });
  const patch = (next: Partial<Draft>) =>
    setDraft((current) => (current ? { ...current, ...next } : current));

  const limitPair = (
    label: string,
    rpmKey: (typeof LIMIT_FIELDS)[number],
    tpmKey: (typeof LIMIT_FIELDS)[number],
  ) => (
    <div className='rl-row'>
      <span className='rl-row__label'>{label}</span>
      <span className='rl-row__inputs'>
        {[
          [rpmKey, 'RPM'],
          [tpmKey, 'TPM'],
        ].map(([key, unit]) => {
          const field = key as (typeof LIMIT_FIELDS)[number];
          return (
            <span className='rl-limit' key={field}>
              {draft ? (
                <Input
                  aria-invalid={invalid.includes(field) || undefined}
                  aria-label={`${label} ${unit}`}
                  inputMode='numeric'
                  placeholder={unlimited}
                  value={draft[field] === '0' ? '' : draft[field]}
                  onChange={(event) => patch({ [field]: event.target.value })}
                />
              ) : (
                <span className='rl-limit__pending'>
                  <PendingValue chars={7} />
                </span>
              )}
              <span className='rl-unit'>{unit}</span>
            </span>
          );
        })}
      </span>
    </div>
  );

  if (options.isError) {
    return (
      <ErrorState
        title={t(`${ns}.loadFailed`, { defaultValue: '加载失败' })}
        action={
          <Button variant='ghost' onClick={() => options.refetch()}>
            {t('common.retry', { defaultValue: '重试' })}
          </Button>
        }
      />
    );
  }

  return (
    <div className='rl-page'>
      <section className='rl-card'>
        <div className='rl-row'>
          <span className='rl-row__label'>
            <b>{t(`${ns}.switch`, { defaultValue: '启用速率限制' })}</b>
            <small>
              {draft == null ? (
                <PendingValue chars={4} />
              ) : draft.enabled ? (
                t(`${ns}.on`, { defaultValue: '已开启' })
              ) : (
                t(`${ns}.off`, { defaultValue: '当前关闭' })
              )}
            </small>
          </span>
          <span>
            <Switch
              aria-label={t(`${ns}.switch`, { defaultValue: '启用速率限制' })}
              checked={draft?.enabled ?? false}
              disabled={!draft}
              onChange={(enabled) => patch({ enabled })}
            />
          </span>
        </div>
        {limitPair(
          t(`${ns}.default`, { defaultValue: '全站默认' }),
          'defaultRpm',
          'defaultTpm',
        )}
        {limitPair(
          t(`${ns}.unpaid`, { defaultValue: '未充值账户' }),
          'unpaidRpm',
          'unpaidTpm',
        )}
        <div className='rl-save'>
          {invalid.length ? (
            <span className='rl-error'>
              {t(`${ns}.invalid`, { defaultValue: '填非负整数' })}
            </span>
          ) : null}
          <Button
            disabled={!changed.length || save.isPending}
            onClick={() => options.data && setDraft(toDraft(options.data))}
          >
            {t(`${ns}.revert`, { defaultValue: '撤销' })}
          </Button>
          <Button
            variant='primary'
            disabled={!changed.length || invalid.length > 0}
            loading={save.isPending}
            onClick={() => save.mutate()}
          >
            {t(`${ns}.save`, { defaultValue: '保存' })}
          </Button>
        </div>
      </section>

      <section className='rl-card'>
        <div className='rl-card__head'>
          <b>{t(`${ns}.overrides`, { defaultValue: '单独设置的账户' })}</b>
        </div>
        <div className='rl-trow rl-trow--head'>
          <span>{t(`${ns}.account`, { defaultValue: '账户' })}</span>
          <span className='rl-num'>RPM</span>
          <span className='rl-num'>TPM</span>
          <span>{t(`${ns}.remark`, { defaultValue: '备注' })}</span>
          <span />
        </div>
        {overrides.isPending ? (
          <PendingBlock height={46} />
        ) : overrides.isError ? (
          <div className='rl-none'>
            {t(`${ns}.overridesFailed`, { defaultValue: '加载失败' })}{' '}
            <Button
              size='xs'
              variant='ghost'
              onClick={() => overrides.refetch()}
            >
              {t('common.retry', { defaultValue: '重试' })}
            </Button>
          </div>
        ) : overrides.data.items.length === 0 ? (
          <div className='rl-none'>
            {t(`${ns}.none`, { defaultValue: '暂无' })}
          </div>
        ) : (
          overrides.data.items.map((account) => (
            <div className='rl-trow' key={account.id}>
              <span className='rl-who'>
                <span>{account.name}</span>
                <small>#{account.internal_id}</small>
              </span>
              <span className='rl-num'>
                {account.rpm
                  ? formatLimit(account.rpm, unlimited)
                  : t(`${ns}.inherit`, { defaultValue: '默认' })}
              </span>
              <span className='rl-num'>
                {account.tpm
                  ? formatLimit(account.tpm, unlimited)
                  : t(`${ns}.inherit`, { defaultValue: '默认' })}
              </span>
              <span className='rl-remark'>{account.remark}</span>
              <Link
                className='rl-open'
                to={`/console/organizations?account=${encodeURIComponent(account.id)}`}
              >
                {t(`${ns}.open`, { defaultValue: '打开' })}
              </Link>
            </div>
          ))
        )}
      </section>
    </div>
  );
}
