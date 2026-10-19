import { useMutation, useQuery } from '@tanstack/react-query';
import { Send } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '../../../components/ui';
import { API, showSuccess } from '../../../helpers';
import { apiErrorHandlerWith, unwrap } from '../../../lib/api-error';
import { queryKeys } from '../../../lib/query-keys';

/* Delivery credentials come from env/yaml, so this is one line of deployment
 * fact at the top of the mail group — not a card of its own. It keeps the test
 * action, which is the one thing a reader wants next to it. */

type EmailStatusData = { configured?: boolean; provider?: string };

export default function EmailStatus() {
  const { t } = useTranslation();
  const status = useQuery({
    queryKey: [...queryKeys.console.detail('deployment', 'email')],
    queryFn: async (): Promise<EmailStatusData> => {
      const response = await API.get('/api/admin/deployment-status');
      return (response.data?.data?.email || {}) as EmailStatusData;
    },
  });
  const testMutation = useMutation({
    mutationFn: async () => {
      unwrap(await API.post('/api/admin/email/test', {}));
    },
    onSuccess: () =>
      showSuccess('page.console.settingV2.branding.emailStatus.testSent'),
    onError: apiErrorHandlerWith(
      t('page.console.settingV2.branding.emailStatus.testFailed'),
    ),
  });
  const configured = Boolean(status.data?.configured);

  return (
    <div className='set-status' role='status'>
      <span
        className='set-status__state'
        data-state={configured ? 'ok' : 'off'}
      >
        <span aria-hidden='true' className='set-status__dot' />
        {configured
          ? t('page.console.settingV2.branding.emailStatus.configured', {
              defaultValue: '邮件已配置',
            })
          : t('page.console.settingV2.branding.emailStatus.notConfigured', {
              defaultValue: '邮件服务未配置',
            })}
      </span>
      {configured ? (
        <span className='set-status__item'>
          <span aria-hidden='true' className='set-status__sep'>
            ·
          </span>
          {t('page.console.settingV2.branding.emailStatus.provider', {
            defaultValue: '通过 {{provider}} 发送',
            provider: status.data?.provider || 'SMTP',
          })}
        </span>
      ) : null}
      <span className='set-status__item'>
        <span aria-hidden='true' className='set-status__sep'>
          ·
        </span>
        {t('page.console.settingV2.branding.emailStatus.source', {
          defaultValue: '发件地址由部署环境决定',
        })}
      </span>
      <span className='set-status__spacer' />
      <Button
        loading={testMutation.isPending}
        onClick={() => testMutation.mutate()}
        size='sm'
        variant='secondary'
      >
        <Send aria-hidden='true' size={14} />
        {t('page.console.settingV2.branding.emailStatus.sendTest')}
      </Button>
    </div>
  );
}
