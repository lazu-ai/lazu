import { useTranslation } from 'react-i18next';
import { formatLimit, useRateLimitOptionsQuery } from './rateLimitOptions';
import { PendingValue } from '../../../components/ui';

/* What the limits say now, on the settings card; the page behind the link
   edits them. The rows use the page's own settings row, so their values line
   up with every control above them. */
export default function RateLimitSummary() {
  const { t } = useTranslation();
  const query = useRateLimitOptionsQuery();
  const unlimited = t('page.console.settingV2.access.rateLimits.unlimited', {
    defaultValue: '不限',
  });
  const value = (figure?: number) =>
    query.isPending || figure == null ? (
      <PendingValue chars={6} />
    ) : (
      formatLimit(figure, unlimited)
    );
  const data = query.data;

  return (
    <div className='set-rl-summary'>
      <div className='set-row'>
        <span className='set-row__lb'>
          {t('page.console.settingV2.access.rateLimits.state', {
            defaultValue: '状态',
          })}
        </span>
        <span className='set-row__vv'>
          <span className='set-row__vl'>
            {query.isPending ? (
              <PendingValue chars={3} />
            ) : data?.enabled ? (
              t('page.console.settingV2.access.rateLimits.on', {
                defaultValue: '已开启',
              })
            ) : (
              t('page.console.settingV2.access.rateLimits.off', {
                defaultValue: '当前关闭',
              })
            )}
          </span>
        </span>
      </div>
      <div className='set-row'>
        <span className='set-row__lb'>
          {t('page.console.settingV2.access.rateLimits.default', {
            defaultValue: '全站默认',
          })}
        </span>
        <span className='set-row__vv'>
          <span className='set-row__vl set-rl-summary__num'>
            {value(data?.defaultRpm)} RPM · {value(data?.defaultTpm)} TPM
          </span>
        </span>
      </div>
      <div className='set-row'>
        <span className='set-row__lb'>
          {t('page.console.settingV2.access.rateLimits.unpaid', {
            defaultValue: '未充值账户',
          })}
        </span>
        <span className='set-row__vv'>
          <span className='set-row__vl set-rl-summary__num'>
            {value(data?.unpaidRpm)} RPM · {value(data?.unpaidTpm)} TPM
          </span>
        </span>
      </div>
    </div>
  );
}
