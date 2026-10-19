import {
  AlertTriangle,
  ChevronRight,
  Plus,
  Save,
  ShieldAlert,
  Trash2,
  UserRound,
} from 'lucide-react';
import React, { useState } from 'react';
import { EMPTY_CELL, UserIdentity, type LogBadgeVariant } from './log-support';
import {
  Badge,
  Button,
  EmptyState,
  Field,
  Input,
  Select,
  SelectItem,
  Switch,
  Tag,
} from '../../components/ui';
import { TextStack, compactNumber } from '../../components/ui/table-tools';
import { asMicroUSD } from '../../helpers/money';
import {
  microUSDToUSD,
  renderQuota,
  usdToMicroUSD,
} from '../../helpers/render';
import { useSpendBudgetsQuery } from '../../hooks/usage-logs/useUsageLogsQuery';
import './log-table.css';
import type {
  SpendBudget,
  SpendBudgetAlert,
  SpendBudgetDraft,
  UsageUserAggregateRow,
} from '../../hooks/usage-logs/useUsageLogsQuery';
import type { TFunction } from 'i18next';

export type LogViewMode = 'requests' | 'budgets';
type SpendBudgetsQueryState = ReturnType<typeof useSpendBudgetsQuery>;

const createBudgetDraft = (): SpendBudgetDraft => ({
  burst_multiplier: 2,
  enabled: true,
  enforcement: 'block',
  limit_microusd: 100_000_000,
  period: 'month',
  scope: 'token',
  token_id: 0,
  user_id: 0,
  warning_threshold_pct: 80,
});

const spendBudgetVariant = (type: string | undefined): LogBadgeVariant => {
  if (type === 'blocked' || type === 'exceeded') {
    return 'danger';
  }
  if (type === 'burst') {
    return 'warning';
  }
  return 'info';
};

const budgetScopeLabel = (scope: string | undefined, t: TFunction) => {
  const labels: Record<string, string> = {
    org: t('page.console.log.budget.scope.org'),
    token: t('page.console.log.budget.scope.token'),
    user: t('page.console.log.budget.scope.user'),
  };
  return labels[scope || 'org'] || labels.org;
};

const budgetTargetLabel = (budget: SpendBudget, t: TFunction) => {
  if (budget.scope === 'token') {
    return t('page.console.log.budget.target.token', {
      id: budget.token_id || '-',
    });
  }
  if (budget.scope === 'user') {
    return t('page.console.log.budget.target.user', {
      id: budget.user_id || '-',
    });
  }
  return t('page.console.log.budget.target.org');
};

const alertTargetLabel = (alert: SpendBudgetAlert, t: TFunction) => {
  if (alert.token_id) {
    return t('page.console.log.budget.target.token', { id: alert.token_id });
  }
  if (alert.user_id) {
    return t('page.console.log.budget.target.user', { id: alert.user_id });
  }
  return t('page.console.log.budget.target.org');
};

