/* The single door.
 *
 * There is no "sign in or sign up?" question here, because it is a question
 * only the server can answer: the address is typed once, a code goes to that
 * mailbox, and whoever proves they hold it either lands in their account or
 * gets one. That removes the choice that used to send new people down the
 * login path and refuse them.
 *
 * Password sign-in lives here too, on the same screen. It used to sit on its
 * own route, and because both routes mount under a `key={location.pathname}`
 * Suspense boundary, following that link tore down the whole subtree — the
 * WebGL ground included — and rebuilt it. Now the two credentials are one
 * piece of state behind a segmented switch: they are never on screen at the
 * same time, and moving between them never navigates.
 *
 * Which credential to offer is deliberately NOT asked of the server. The
 * elegant version of this screen would take the address, ask whether that
 * account has a password, and show the right field — but "this address has a
 * password" is an answer to "does this account exist", so the switch is driven
 * by the visitor and by the site-level flags only.
 *
 * Consent is the notice under the button rather than a checkbox above it —
 * what makes it hold up is a conspicuous link next to the control you press,
 * and the version and timestamp the server records on the account.
 */
import { Lock, Mail, User, Users } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Link,
  useLocation,
  useNavigate,
  useSearchParams,
} from 'react-router-dom';
import { z } from 'zod';
import {
  AuthDivider,
  AuthPanel,
  AuthReveal,
  AuthSegmented,
  AuthShell,
  AuthTextField,
} from './AuthLayout';
import DoorSkeleton, { endDoorEntry, useDoorReveal } from './DoorSkeleton';
import { readStoredSessionHint, startLoginSessionProbe } from './loginSession';
import OtpInput from './OtpInput';
import { EMAIL_VALID_MINUTES, RESEND_SECONDS, canSendTo } from './resend';
import SsoButton from './SsoButton';
import TermsNotice from './TermsNotice';
import useAuthStatus from './useAuthStatus';
import { useSession } from '../../context/User';
import { API, updateAPI } from '../../helpers/api';
import { buildAPIURL } from '../../helpers/apiUrl';
import {
  safeInternalAuthPath,
  postLoginDestination,
} from '../../helpers/authNavigation';
import { setUserData } from '../../helpers/data';
import { showError, showSuccess, showWarning } from '../../helpers/utils';
import { unwrap } from '../../lib/api-error';
import { Button } from '../ui';
import type { CSSProperties } from 'react';

const CODE_LENGTH = 6;
// One browser-level preference, written only after a verified sign-in.
// No account identifier is stored or looked up to choose the first screen.
const LAST_CREDENTIAL_KEY = 'lazu.lastLoginCredential.v1';

type AuthCredential = 'code' | 'password';

/* Arriving from a project invitation: the address is already known, the
   code goes there, and signing in returns to the invitation to accept it. */
type InviteContext = {
  token: string;
  email: string;
  project: string;
  inviter: string;
};

const readInviteContext = (state: unknown): InviteContext | null => {
  const invite = (state as { invite?: Partial<InviteContext> } | null)?.invite;
  if (!invite?.token || !invite.email) return null;
  return {
    token: String(invite.token),
    email: String(invite.email),
    project: String(invite.project ?? ''),
    inviter: String(invite.inviter ?? ''),
  };
};
type FieldErrors = { identifier?: string; password?: string };

const readLastCredential = (): AuthCredential => {
  try {
    return globalThis.localStorage?.getItem(LAST_CREDENTIAL_KEY) === 'password'
      ? 'password'
      : 'code';
  } catch {
    return 'code';
  }
};

const rememberCredential = (credential: AuthCredential) => {
  try {
    globalThis.localStorage?.setItem(LAST_CREDENTIAL_KEY, credential);
  } catch {
    // Storage is only a preference; a successful login must still complete.
  }
};

type PasswordCredentialWindow = Window & {
  PasswordCredential?: new (data: {
    id: string;
    origin: string;
    password: string;
  }) => Credential;
};

const offerToSavePassword = (identifier: string, password: string) => {
  const PasswordCredential = (window as PasswordCredentialWindow)
    .PasswordCredential;
  if (!PasswordCredential || !navigator.credentials?.store) return;
  try {
    // Chrome can recognize a successful SPA sign-in heuristically, but an
    // explicit store request avoids depending on that detection. The browser
    // decides whether to offer to save; rejection must not affect sign-in.
    void navigator.credentials
      .store(
        new PasswordCredential({
          id: identifier,
          origin: window.location.origin,
          password,
        }),
      )
      .catch(() => undefined);
  } catch {
    // Password saving is optional and must never block a successful sign-in.
  }
};

