import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from './button';
import './inline-secret.css';

type InlineSecretProps = {
  masked: string;
  identity: string;
  available?: boolean;
  reveal: () => Promise<string>;
};

/** Secrets stay in this field's memory, never in query/localStorage caches. */
export function InlineSecret({
  masked,
  identity,
  available = true,
  reveal,
}: InlineSecretProps) {
  const { t } = useTranslation();
  const [secret, setSecret] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState('');
  const generation = useRef(0);
  const field = useRef<HTMLInputElement>(null);
  useEffect(() => {
    generation.current++;
    setSecret(null);
    setError('');
    setBusy(false);
    setCopied(false);
    return () => {
      generation.current++;
    };
  }, [identity, masked]);
  async function show(copy = false) {
    const current = generation.current;
    setBusy(true);
    setError('');
    try {
      const value = secret ?? (await reveal());
      if (current !== generation.current) return;
      setSecret(value);
      if (copy) {
        try {
          await navigator.clipboard.writeText(value);
          if (current === generation.current) setCopied(true);
        } catch {
          setError(
            t('secret.copyFallback', { defaultValue: '请在此字段全选并复制' }),
          );
          requestAnimationFrame(() => field.current?.select());
        }
      }
    } catch {
      if (current === generation.current)
        setError(t('secret.readFailed', { defaultValue: '无法读取，请重试' }));
    } finally {
      if (current === generation.current) setBusy(false);
    }
  }
  return (
    <div className='lazu-inline-secret'>
      <div className='lazu-inline-secret__row'>
        <input
          ref={field}
          readOnly
          aria-label='API Key'
          value={secret ?? masked}
          spellCheck={false}
        />
        <Button
          type='button'
          size='xs'
          variant='ghost'
          disabled={busy || !available}
          aria-pressed={secret !== null}
          onClick={() => {
            if (secret !== null) {
              setSecret(null);
              setCopied(false);
              setError('');
            } else void show();
          }}
        >
          {secret !== null
            ? t('secret.hide', { defaultValue: '隐藏' })
            : t('secret.show', { defaultValue: '显示' })}
        </Button>
        <Button
          type='button'
          size='xs'
          variant='ghost'
          disabled={busy || !available}
          onClick={() => void show(true)}
        >
          {copied
            ? t('secret.copied', { defaultValue: '已复制' })
            : t('secret.copy', { defaultValue: '复制' })}
        </Button>
      </div>
      {(!available || error) && (
        <span className='lazu-inline-secret__message' role='status'>
          {error ||
            t('secret.legacy', {
              defaultValue: '旧密钥仅存摘要，无法显示全文',
            })}
        </span>
      )}
    </div>
  );
}