export function BudgetGuardPanel({
  query,
  t,
}: {
  query: SpendBudgetsQueryState;
  t: TFunction;
}) {
  const [draft, setDraft] = useState<SpendBudgetDraft>(createBudgetDraft);
  const patchDraft = (patch: Partial<SpendBudgetDraft>) => {
    setDraft((current) => ({ ...current, ...patch }));
  };
  const editBudget = (budget: SpendBudget) => {
    setDraft({
      burst_multiplier: Number(budget.burst_multiplier || 0),
      enabled: budget.enabled !== false,
      enforcement: budget.enforcement || 'block',
      id: budget.id,
      limit_microusd: Number(budget.limit_microusd || 0),
      period: budget.period || 'month',
      scope: budget.scope || 'org',
      token_id: Number(budget.token_id || 0),
      user_id: Number(budget.user_id || 0),
      warning_threshold_pct: Number(budget.warning_threshold_pct || 80),
    });
  };
  const resetDraft = () => {
    setDraft(createBudgetDraft());
  };
  const submitBudget = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    query.saveBudget(draft);
  };

  return (
    <div className='grid gap-5'>
      <form
        className='grid gap-3 border-b border-[var(--ui-border)] pb-4'
        onSubmit={submitBudget}
      >
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <TextStack title={t('page.console.log.budget.form.title')} />
          <Button type='button' size='sm' variant='ghost' onClick={resetDraft}>
            <Plus size={15} />
            {t('page.console.log.budget.action.new')}
          </Button>
        </div>
        <div className='grid gap-3 md:grid-cols-2 xl:grid-cols-4'>
          <Field>
            <label className='lazu-label'>
              {t('page.console.log.budget.form.scope')}
            </label>
            <Select
              value={draft.scope}
              onValueChange={(value) =>
                patchDraft({
                  scope: value as SpendBudgetDraft['scope'],
                  token_id: value === 'token' ? draft.token_id : 0,
                  user_id: value === 'user' ? draft.user_id : 0,
                })
              }
            >
              <SelectItem value='token'>
                {t('page.console.log.budget.scope.token')}
              </SelectItem>
              <SelectItem value='user'>
                {t('page.console.log.budget.scope.user')}
              </SelectItem>
              <SelectItem value='org'>
                {t('page.console.log.budget.scope.org')}
              </SelectItem>
            </Select>
          </Field>
          <Field>
            <label className='lazu-label' htmlFor='budget-token-id'>
              {t('page.console.log.budget.form.tokenId')}
            </label>
            <Input
              id='budget-token-id'
              type='number'
              min='0'
              disabled={draft.scope !== 'token'}
              value={draft.token_id}
              onChange={(event) =>
                patchDraft({ token_id: Number(event.target.value) || 0 })
              }
            />
          </Field>
          <Field>
            <label className='lazu-label' htmlFor='budget-user-id'>
              {t('page.console.log.budget.form.userId')}
            </label>
            <Input
              id='budget-user-id'
              type='number'
              min='0'
              disabled={draft.scope !== 'user'}
              value={draft.user_id}
              onChange={(event) =>
                patchDraft({ user_id: Number(event.target.value) || 0 })
              }
            />
          </Field>
          <Field>
            <label className='lazu-label'>
              {t('page.console.log.budget.form.period')}
            </label>
            <Select
              value={draft.period}
              onValueChange={(value) =>
                patchDraft({ period: value as SpendBudgetDraft['period'] })
              }
            >
              <SelectItem value='day'>
                {t('page.console.log.budget.period.day')}
              </SelectItem>
              <SelectItem value='month'>
                {t('page.console.log.budget.period.month')}
              </SelectItem>
            </Select>
          </Field>
          <Field>
            <label className='lazu-label' htmlFor='budget-limit'>
              {t('page.console.log.budget.form.limit')}
            </label>
            <Input
              id='budget-limit'
              type='number'
              min='0'
              step='0.000001'
              value={microUSDToUSD(asMicroUSD(draft.limit_microusd))}
              onChange={(event) =>
                patchDraft({
                  limit_microusd: Number(usdToMicroUSD(event.target.value)),
                })
              }
            />
          </Field>
          <Field>
            <label className='lazu-label' htmlFor='budget-warning'>
              {t('page.console.log.budget.form.warningThreshold')}
            </label>
            <Input
              id='budget-warning'
              type='number'
              min='1'
              max='99'
              value={draft.warning_threshold_pct}
              onChange={(event) =>
                patchDraft({
                  warning_threshold_pct: Number(event.target.value) || 80,
                })
              }
            />
          </Field>
          <Field>
            <label className='lazu-label' htmlFor='budget-burst'>
              {t('page.console.log.budget.form.burstMultiplier')}
            </label>
            <Input
              id='budget-burst'
              type='number'
              min='0'
              step='0.1'
              value={draft.burst_multiplier}
              onChange={(event) =>
                patchDraft({ burst_multiplier: Number(event.target.value) })
              }
            />
          </Field>
          <Field>
            <label className='lazu-label'>
              {t('page.console.log.budget.form.enforcement')}
            </label>
            <Select
              value={draft.enforcement}
              onValueChange={(value) =>
                patchDraft({
                  enforcement: value as SpendBudgetDraft['enforcement'],
                })
              }
            >
              <SelectItem value='block'>
                {t('page.console.log.budget.enforcement.block')}
              </SelectItem>
              <SelectItem value='alert'>
                {t('page.console.log.budget.enforcement.alert')}
              </SelectItem>
            </Select>
          </Field>
        </div>
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <Switch
            checked={draft.enabled}
            onCheckedChange={(checked) => patchDraft({ enabled: checked })}
          >
            {t('page.console.log.budget.form.enabled')}
          </Switch>
          <Button type='submit' loading={query.savePending}>
            <Save size={15} />
            {t('page.console.log.budget.action.save')}
          </Button>
        </div>
      </form>

      <div className='grid gap-3'>
        <TextStack title={t('page.console.log.budget.table.title')} />
        {query.budgets.length ? (
          <div className='lazu-table-wrap'>
            <table className='lazu-table lazu-table--compact'>
              <thead>
                <tr>
                  <th>{t('page.console.log.budget.column.target')}</th>
                  <th>{t('page.console.log.budget.column.period')}</th>
                  <th className='text-right'>
                    {t('page.console.log.budget.column.limit')}
                  </th>
                  <th>{t('page.console.log.budget.column.guardrails')}</th>
                  <th>{t('page.console.log.budget.column.status')}</th>
                  <th className='text-right'>
                    {t('page.console.log.budget.column.actions')}
                  </th>
                </tr>
              </thead>
              <tbody>
                {query.budgets.map((budget) => (
                  <tr key={budget.id}>
                    <td>
                      <TextStack
                        title={budgetTargetLabel(budget, t)}
                        description={budgetScopeLabel(budget.scope, t)}
                      />
                    </td>
                    <td>
                      <Tag>
                        {budget.period === 'day'
                          ? t('page.console.log.budget.period.day')
                          : t('page.console.log.budget.period.month')}
                      </Tag>
                    </td>
                    <td className='text-right font-mono'>
                      {renderQuota(asMicroUSD(budget.limit_microusd || 0), 6)}
                    </td>
                    <td>
                      <span className='inline-flex flex-wrap gap-1'>
                        <Badge variant='info'>
                          {t('page.console.log.budget.warningValue', {
                            value: budget.warning_threshold_pct || 80,
                          })}
                        </Badge>
                        {Number(budget.burst_multiplier || 0) > 0 ? (
                          <Badge variant='warning'>
                            {t('page.console.log.budget.burstValue', {
                              value: budget.burst_multiplier,
                            })}
                          </Badge>
                        ) : null}
                        <Badge
                          variant={
                            budget.enforcement === 'alert'
                              ? 'warning'
                              : 'danger'
                          }
                        >
                          {budget.enforcement === 'alert'
                            ? t('page.console.log.budget.enforcement.alert')
                            : t('page.console.log.budget.enforcement.block')}
                        </Badge>
                      </span>
                    </td>
                    <td>
                      <Badge
                        variant={
                          budget.enabled === false ? 'neutral' : 'success'
                        }
                      >
                        {budget.enabled === false
                          ? t('page.console.log.budget.status.disabled')
                          : t('page.console.log.budget.status.enabled')}
                      </Badge>
                    </td>
                    <td className='text-right'>
                      <div className='inline-flex gap-1'>
                        <Button
                          size='xs'
                          variant='ghost'
                          onClick={() => editBudget(budget)}
                        >
                          {t('page.console.tables.edit')}
                        </Button>
                        <Button
                          size='xs'
                          variant='ghost'
                          loading={query.deletePending}
                          onClick={() => query.deleteBudget(budget.id)}
                        >
                          <Trash2 size={12} />
                          {t('page.console.tables.delete')}
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <EmptyState
            icon={<ShieldAlert size={24} />}
            title={t('page.console.log.budget.empty.title')}
            description={t('page.console.log.budget.empty.description')}
          />
        )}
      </div>

      <div className='grid gap-3'>
        <TextStack title={t('page.console.log.budget.alerts.title')} />
        {query.alerts.length ? (
          <div className='lazu-table-wrap'>
            <table className='lazu-table lazu-table--compact'>
              <thead>
                <tr>
                  <th>{t('page.console.log.budget.column.type')}</th>
                  <th>{t('page.console.log.budget.column.target')}</th>
                  <th className='text-right'>
                    {t('page.console.log.budget.column.spent')}
                  </th>
                  <th>{t('page.console.log.budget.column.session')}</th>
                  <th>{t('page.console.log.budget.column.request')}</th>
                  <th>{t('page.console.log.budget.column.created')}</th>
                </tr>
              </thead>
              <tbody>
                {query.alerts.map((alert) => (
                  <tr key={alert.id}>
                    <td>
                      <Badge variant={spendBudgetVariant(alert.alert_type)}>
                        {alert.alert_type || '-'}
                      </Badge>
                    </td>
                    <td>
                      <TextStack
                        title={alertTargetLabel(alert, t)}
                        description={alert.agent_id || alert.message || ''}
                      />
                    </td>
                    <td className='text-right'>
                      <TextStack
                        title={renderQuota(
                          asMicroUSD(alert.spent_microusd || 0),
                          6,
                        )}
                        description={t('page.console.log.budget.alertDelta', {
                          value: renderQuota(
                            asMicroUSD(alert.delta_microusd || 0),
                            6,
                          ),
                        })}
                      />
                    </td>
                    <td>
                      {alert.session_id ? (
                        <button
                          type='button'
                          className='max-w-[12rem] truncate font-mono text-xs underline-offset-4 hover:underline'
                        >
                          {alert.session_id}
                        </button>
                      ) : (
                        '-'
                      )}
                    </td>
                    <td>
                      {alert.request_id ? (
                        <span className='font-mono text-xs'>
                          {alert.request_id}
                        </span>
                      ) : (
                        '-'
                      )}
                    </td>
                    <td>{alert.created_at || '-'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <EmptyState
            icon={<AlertTriangle size={24} />}
            title={t('page.console.log.budget.alerts.empty.title')}
            description={t('page.console.log.budget.alerts.empty.description')}
          />
        )}
      </div>
    </div>
  );
}

export function UsageUserRanking({
  loading,
  onDrill,
  peakMicrousd,
  rows,
  t,
  totalMicrousd,
}: {
  loading: boolean;
  onDrill: (row: UsageUserAggregateRow) => void;
  peakMicrousd: number;
  rows: UsageUserAggregateRow[];
  t: TFunction;
  totalMicrousd: number;
}) {
  return (
    <div className='logv2-card'>
      <div className='logv2-scroll'>
        <table className='logv2-table'>
          <thead>
            <tr>
              <th className='logv2-rank' />
              <th>{t('page.console.log.column.user')}</th>
              <th className='logv2-num'>
                {t('page.console.log.users.requests', {
                  defaultValue: '请求',
                })}
              </th>
              <th className='logv2-num'>Tokens</th>
              <th className='logv2-num'>
                {t('page.console.log.users.spend', { defaultValue: '消费' })}
              </th>
              <th className='logv2-num'>
                {t('page.console.log.users.errorRate', {
                  defaultValue: '错误率',
                })}
              </th>
              <th>
                {t('page.console.log.users.share', { defaultValue: '占比' })}
              </th>
              <th />
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr>
                <td className='logv2-state' colSpan={8}>
                  {t('legacy.i18n.loading.514c33af5c')}
                </td>
              </tr>
            ) : rows.length === 0 ? (
              <tr>
                <td colSpan={8}>
                  <EmptyState
                    icon={<UserRound size={24} />}
                    title={t('legacy.i18n.no_results_found.3f5746269e')}
                    description={t('page.console.log.users.emptyDescription', {
                      defaultValue: '当前时间窗口内没有产生用量的用户。',
                    })}
                  />
                </td>
              </tr>
            ) : (
              rows.map((row, index) => {
                const requests = Number(row.request_count || 0);
                const amount = Number(row.amount_microusd || 0);
                const tokens =
                  Number(row.prompt_tokens || 0) +
                  Number(row.completion_tokens || 0);
                // error_count only exists when the routing-outcome join is
                // available; absent means "not measured", never "zero".
                const errorRate =
                  row.error_count === undefined ||
                  row.error_count === null ||
                  requests <= 0
                    ? null
                    : (Number(row.error_count) / requests) * 100;
                const share =
                  peakMicrousd > 0
                    ? Math.max(2, Math.round((amount / peakMicrousd) * 100))
                    : 0;
                return (
                  <tr className='logv2-row' key={`${row.id}-${row.name}`}>
                    <td className='logv2-rank'>
                      {String(index + 1).padStart(2, '0')}
                    </td>
                    <td>
                      <UserIdentity
                        name={String(row.name || '')}
                        uid={row.id}
                      />
                    </td>
                    <td className='logv2-num'>{compactNumber(requests)}</td>
                    <td className='logv2-num'>{compactNumber(tokens)}</td>
                    <td className='logv2-num'>
                      <b>{renderQuota(asMicroUSD(amount), 6)}</b>
                    </td>
                    <td
                      className={
                        errorRate !== null && errorRate >= 1
                          ? 'logv2-num logv2-warn'
                          : 'logv2-num logv2-soft'
                      }
                    >
                      {errorRate === null
                        ? EMPTY_CELL
                        : `${errorRate.toFixed(1)}%`}
                    </td>
                    <td>
                      <span className='logv2-bar'>
                        <i style={{ width: `${share}%` }} />
                      </span>
                    </td>
                    <td>
                      <Button
                        size='xs'
                        variant='ghost'
                        onClick={() => onDrill(row)}
                      >
                        {t('page.console.log.users.drill', {
                          defaultValue: '查看明细',
                        })}
                        <ChevronRight aria-hidden='true' size={13} />
                      </Button>
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>
      <div className='logv2-foot'>
        <span>
          {t('page.console.log.footer.usersSummary', {
            cost: renderQuota(asMicroUSD(totalMicrousd), 6),
            count: rows.length,
            defaultValue: '{{count}} 个活跃用户 · 合计 {{cost}}',
          })}
        </span>
        <span className='logv2-faint'>
          {t('page.console.log.users.windowNote', {
            defaultValue:
              '按时间窗口聚合消费前 50 名，不含模型 / Key / 类型筛选',
          })}
        </span>
      </div>
    </div>
  );
}