const LoginForm = () => {
  const navigate = useNavigate();
  const location = useLocation();
  const { t } = useTranslation();
  const [searchParams] = useSearchParams();
  const userSession = useSession();
  const { ready, status: authStatus } = useAuthStatus();
  // A stored user is only a hint: the backend's cookie session decides
  // whether this visitor is already signed in.
  const [sessionProbe] = useState(() => {
    if (searchParams.has('expired') || searchParams.has('error')) return null;
    const hint = readStoredSessionHint();
    return hint ? startLoginSessionProbe(hint) : null;
  });
  const [checkingSession, setCheckingSession] = useState(Boolean(sessionProbe));

  const [step, setStep] = useState<'code' | 'door'>('door');
  const [credential, setCredential] =
    useState<AuthCredential>(readLastCredential);
  // One field serves both credentials: an address for the code, an address or
  // a username for the password. Whatever was typed survives the switch.
  const [identifier, setIdentifier] = useState('');
  const [password, setPassword] = useState('');
  const [email, setEmail] = useState('');
  const [code, setCode] = useState('');
  const [errors, setErrors] = useState<FieldErrors>({});
  const [sending, setSending] = useState(false);
  const [verifying, setVerifying] = useState(false);
  const [signingIn, setSigningIn] = useState(false);
  const [countdown, setCountdown] = useState(0);
  const submittedCode = useRef('');
  const [invite] = useState(() => readInviteContext(location.state));
  const inviteStarted = useRef(false);

  const doorRevealed = useDoorReveal(!ready || checkingSession);
  useEffect(() => {
    if (ready && !checkingSession) endDoorEntry();
  }, [ready, checkingSession]);

  const requestedDestination = useMemo(() => {
    const stateFrom = (
      location.state as {
        from?: { pathname?: string; search?: string; hash?: string };
      } | null
    )?.from;
    const candidate =
      searchParams.get('next') ||
      (stateFrom?.pathname
        ? `${stateFrom.pathname}${stateFrom.search || ''}${stateFrom.hash || ''}`
        : '/console');
    return safeInternalAuthPath(candidate);
  }, [location.state, searchParams]);

  useEffect(() => {
    if (!checkingSession || !sessionProbe) return undefined;
    let cancelled = false;
    const releaseDoor = () => {
      if (cancelled) return;
      cancelled = true;
      setCheckingSession(false);
    };
    const onStorage = (event: StorageEvent) => {
      if (event.key === 'user' && event.newValue !== sessionProbe.hint)
        releaseDoor();
    };
    window.addEventListener('storage', onStorage);

    void sessionProbe.result.then((result) => {
      try {
        if (cancelled) return;
        // Another tab may have signed in since this request began. Never
        // overwrite or clear its newer identity with this older result.
        if (readStoredSessionHint() !== sessionProbe.hint) {
          releaseDoor();
          return;
        }
        if (result.kind === 'authenticated') {
          const user = result.user;
          userSession.signIn(user);
          setUserData(user);
          updateAPI();
          cancelled = true;
          navigate(postLoginDestination(requestedDestination, user.role), {
            replace: true,
          });
          return;
        }
        if (result.kind === 'unauthorized') {
          localStorage.removeItem('user');
          userSession.signOut();
          updateAPI();
        }
        // HTTP 200 success=false and transport failures do not prove the
        // cookie session is invalid; a fresh sign-in remains available.
      } finally {
        if (!cancelled) setCheckingSession(false);
      }
    });
    return () => {
      cancelled = true;
      window.removeEventListener('storage', onStorage);
    };
  }, [
    checkingSession,
    navigate,
    requestedDestination,
    sessionProbe,
    userSession.signIn,
    userSession.signOut,
  ]);

  useEffect(() => {
    if (searchParams.get('expired')) {
      showError(t('page.public.auth.errors.sessionExpired'));
    }
    // Failed OAuth/OIDC redirects land here as ?error=<catalog code>, which
    // reads like any other failure the server reports.
    const oauthError = searchParams.get('error');
    if (oauthError) showError({ error: { code: oauthError } });
  }, [searchParams, t]);

  useEffect(() => {
    if (countdown <= 0) return undefined;
    const timer = setTimeout(() => setCountdown((value) => value - 1), 1000);
    return () => clearTimeout(timer);
  }, [countdown]);

  const emailSchema = useMemo(
    () => z.string().trim().email(t('page.public.auth.errors.emailInvalid')),
    [t],
  );

  const providers = authStatus?.providers ?? [];
  const emailCodeAuthAvailable = Boolean(authStatus?.email_code_auth);
  /* `password_login` only governs deployments that have another way in
     configured; with no provider at all it is the only door there is. Note
     what is not here any more: a `!authStatus` clause. An unanswered status
     used to read as "password only", which is how a screen nobody had asked
     for got painted while the real answer was still in flight. */
  const passwordLoginAvailable = authStatus
    ? !authStatus.enabled || authStatus.password_login || authStatus.break_glass
    : false;

  // The switch only appears when both ways in are actually open; otherwise the
  // single available credential is used without asking.
  const bothCredentials = emailCodeAuthAvailable && passwordLoginAvailable;
  const activeCredential: AuthCredential | null = bothCredentials
    ? credential
    : emailCodeAuthAvailable
      ? 'code'
      : passwordLoginAvailable
        ? 'password'
        : null;
  const isPasswordMode = activeCredential === 'password';

  const completeLogin = (data: Record<string, unknown>) => {
    userSession.signIn(data);
    localStorage.setItem('user', JSON.stringify(data));
    setUserData(data);
    updateAPI();
    showSuccess(t('page.public.auth.loginSuccess'));
    navigate(postLoginDestination(requestedDestination, data.role), {
      replace: true,
      // The invitation page accepts on arrival instead of asking again.
      state: invite ? { autoAccept: true } : undefined,
    });
  };

  const sendCode = async (address: string) => {
    const parsed = emailSchema.safeParse(address);
    if (!parsed.success) {
      setErrors({ identifier: parsed.error.issues[0]?.message ?? '' });
      return false;
    }
    setErrors({});
    /* The cooldown follows the address, not the screen: stepping back and
       submitting the same one again would burn the code already sent. */
    if (!canSendTo(parsed.data, email, countdown)) return true;
    setSending(true);
    try {
      unwrap(
        await API.post('/api/auth/email/start', {
          email: parsed.data,
          invite: invite?.token,
        }),
      );
      setEmail(parsed.data);
      setCountdown(RESEND_SECONDS);
      return true;
    } catch (failure) {
      showError(failure, t('page.public.auth.errors.verificationCodeFailed'));
      return false;
    } finally {
      setSending(false);
    }
  };

  const verifyCode = async (value: string) => {
    if (verifying) return;
    setVerifying(true);
    try {
      const data = unwrap<Record<string, unknown>>(
        await API.post('/api/auth/email/verify', {
          code: value,
          email,
          invite: invite?.token,
        }),
      );
      rememberCredential('code');
      completeLogin(data);
    } catch (failure) {
      showError(failure, t('page.public.auth.errors.loginFailed'));
      // A refused code has been consumed or burned server-side; clearing the
      // field stops the next keystroke from resubmitting the same digits.
      setCode('');
      submittedCode.current = '';
    } finally {
      setVerifying(false);
    }
  };

  // A six-digit field is finished the moment it is full; making someone press
  // a button after that is a step with nothing left to decide.
  useEffect(() => {
    if (step !== 'code' || code.length !== CODE_LENGTH) return;
    if (submittedCode.current === code) return;
    submittedCode.current = code;
    void verifyCode(code);
    // verifyCode closes over state that is stable for the lifetime of a code.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [code, step]);

  const signInWithPassword = async () => {
    const trimmed = identifier.trim();
    const nextErrors: FieldErrors = {};
    if (!trimmed) {
      nextErrors.identifier = t('page.public.auth.errors.usernameRequired');
    }
    if (!password) {
      nextErrors.password = t('page.public.auth.errors.passwordRequired');
    }
    setErrors(nextErrors);
    if (nextErrors.identifier || nextErrors.password) return;

    setSigningIn(true);
    try {
      // The backend resolves this field with `username = ? OR email = ?`, so a
      // username and an address are equally acceptable here.
      const data = unwrap<Record<string, unknown>>(
        await API.post('/api/user/login', {
          password,
          username: trimmed,
        }),
      );
      rememberCredential('password');
      offerToSavePassword(trimmed, password);
      completeLogin(data);
      if (trimmed === 'root' && password === '123456') {
        showWarning(t('page.public.auth.defaultPasswordWarning'));
      }
    } catch (failure) {
      showError(failure, t('page.public.auth.errors.loginFailed'));
    } finally {
      setSigningIn(false);
    }
  };

  /* An invitation names the address, so the door is already answered: send
     the code there as soon as the page knows a code can be sent. Without
     code sign-in the door opens with the address filled in instead. */
  useEffect(() => {
    if (!invite || inviteStarted.current || !ready || checkingSession) return;
    inviteStarted.current = true;
    setIdentifier(invite.email);
    if (!emailCodeAuthAvailable) {
      setCredential('password');
      return;
    }
    void sendCode(invite.email).then((sent) => {
      if (sent) setStep('code');
    });
    // sendCode closes over state that is settled once the page is ready.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [invite, ready, checkingSession, emailCodeAuthAvailable]);

  const inviteNote = invite ? (
    <p className='auth-invite'>
      <Users aria-hidden='true' size={15} strokeWidth={1.8} />
      <span>
        {invite.inviter
          ? t('page.public.auth.inviteContext', {
              inviter: invite.inviter,
              project: invite.project,
            })
          : t('page.public.auth.inviteContextProject', {
              project: invite.project,
            })}
      </span>
    </p>
  ) : null;

  const switchCredential = (next: string) => {
    if (next === credential) return;
    setCredential(next as AuthCredential);
    setErrors({});
    setPassword('');
  };

  const backToDoor = () => {
    setStep('door');
    setCode('');
    submittedCode.current = '';
    // "Another address" means another one; the field does not keep the last.
    setIdentifier('');
  };

  const fallBackToPassword = () => {
    const carried = email || identifier;
    backToDoor();
    setCredential('password');
    // The address typed for the code is a valid password identifier too, so
    // it survives this trip back rather than making them type it twice. The
    // other way out clears it, because it is asking for a different address.
    setIdentifier(carried);
  };

  /* The address is the one word on this screen that has to be read exactly,
     so it is emphasised inside the sentence rather than parked on a line of
     its own. Splitting the translated string on a literal we already hold
     keeps the emphasis without pinning the word order of four languages. */
  const codeDescription = useMemo(() => {
    const text = t('page.public.auth.codeDescription', {
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

  const handleProviderLogin = (providerID: string) => {
    const params = new URLSearchParams({ next: requestedDestination });
    const path =
      providerID === 'oidc'
        ? '/api/auth/oidc/login'
        : `/api/auth/oauth/${encodeURIComponent(providerID)}/login`;
    window.location.href = buildAPIURL(`${path}?${params.toString()}`);
  };

  const renderDoor = () => (
    <AuthPanel
      // No visible heading and no subtitle. The wordmark above already names
      // the site, and the segmented control names both ways in — a 29px
      // "Sign in to Lazu" plus a line explaining that a code arrives by email
      // said nothing the controls underneath did not already say, and pushed
      // the first thing you can press below the fold on short laptops.
      heading='hidden'
      // One token for both credentials: switching them must not remount the
      // panel (see AuthReveal). The morph still plays between door and code.
      morphToken='door'
      title={t('page.public.auth.doorTitle')}
    >
      {inviteNote}
      {providers.length > 0 ? (
        <div className='auth-sso-stack'>
          {providers.map((provider) => (
            <SsoButton
              key={provider.id}
              onClick={() => handleProviderLogin(provider.id)}
              provider={provider.provider}
            />
          ))}
        </div>
      ) : null}

      {providers.length > 0 && activeCredential !== null ? (
        <AuthDivider>{t('page.public.auth.or')}</AuthDivider>
      ) : null}

      {bothCredentials ? (
        <AuthSegmented
          ariaLabel={t('page.public.auth.credentialMethod')}
          onChange={switchCredential}
          options={[
            { label: t('page.public.auth.credentialCode'), value: 'code' },
            {
              label: t('page.public.auth.credentialPassword'),
              value: 'password',
            },
          ]}
          value={credential}
        />
      ) : null}

      {activeCredential !== null ? (
        <form
          className='auth-form'
          onSubmit={async (event) => {
            event.preventDefault();
            if (isPasswordMode) {
              await signInWithPassword();
              return;
            }
            if (await sendCode(identifier)) setStep('code');
          }}
        >
          <AuthTextField
            autoComplete={isPasswordMode ? 'username' : 'email'}
            error={errors.identifier}
            icon={isPasswordMode ? User : Mail}
            id='auth-identifier'
            name={isPasswordMode ? 'username' : 'email'}
            label={t(
              isPasswordMode
                ? 'page.public.auth.usernameOrEmail'
                : 'page.public.auth.email',
            )}
            onChange={setIdentifier}
            placeholder={t(
              isPasswordMode
                ? 'page.public.auth.usernameOrEmailPlaceholder'
                : 'page.public.auth.emailPlaceholder',
            )}
            // A username is not a valid `type="email"` value, so the browser
            // would refuse to submit one before the form ever ran.
            type={isPasswordMode ? 'text' : 'email'}
            value={identifier}
          />

          <AuthReveal open={isPasswordMode}>
            <AuthTextField
              aside={
                <Link className='auth-field-aside' to='/reset'>
                  {t('page.public.auth.forgotPassword')}
                </Link>
              }
              autoComplete='current-password'
              error={errors.password}
              icon={Lock}
              id='auth-password'
              key={activeCredential}
              label={t('page.public.auth.password')}
              name='password'
              onChange={setPassword}
              placeholder={t('page.public.auth.passwordPlaceholder')}
              type='password'
              value={password}
            />
          </AuthReveal>

          <Button
            fullWidth
            loading={isPasswordMode ? signingIn : sending}
            type='submit'
            variant='primary'
          >
            {t(
              isPasswordMode
                ? 'page.public.auth.login'
                : 'page.public.auth.continue',
            )}
          </Button>
        </form>
      ) : null}

      {providers.length > 0 || activeCredential !== null ? (
        <TermsNotice />
      ) : null}
    </AuthPanel>
  );

  const renderCodeStep = () => (
    <AuthPanel
      description={codeDescription}
      heading='step'
      morphToken='code'
      title={t('page.public.auth.codeTitle')}
    >
      {inviteNote}
      <OtpInput
        disabled={verifying}
        length={CODE_LENGTH}
        onChange={setCode}
        value={code}
      />

      {/* The resend is the one control under the boxes, and the wait is drawn
          on it rather than said beside it. The other ways out of this screen
          — another address, a password — share one quiet line. */}
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
          onClick={() => void sendCode(email)}
          style={
            {
              '--auth-resend-progress': `${Math.round(
                ((RESEND_SECONDS - countdown) / RESEND_SECONDS) * 100,
              )}%`,
            } as CSSProperties
          }
          type='button'
        >
          <span>{t('page.public.auth.resendCode')}</span>
          {countdown > 0 ? (
            <span aria-hidden='true' className='auth-resend__count'>
              {countdown} s
            </span>
          ) : null}
        </button>

        {invite ? (
          <p className='auth-meta__row auth-meta__row--quiet'>
            {t('page.public.auth.inviteNewAccount')}
          </p>
        ) : null}
        <p className='auth-meta__row auth-meta__row--quiet'>
          <span>{t('page.public.auth.noEmailHint')}</span>
          <span aria-hidden='true'>·</span>
          {invite ? (
            /* The invitation fixes the address; another one is the
               invitation page's question, not this screen's. */
            <button
              onClick={() => navigate(requestedDestination, { replace: true })}
              type='button'
            >
              {t('page.public.auth.inviteBack')}
            </button>
          ) : (
            <button onClick={backToDoor} type='button'>
              {t('page.public.auth.useAnotherEmail')}
            </button>
          )}
          {passwordLoginAvailable ? (
            <>
              <span aria-hidden='true'>·</span>
              <button onClick={fallBackToPassword} type='button'>
                {t('page.public.auth.codeFallbackAction')}
              </button>
            </>
          ) : null}
        </p>
      </div>
    </AuthPanel>
  );

  /* The shell — the ground, the theme and language controls — is true before
     the answer arrives, so it paints straight away and only the 352px column
     waits. The outline is the same element in the same place as the door, so
     when the answer lands the column fills in rather than changing its mind. */
  return (
    <AuthShell>
      {ready && !checkingSession ? (
        step === 'door' ? (
          renderDoor()
        ) : (
          renderCodeStep()
        )
      ) : doorRevealed ? (
        <DoorSkeleton
          layout={
            ready
              ? {
                  credential: activeCredential,
                  providerCount: providers.length,
                  showSwitch: bothCredentials,
                }
              : undefined
          }
        />
      ) : null}
    </AuthShell>
  );
};

export default LoginForm;
