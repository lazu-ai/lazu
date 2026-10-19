/* Forgot password, in the same two beats as the sign-in code.
 *
 * It used to be one screen that, on success, cleared the address, fired a
 * toast, and turned the primary button into "Retry (27)" — which is what a
 * failure looks like. Now the first step only collects the address, and the
 * second says where the link went, how long it lives, and offers a resend on
 * the same 60-second rhythm as the code screen. The server answers the same
 * way for every address, so this screen never claims the account exists. */
import { Mail } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { z } from 'zod';
import { AuthPanel, AuthShell, AuthTextField } from './AuthLayout';
import { EMAIL_VALID_MINUTES, RESEND_SECONDS, canSendTo } from './resend';
import { API } from '../../helpers/api';
import { showError } from '../../helpers/utils';
import { unwrap } from '../../lib/api-error';
import { Button } from '../ui';
import type { CSSProperties } from 'react';

const PasswordResetForm = () => {
  const { t } = useTranslation();
  const [step, setStep] = useState<'email' | 'sent'>('email');
  const [email, setEmail] = useState('');
  /** The address the live countdown belongs to. */
  const [sentTo, setSentTo] = useState('');
  const [error, setError] = useState('');
  const [sending, setSending] = useState(false);
  const [countdown, setCountdown] = useState(0);

  const emailSchema = useMemo(
    () => z.string().trim().email(t('page.public.auth.errors.emailInvalid')),
    [t],
  );

  useEffect(() => {
    if (countdown <= 0) return undefined;
    const timer = setTimeout(() => setCountdown((value) => value - 1), 1000);
    return () => clearTimeout(timer);
  }, [countdown]);

  const sendLink = async (address: string) => {
    const parsed = emailSchema.safeParse(address);
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? '');
      return false;
    }
    setError('');
    /* The cooldown follows the address, not the screen: coming back here and
       submitting the same one again would replace the token behind a link
       that may already be open in the reader's inbox. */
    if (!canSendTo(parsed.data, sentTo, countdown)) return true;
    setSending(true);
    try {
      unwrap(
        await API.get(
          `/api/reset_password?email=${encodeURIComponent(parsed.data)}`,
        ),
      );
      setEmail(parsed.data);
      setSentTo(parsed.data);
      setCountdown(RESEND_SECONDS);
      return true;
    } catch (error) {
      showError(error, t('page.public.auth.resetEmailFailed'));
      return false;
    } finally {
      setSending(false);
    }
  };

  /* The address is the one word that has to be read exactly, so it is
     emphasised inside the sentence — same split as the code screen, so the
     translated word order stays the translator's. */
  const sentDescription = useMemo(() => {
    const text = t('page.public.auth.resetSentDescription', {
      email,
      minutes: EMAIL_VALID_MINUTES,
    });
    const at = email ? text.indexOf(email) : -1;
    if (at < 0) return text;
    return (
      <>
        {text.slice(0, at)}
        <strong>{email}</strong>
        {text.slice(at + email.length)}
      </>
    );
  }, [email, t]);

  const renderEmailStep = () => (
    <AuthPanel
      description={t('page.public.auth.resetDescription')}
      footer={
        <>
          {t('page.public.auth.rememberedPassword')}{' '}
          <Link className='auth-link' to='/login'>
            {t('page.public.auth.login')}
          </Link>
        </>
      }
      morphToken='email'
      title={t('page.public.auth.resetTitle')}
    >
      <form
        className='auth-form'
        onSubmit={async (event) => {
          event.preventDefault();
          if (await sendLink(email)) setStep('sent');
        }}
      >
        <AuthTextField
          autoComplete='email'
          error={error}
          icon={Mail}
          id='reset-email'
          label={t('page.public.auth.email')}
          name='email'
          onChange={setEmail}
          placeholder={t('page.public.auth.emailPlaceholder')}
          type='email'
          value={email}
        />
        <Button fullWidth loading={sending} type='submit' variant='primary'>
          {t('page.public.auth.resetSendLink')}
        </Button>
      </form>
    </AuthPanel>
  );

  const renderSentStep = () => (
    <AuthPanel
      description={sentDescription}
      heading='step'
      morphToken='sent'
      title={t('page.public.auth.resetSentTitle')}
    >
      <div className='auth-meta auth-meta--code'>
        <button
          aria-label={
            countdown > 0
              ? t('page.public.auth.resendCountdown', { count: countdown })
              : undefined
          }
          className='auth-resend'
          data-owns-chrome
          disabled={countdown > 0 || sending}
          onClick={() => void sendLink(email)}
          style={
            {
              '--auth-resend-progress': `${Math.round(
                ((RESEND_SECONDS - countdown) / RESEND_SECONDS) * 100,
              )}%`,
            } as CSSProperties
          }
          type='button'
        >
          <span>{t('page.public.auth.resetResend')}</span>
          {countdown > 0 ? (
            <span aria-hidden='true' className='auth-resend__count'>
              {countdown} s
            </span>
          ) : null}
        </button>

        <p className='auth-meta__row auth-meta__row--quiet'>
          <span>{t('page.public.auth.noEmailHint')}</span>
          <span aria-hidden='true'>·</span>
          <button
            onClick={() => {
              // "Another address" means another one: the field starts empty
              // rather than holding the one that was just used.
              setEmail('');
              setStep('email');
            }}
            type='button'
          >
            {t('page.public.auth.useAnotherEmail')}
          </button>
          <span aria-hidden='true'>·</span>
          <Link to='/login'>
            {t('page.public.auth.rememberedPassword')}
            {t('page.public.auth.login')}
          </Link>
        </p>
      </div>
    </AuthPanel>
  );

  return (
    <AuthShell>
      {step === 'email' ? renderEmailStep() : renderSentStep()}
    </AuthShell>
  );
};

export default PasswordResetForm;
