import { Plus } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Link, Navigate, useNavigate } from 'react-router-dom';
import { PlanStatusBadge } from './PlanStatusBadge';
import {
  formatDay,
  formatEndDay,
  laneLabel,
  pctLabel,
  planStatus,
  vendorRuleSpan,
  type PricePlan,
} from './pricePlans';
import { usePricePlansQuery } from './usePricePlans';
import {
  Button,
  ConsolePage,
  ErrorState,
  PendingBlock,
} from '../../components/ui';
import { isRoot } from '../../helpers/utils';
import { useDelayedReveal } from '../../hooks/common/useDelayedReveal';
import type { TFunction } from 'i18next';
import './model-ops.css';
import './ledger.css';
import './price-plans.css';

const ns = 'page.console.modelOps.plans';

const COLS = 'minmax(0,1.6fr) 110px 150px 100px 110px 90px 130px 96px';

function PlanRow({ plan, t }: { plan: PricePlan; t: TFunction }) {
  const vendors = vendorRuleSpan(plan.rules);
  const models = plan.rules.filter((rule) => rule.kind === 'model').length;
  const until = plan.ends_at
    ? formatEndDay(plan.ends_at)
    : t(`${ns}.forever`, { defaultValue: '长期' });
  return (
    <Link
      className='mo-ledger__row pp-row'
      to={`/console/price-plans/${plan.id}`}
    >
      <span className='pp-row__name'>
        <b>{plan.name}</b>
        {plan.note ? <small>{plan.note}</small> : null}
      </span>
      <span className='mo-num'>{pctLabel(plan.default_pct)}</span>
      <span>
        {vendors.count
          ? t(`${ns}.vendorSpan`, {
              count: vendors.count,
              defaultValue: '{{count}} 条 · {{range}}',
              range: vendors.range,
            })
          : '0'}
      </span>
      <span className='mo-num'>{models}</span>
      <span>{laneLabel(t, plan.lanes)}</span>
      <span>
        {t(`${ns}.members`, {
          count: plan.member_count,
          defaultValue: '{{count}} 人',
        })}
      </span>
      <span title={plan.starts_at ? formatDay(plan.starts_at) : undefined}>
        {until}
      </span>
      <span>
        <PlanStatusBadge status={planStatus(plan)} t={t} />
      </span>
    </Link>
  );
}

/** 专属折扣 — the plans, one row each. A row opens its editor. */
export default function PricePlansPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const root = isRoot();
  const plans = usePricePlansQuery(root);
  const reveal = useDelayedReveal(plans.isPending);

  if (!root) return <Navigate replace to='/console' />;

  return (
    <ConsolePage
      appearance='review'
      className='model-ops-page--compact'
      title={t('page.console.modelOps.nav.pricePlans', {
        defaultValue: '专属折扣',
      })}
      description={t(`${ns}.description`, {
        defaultValue: '给指定客户的折扣。',
      })}
    >
      <div className='pp-toolbar'>
        <span className='mo-sub'>
          {t(`${ns}.precedence`, {
            defaultValue: '优先级：模型 › 供应商 › 全场',
          })}
        </span>
        <Button
          variant='primary'
          onClick={() => navigate('/console/price-plans/new')}
        >
          <Plus size={15} aria-hidden='true' />
          {t(`${ns}.create`, { defaultValue: '新建方案' })}
        </Button>
      </div>

      {plans.isError ? (
        <ErrorState
          title={t(`${ns}.loadFailed`, { defaultValue: '方案加载失败' })}
          action={
            <Button variant='ghost' onClick={() => plans.refetch()}>
              {t('common.retry', { defaultValue: '重试' })}
            </Button>
          }
        />
      ) : (
        <div
          className='mo-ledger pp-ledger'
          style={{ ['--cols' as string]: COLS }}
        >
          <div className='mo-ledger__head'>
            <span>{t(`${ns}.col.plan`, { defaultValue: '方案' })}</span>
            <span>{t(`${ns}.col.default`, { defaultValue: '全场默认' })}</span>
            <span>
              {t(`${ns}.col.vendors`, { defaultValue: '供应商规则' })}
            </span>
            <span>{t(`${ns}.col.models`, { defaultValue: '模型例外' })}</span>
            <span>{t(`${ns}.col.lanes`, { defaultValue: '线路' })}</span>
            <span>{t(`${ns}.col.members`, { defaultValue: '成员' })}</span>
            <span>{t(`${ns}.col.until`, { defaultValue: '有效期' })}</span>
            <span>{t(`${ns}.col.status`, { defaultValue: '状态' })}</span>
          </div>
          {plans.isPending ? (
            [0, 1, 2].map((index) => (
              <div className='pp-row pp-row--pending' key={index}>
                {reveal ? <PendingBlock height={18} /> : null}
              </div>
            ))
          ) : plans.data.length === 0 ? (
            <div className='pp-empty'>
              {t(`${ns}.empty`, { defaultValue: '暂无方案' })}
            </div>
          ) : (
            plans.data.map((plan) => (
              <PlanRow key={plan.id} plan={plan} t={t} />
            ))
          )}
        </div>
      )}
    </ConsolePage>
  );
}
