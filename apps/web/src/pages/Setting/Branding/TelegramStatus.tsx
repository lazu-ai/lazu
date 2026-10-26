import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { BellOff, Send } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '../../../components/ui';
import { PendingValue } from '../../../components/ui/pending';
import { API } from '../../../helpers/api';
import { showSuccess } from '../../../helpers/utils';
import { apiErrorHandlerWith, unwrap } from '../../../lib/api-error';
import { queryKeys } from '../../../lib/query-keys';

/* The bot token and the alert chat come from env/yaml, like mail delivery,
 * so this is a line of deployment fact plus the two actions an operator
 * reaches for: prove the chat works, and quiet it during maintenance. A mute
 * only silences Telegram; alert mail keeps going. */

type TelegramStatusData = {
  answer_chats?: number;
  answer_configured?: boolean;
  bot_configured?: boolean;
  ops_chat_configured?: boolean;
  ops_muted_until?: number;
  updates?: string;
};

const MUTE_MINUTES = 120;

const statusKey = [...queryKeys.console.detail('deployment', 'telegram')];

export default function TelegramStatus() {
  const { t, i18n } = useTranslation();
  const queryClient = useQueryClient();
  const status = useQuery({
    queryKey: statusKey,
    queryFn: async (): Promise<TelegramStatusData> => {
      const response = await API.get('/api/admin/deployment-status');
      return (response.data?.data?.telegram || {}) as TelegramStatusData;
    },
  });
  const testMutation = useMutation({
    mutationFn: async () => {
      unwrap(await API.post('/api/admin/telegram/test', {}));
    },
    onSuccess: () =>
      showSuccess(
        t('page.console.settingV2.branding.telegramStatus.testSent', {
          defaultValue: '测试消息已发到告警群。',
        }),
      ),
    onError: apiErrorHandlerWith(
      t('page.console.settingV2.branding.telegramStatus.testFailed', {
        defaultValue: '测试消息没有发出。',
      }),
    ),
  });
  const muteMutation = useMutation({
    mutationFn: async (minutes: number) => {
      unwrap(await API.post('/api/admin/telegram/mute', { minutes }));
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: statusKey }),
    onError: apiErrorHandlerWith(
      t('page.console.settingV2.branding.telegramStatus.muteFailed', {
        defaultValue: '静默设置没有保存。',
      }),
    ),
  });

  const data = status.data;
  const ready = Boolean(data?.bot_configured && data?.ops_chat_configured);
  const mutedUntil =
    data?.ops_muted_until && data.ops_muted_until * 1000 > Date.now()
      ? new Date(data.ops_muted_until * 1000)
      : null;

  let stateLabel;
  if (!data) {
    stateLabel = <PendingValue chars={8} />;
  } else if (ready) {
    stateLabel = t('page.console.settingV2.branding.telegramStatus.ready', {
      defaultValue: '告警同时发到 Telegram',
    });
  } else if (data.bot_configured) {
    stateLabel = t('page.console.settingV2.branding.telegramStatus.noChat', {
      defaultValue: '机器人已配置，未设告警群',
    });
  } else {
    stateLabel = t(
      'page.console.settingV2.branding.telegramStatus.notConfigured',
      { defaultValue: 'Telegram 未配置' },
    );
  }

  return (
    <div className='set-status' role='status'>
      <span
        className='set-status__state'
        data-state={data && ready && !mutedUntil ? 'ok' : 'off'}
      >
        <span aria-hidden='true' className='set-status__dot' />
        {stateLabel}
      </span>
      {mutedUntil ? (
        <span className='set-status__item'>
          <span aria-hidden='true' className='set-status__sep'>
            ·
          </span>
          {t('page.console.settingV2.branding.telegramStatus.mutedUntil', {
            defaultValue: '已静默至 {{time}}',
            time: mutedUntil.toLocaleTimeString(i18n.language, {
              hour: '2-digit',
              minute: '2-digit',
            }),
          })}
        </span>
      ) : null}
      {data?.bot_configured ? (
        <span className='set-status__item'>
          <span aria-hidden='true' className='set-status__sep'>
            ·
          </span>
          {data.answer_configured && data.updates
            ? t('page.console.settingV2.branding.telegramStatus.answerOn', {
                defaultValue: '答疑机器人已开启（{{mode}}，{{count}} 个群）',
                mode: data.updates,
                count: data.answer_chats ?? 0,
              })
            : t('page.console.settingV2.branding.telegramStatus.answerOff', {
                defaultValue: '答疑机器人未开启',
              })}
        </span>
      ) : null}
      <span className='set-status__item'>
        <span aria-hidden='true' className='set-status__sep'>
          ·
        </span>
        {t('page.console.settingV2.branding.telegramStatus.source', {
          defaultValue: '机器人和告警群由部署环境决定',
        })}
      </span>
      <span className='set-status__spacer' />
      {ready ? (
        <>
          <Button
            loading={muteMutation.isPending}
            onClick={() => muteMutation.mutate(mutedUntil ? 0 : MUTE_MINUTES)}
            size='sm'
            variant='ghost'
          >
            <BellOff aria-hidden='true' size={14} />
            {mutedUntil
              ? t('page.console.settingV2.branding.telegramStatus.unmute', {
                  defaultValue: '解除静默',
                })
              : t('page.console.settingV2.branding.telegramStatus.mute', {
                  defaultValue: '静默 2 小时',
                })}
          </Button>
          <Button
            loading={testMutation.isPending}
            onClick={() => testMutation.mutate()}
            size='sm'
            variant='secondary'
          >
            <Send aria-hidden='true' size={14} />
            {t('page.console.settingV2.branding.telegramStatus.sendTest', {
              defaultValue: '发送测试消息',
            })}
          </Button>
        </>
      ) : null}
    </div>
  );
}
