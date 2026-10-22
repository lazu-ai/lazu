import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { API } from '../../helpers';
import { apiErrorHandler, unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';
import { Button } from '../ui';
import { PendingValue } from '../ui/pending';

/* Telegram reminders go to the Telegram account linked to this one. Linking
 * happens in Telegram: the button opens a one-time t.me link, the person taps
 * Start, and the bot completes it. Until the server says the link landed,
 * the field keeps asking, for as long as the link is valid. */

export type TelegramLinkStatus = {
  available: boolean;
  linked: boolean;
  linked_at?: number;
};

export const telegramStatusKey = [
  ...queryKeys.console.detail('self', 'telegram'),
];

export function useTelegramLinkStatus(refetchInterval?: number | false) {
  return useQuery({
    queryKey: telegramStatusKey,
    queryFn: async (): Promise<TelegramLinkStatus> =>
      unwrap<TelegramLinkStatus>(await API.get('/api/user/telegram')),
    refetchInterval,
  });
}

const ns = 'page.console.personal.notification.telegram';

export default function TelegramLinkField() {
  const { t, i18n } = useTranslation();
  const queryClient = useQueryClient();
  const [waitingUntil, setWaitingUntil] = useState<number | null>(null);
  const waiting = waitingUntil !== null && Date.now() < waitingUntil;
  const status = useTelegramLinkStatus(waiting ? 3000 : false);

  useEffect(() => {
    if (status.data?.linked) setWaitingUntil(null);
  }, [status.data?.linked]);

  const linkMutation = useMutation({
    mutationFn: async () =>
      unwrap<{ expires_in: number; url: string }>(
        await API.post('/api/user/telegram/link', {}),
      ),
    onSuccess: (data) => {
      setWaitingUntil(Date.now() + data.expires_in * 1000);
      window.open(data.url, '_blank', 'noopener');
    },
    onError: apiErrorHandler,
  });
  const unlinkMutation = useMutation({
    mutationFn: async () => unwrap(await API.delete('/api/user/telegram')),
    onSuccess: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: telegramStatusKey }),
        queryClient.invalidateQueries({ queryKey: queryKeys.user }),
      ]),
    onError: apiErrorHandler,
  });

  if (!status.data) {
    return (
      <div className='set-status' role='status'>
        <PendingValue chars={12} />
      </div>
    );
  }
  if (status.data.linked) {
    const since = status.data.linked_at
      ? new Date(status.data.linked_at * 1000).toLocaleDateString(i18n.language)
      : '';
    return (
      <div className='set-status' role='status'>
        <span className='set-status__state' data-state='ok'>
          <span aria-hidden='true' className='set-status__dot' />
          {t(`${ns}.linked`, {
            defaultValue: '已绑定 Telegram（{{date}}）',
            date: since,
          })}
        </span>
        <span className='set-status__spacer' />
        <Button
          loading={unlinkMutation.isPending}
          onClick={() => unlinkMutation.mutate()}
          size='sm'
          type='button'
          variant='ghost-danger'
        >
          {t(`${ns}.unlink`, { defaultValue: '解绑' })}
        </Button>
      </div>
    );
  }
  return (
    <div className='set-status' role='status'>
      <span className='set-status__state' data-state='off'>
        <span aria-hidden='true' className='set-status__dot' />
        {waiting
          ? t(`${ns}.waiting`, {
              defaultValue: '在 Telegram 里点「开始」完成绑定…',
            })
          : t(`${ns}.notLinked`, {
              defaultValue: '还没有绑定 Telegram，绑定后提醒会发到你的私聊',
            })}
      </span>
      <span className='set-status__spacer' />
      <Button
        disabled={!status.data.available}
        loading={linkMutation.isPending}
        onClick={() => linkMutation.mutate()}
        size='sm'
        type='button'
        variant='secondary'
      >
        {waiting
          ? t(`${ns}.relink`, { defaultValue: '重新打开 Telegram' })
          : t(`${ns}.link`, { defaultValue: '绑定 Telegram' })}
      </Button>
    </div>
  );
}
