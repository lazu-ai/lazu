import { Badge } from '../../components/ui/badge';
import type { PlanStatus } from './pricePlans';
import type { TFunction } from 'i18next';

const ns = 'page.console.modelOps.plans';

export function PlanStatusBadge({
  status,
  t,
}: {
  status: PlanStatus;
  t: TFunction;
}) {
  if (status === 'live')
    return (
      <Badge variant='success'>
        {t(`${ns}.status.live`, { defaultValue: '生效中' })}
      </Badge>
    );
  if (status === 'upcoming')
    return (
      <Badge>{t(`${ns}.status.upcoming`, { defaultValue: '未开始' })}</Badge>
    );
  if (status === 'expired')
    return (
      <Badge>{t(`${ns}.status.expired`, { defaultValue: '已过期' })}</Badge>
    );
  return <Badge>{t(`${ns}.status.off`, { defaultValue: '已停用' })}</Badge>;
}
