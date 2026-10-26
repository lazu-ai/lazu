/* Show / copy a Key's secret — the six states of S1.
 * 1 default masked · 2 fetching · 3 revealed with 30 s re-mask · 4 copied ·
 * 5 legacy digest only · 6 no permission (buttons do not exist). Secrets
 * live only in this component's state; never in a query cache. */
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '../../../components/ui/button';
import { REVEAL_REMASK_SECONDS } from '../budget';
import { useProject } from '../ProjectContext';
import { errorReceipt, receipt } from '../receipt';
import { PjBadge } from './atoms';
import type { ProjectToken } from '../types';

export function KeySecretCell({
  token,
  canReveal,
  isOwn,
  onRotate,
}: {
  token: ProjectToken;
  canReveal: boolean;
  isOwn: boolean;
  onRotate?: () => void;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const { client, current } = useProject();
  const [secret, setSecret] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [left, setLeft] = useState(0);
  const generation = useRef(0);

  // Any change of Key or project drops the plaintext immediately.
  useEffect(() => {
    generation.current += 1;
    setSecret(null);
    setLeft(0);
    return () => {
      generation.current += 1;
    };
  }, [token.id, token.version, current?.id]);

  useEffect(() => {
    if (secret === null) return;
    if (left <= 0) {
      setSecret(null);
      return;
    }
    const timer = window.setTimeout(() => setLeft((value) => value - 1), 1000);
    return () => window.clearTimeout(timer);
  }, [left, secret]);

  /* The backend only ever reports `queued`, so the receipt says the
     notification was submitted. Claiming it was delivered would be a promise
     we cannot keep until there is a delivery receipt to read. */
  const notifyText = (status: 'queued' | 'not_required') => {
    if (status === 'not_required' || isOwn) return '';
    const who = token.owner?.username;
    return who ? t('projects.key.notifyQueued', { name: who }) : '';
  };

  const fetchSecret = async () => {
    if (!client || !current) return null;
    const mine = generation.current;
    setBusy(true);
    try {
      const result = await client.revealToken(token.id, current.id);
      if (mine !== generation.current) return null;
      return result;
    } catch (error) {
      if (mine === generation.current) errorReceipt(error);
      return null;
    } finally {
      if (mine === generation.current) setBusy(false);
    }
  };

  const show = async () => {
    const result = await fetchSecret();
    if (!result) return;
    setSecret(result.key);
    setLeft(REVEAL_REMASK_SECONDS);
    const note = notifyText(result.notification_status);
    if (note)
      receipt(t('projects.key.revealReceipt', { note, name: token.name }));
  };

  const copy = async () => {
    const result = secret
      ? { key: secret, notification_status: 'not_required' as const }
      : await fetchSecret();
    if (!result) return;
    try {
      await navigator.clipboard.writeText(result.key);
      const note = secret ? '' : notifyText(result.notification_status);
      receipt(
        note
          ? t('projects.key.copiedNotified', { name: token.name, note })
          : t('projects.key.copied', { name: token.name }),
      );
    } catch {
      errorReceipt(
        new Error(
          t('secret.copyFallback', { defaultValue: '请在此字段全选并复制' }),
        ),
      );
    }
  };

  if (!canReveal) {
    return (
      <span className='pj-k'>
        <span className='pj-k__mask'>{token.masked_key}</span>
        <PjBadge>{t('projects.key.ownerOnly')}</PjBadge>
      </span>
    );
  }

  if (!token.secret_available) {
    return (
      <span className='pj-k'>
        <span className='pj-k__mask'>{token.masked_key}</span>
        <PjBadge>{t('projects.key.secretUnavailable')}</PjBadge>
        {onRotate && token.capabilities.can_rotate ? (
          <Button onClick={onRotate} size='xs' variant='ghost'>
            {t('projects.key.rotateEllipsis')}
          </Button>
        ) : null}
      </span>
    );
  }

  return (
    <span className='pj-k' data-revealed={secret !== null || undefined}>
      <span className={secret ? 'pj-k__plain' : 'pj-k__mask'}>
        {secret ?? token.masked_key}
      </span>
      {secret ? (
        <Button
          onClick={() => {
            setSecret(null);
            setLeft(0);
          }}
          size='xs'
          variant='ghost'
        >
          {t('secret.hide', { defaultValue: '隐藏' })}
        </Button>
      ) : (
        <Button
          disabled={busy}
          loading={busy}
          loadingLabel=''
          onClick={() => void show()}
          size='xs'
          variant='ghost'
        >
          {t('secret.show', { defaultValue: '显示' })}
        </Button>
      )}
      <Button
        disabled={busy}
        onClick={() => void copy()}
        size='xs'
        variant='ghost'
      >
        {t('secret.copy', { defaultValue: '复制' })}
      </Button>
      {secret ? (
        <span className='pj-cd' aria-live='off'>
          {left}s
        </span>
      ) : null}
    </span>
  );
}
