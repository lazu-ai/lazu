import { useQuery } from '@tanstack/react-query';
import dayjs from 'dayjs';
import { useContext } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Button } from '../../components/ui';
import { UserContext } from '../../context/User';
import { API } from '../../helpers/api';
import { asMicroUSD } from '../../helpers/money';
import { buildQuotaDisplay } from '../../helpers/quotaDisplay';
import { renderQuota } from '../../helpers/render';
import { timestamp2string } from '../../helpers/utils';
import { unwrap } from '../../lib/api-error';
import './token-usage-panel.css';

/** Per-Key totals use its immutable public ID and an owner-checked endpoint. */

type TokenUsageRecord = {
  id?: number | string;
  last_used_at?: number;
  created_time?: number;
  expired_time?: number;
  model_limits?: string;
  model_limits_enabled?: boolean;
  name?: string;
  remain_quota?: unknown;
  unlimited_quota?: boolean;
  used_quota?: unknown;
};

type UsageStat = {
  amount_microusd?: number;
  rpm?: number;
  tpm?: number;
};

const useTokenSpend = (tokenID: string, ownerID: string, days: number) =>
  useQuery<UsageStat>({
    enabled: Boolean(tokenID && ownerID),
    queryKey: ['token-usage-stat', ownerID, tokenID, days],
    queryFn: async () => {
      const end = dayjs();
      const start = days === 0 ? end.startOf('day') : end.subtract(days, 'day');
      const res = await API.get('/api/log/self/stat', {
        params: {
          end_timestamp: end.unix(),
          start_timestamp: start.unix(),
          token_id: tokenID,
        },
      });
      return unwrap<UsageStat | null>(res) || {};
    },
    staleTime: 60_000,
  });

const spendLabel = (stat: UsageStat | undefined, loading: boolean) => {
  if (loading) return '…';
  if (!stat || stat.amount_microusd == null) return '—';
  return renderQuota(asMicroUSD(stat.amount_microusd));
};

export function TokenUsagePanel({ record }: { record: TokenUsageRecord }) {
  const { t } = useTranslation();
  const tokenName = String(record.name || '');
  const [userState] = useContext(UserContext);
  const ownerID = String(userState?.user?.id ?? '');
  const tokenID = String(record.id ?? '');
  const week = useTokenSpend(tokenID, ownerID, 7);
  const today = useTokenSpend(tokenID, ownerID, 0);

  const quota = buildQuotaDisplay({
    remaining: asMicroUSD(record.remain_quota),
    unlimited: Boolean(record.unlimited_quota),
    used: asMicroUSD(record.used_quota),
  });

  const expired = Number(record.expired_time) || -1;
  const expiryLabel =
    expired === -1
      ? t('page.console.token.usage.neverExpires', { defaultValue: '不过期' })
      : timestamp2string(expired);
  const daysLeft =
    expired === -1 ? null : dayjs.unix(expired).diff(dayjs(), 'day');

  const facts: Array<[string, React.ReactNode]> = [
    [
      t('page.console.token.usage.today', { defaultValue: '今日消耗' }),
      spendLabel(today.data, today.isLoading),
    ],
    [
      t('page.console.token.usage.week', { defaultValue: '最近 7 天消耗' }),
      spendLabel(week.data, week.isLoading),
    ],
    [
      t('secret.lastUsed'),
      record.last_used_at
        ? timestamp2string(record.last_used_at)
        : t('common.noData', { defaultValue: '无数据' }),
    ],
    [
      t('page.console.token.usage.expiry', { defaultValue: '有效期' }),
      daysLeft == null
        ? expiryLabel
        : expired <= dayjs().unix()
          ? `${expiryLabel} · ${t('page.console.token.status.expired')}`
          : t('page.console.token.usage.expiryLeft', {
              date: expiryLabel,
              days: daysLeft,
              defaultValue: '{{date}} · 剩 {{days}} 天',
            }),
    ],
    [
      t('page.console.token.usage.models', { defaultValue: '模型范围' }),
      record.model_limits_enabled && record.model_limits
        ? record.model_limits.split(',').filter(Boolean).length +
          t('page.console.token.usage.modelCount', { defaultValue: ' 个模型' })
        : t('legacy.i18n.all_models.688facc5f5'),
    ],
    [
      t('page.console.tables.create'),
      record.created_time
        ? timestamp2string(record.created_time)
        : t('common.noData', { defaultValue: '无数据' }),
    ],
  ];

  return (
    <div className='tok-usage'>
      <div className='tok-usage__quota'>
        <span className='tok-usage__label'>
          {t('page.console.token.column.quota')}
        </span>
        {quota.mode === 'finite' ? (
          <>
            <strong className='tok-usage__value'>
              {renderQuota(quota.remaining)}
            </strong>
            <div className='tok-usage__meter'>
              <span style={{ width: `${quota.usedPercent}%` }} />
            </div>
            <span className='tok-usage__meter-line'>
              {t('page.console.token.usage.quotaLine', {
                percent: quota.usedPercent,
                total: renderQuota(quota.total),
                used: renderQuota(quota.used),
                defaultValue: '已用 {{used}} / {{total}} · {{percent}}%',
              })}
            </span>
          </>
        ) : (
          <>
            <strong className='tok-usage__value'>
              {t('legacy.i18n.unlimited_quota.b26f37b7f5')}
            </strong>
            <span className='tok-usage__meter-line'>
              {t('page.console.token.quotaUsed', {
                value: renderQuota(quota.used),
              })}
            </span>
          </>
        )}
      </div>

      <dl className='tok-usage__facts'>
        {facts.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>

      <div className='tok-usage__actions'>
        <Button
          render={
            <Link to={`/console/usage?key=${encodeURIComponent(tokenName)}`} />
          }
          size='sm'
          variant='secondary'
        >
          {t('page.console.token.usage.viewNamedLogs', {
            defaultValue: '查看此名称的 Key 请求日志 →',
          })}
        </Button>
      </div>
    </div>
  );
}

export default TokenUsagePanel;
