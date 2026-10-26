import { NavLink } from 'react-router-dom';
import { PendingValue } from '../../components/ui/pending';
import { useDelayedReveal } from '../../hooks/common/useDelayedReveal';
import {
  useModelOpsSourcesQuery,
  useOfferedModelCountQuery,
} from '../../hooks/models/useModelOpsQuery';
import type { TFunction } from 'i18next';

const ns = 'page.console.modelOps';

/**
 * 模型 / 来源 — the two pages of 模型运营, with the count each holds. What
 * used to be pages of their own (资料更新, 专属折扣) are a filter on 模型 and
 * a page under 账户.
 */
export default function ModelOpsNav({ t }: { t: TFunction }) {
  const sourcesQuery = useModelOpsSourcesQuery('', 48);
  const offeredQuery = useOfferedModelCountQuery(true);
  const revealPending = useDelayedReveal(
    offeredQuery.isPending || sourcesQuery.isPending,
  );
  const items = [
    {
      count: offeredQuery.data,
      failed: offeredQuery.isError,
      label: t(`${ns}.nav.models`, { defaultValue: '模型' }),
      pending: offeredQuery.isPending,
      to: '/console/model-ops',
    },
    {
      count: sourcesQuery.data?.items?.length,
      failed: sourcesQuery.isError,
      label: t(`${ns}.nav.sources`, { defaultValue: '来源' }),
      pending: sourcesQuery.isPending,
      to: '/console/model-ops/sources',
    },
  ];

  return (
    <nav
      aria-label={t(`${ns}.nav.label`, { defaultValue: '模型运营页面' })}
      className='mo-pagenav'
    >
      {items.map((item) => (
        <NavLink
          className={({ isActive }) =>
            `mo-pagenav__item${isActive ? ' is-active' : ''}`
          }
          end
          key={item.to}
          to={item.to}
        >
          {item.label}
          <span className='mo-pagenav__count'>
            {item.failed ? (
              <span
                aria-label={t('common.status.error')}
                title={t('common.status.error')}
              >
                —
              </span>
            ) : item.pending ? (
              <span
                style={{ visibility: revealPending ? 'visible' : 'hidden' }}
              >
                <PendingValue chars={2} />
              </span>
            ) : (
              item.count
            )}
          </span>
        </NavLink>
      ))}
    </nav>
  );
}
