import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { API } from '../../../helpers/api';
import { queryKeys } from '../../../lib/query-keys';

/* CHANNEL_TEST_FREQUENCY is reapplied every time the gateway reads the monitor
 * setting, so on a deployment that sets it the probe controls below save
 * successfully and change nothing. Deployment state is reported as one line,
 * never as a form row (yunmu-console.md, "Locked settings surfaces" 6), so the
 * group says who owns the schedule instead of letting a save quietly lose.
 */

type ProbeStatusData = { env_managed?: boolean };

export default function ProbeStatus() {
  const { t } = useTranslation();
  const query = useQuery({
    queryKey: [...queryKeys.console.detail('deployment', 'channel-probe')],
    queryFn: async (): Promise<ProbeStatusData> => {
      const response = await API.get('/api/admin/deployment-status');
      return (response.data?.data?.channel_probe || {}) as ProbeStatusData;
    },
  });

  if (!query.data?.env_managed) return null;

  return (
    <div className='set-status' role='status'>
      <span className='set-status__state' data-state='off'>
        <span aria-hidden='true' className='set-status__dot' />
        {t('page.console.settingV2.upstream.probe.envManaged', {
          defaultValue: '探活由部署环境接管',
        })}
      </span>
      <span className='set-status__item'>
        <span aria-hidden='true' className='set-status__sep'>
          ·
        </span>
        {t('page.console.settingV2.upstream.probe.envManagedHint', {
          defaultValue:
            '这台部署设了 CHANNEL_TEST_FREQUENCY，它会覆盖下面的定时探活开关和间隔。',
        })}
      </span>
    </div>
  );
}
