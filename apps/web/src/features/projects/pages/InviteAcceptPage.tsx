/* /invite/:token — the page an invitation link lands on. Covers signed-out,
 * a signed-in account on another address, an account with no verified
 * address, expired, revoked, and a repeat accept (which simply succeeds
 * again). Signing in from here comes back with `autoAccept`, so the person
 * who just proved the address is not asked a second time. */
import { useQuery } from '@tanstack/react-query';
import { useContext, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom';
import {
  Button,
  Input,
  LoadingState,
  StatusPage,
} from '../../../components/ui';
import { UserContext } from '../../../context/User';
import { API } from '../../../helpers/api';
import { useDelayedReveal } from '../../../hooks/common/useDelayedReveal';
import { isLazuApiError, unwrap } from '../../../lib/api-error';
import { announceProjectsChanged } from '../bus';
import { loadProjectsClient } from '../client';
import { Kvs } from '../components/atoms';
import { InviteExpiry } from '../components/InviteExpiry';
import { formatUSDShort } from '../money';
import { useProject } from '../ProjectContext';
import { projectKeys } from '../queryKeys';
import { describeError } from '../receipt';
import { useInviteClock } from '../useInviteClock';
import type { InvitePreview } from '../types';
import type { ReactNode } from 'react';
import '../projects.css';

type SelfEmail = { email?: string; email_verified?: boolean };
type Gate = 'signin' | 'accept' | 'mismatch' | 'verify';

const RESEND_SECONDS = 60;

/* The address is the one word on these screens that must be read exactly, so
 * it is set in mono inside the sentence. Splitting the translated string on
 * the literal keeps the emphasis without pinning four languages' word order. */
function withAddress(text: string, address: string): ReactNode {
  const at = address ? text.indexOf(address) : -1;
  if (at < 0) return text;
  return (
    <>
      {text.slice(0, at)}
      <span className='pj-mono'>{address}</span>
      {text.slice(at + address.length)}
    </>
  );
}

export default function InviteAcceptPage() {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const { token = '' } = useParams();
  const location = useLocation();
  const navigate = useNavigate();
  const project = useProject();
  const [opening, setOpening] = useState<'refreshing' | 'switching' | null>(
    null,
  );
  const [openError, setOpenError] = useState('');
  const [userState, userDispatch] = useContext(UserContext);
  const signedIn = Boolean(userState.user?.id || localStorage.getItem('user'));
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<
    | { kind: 'ok'; projectId: string }
    | { kind: 'error'; code: string; message: string }
    | null
  >(null);
  const autoAccept = Boolean(
    (location.state as { autoAccept?: boolean } | null)?.autoAccept,
  );
  const autoAccepted = useRef(false);

  const preview = useQuery({
    queryKey: projectKeys.invite(token),
    queryFn: async () => (await loadProjectsClient()).previewInvite(token),
    enabled: Boolean(token),
    retry: false,
  });

  /* The stored user can be stale and the sign-in reply carries no verified
     flag, so the address question is asked of the server. */
  const self = useQuery({
    queryKey: ['invite-accept', 'self'],
    queryFn: async () =>
      unwrap<SelfEmail>(
        await API.get('/api/user/self', { skipErrorHandler: true } as never),
      ),
    enabled: signedIn,
    retry: false,
    staleTime: 0,
  });

  const accept = async () => {
    setBusy(true);
    try {
      const client = await loadProjectsClient();
      const result = await client.acceptInvite(token);
      setOutcome({ kind: 'ok', projectId: result.project_id });
    } catch (error) {
      setOpening(null);
      setOutcome({
        kind: 'error',
        code: isLazuApiError(error) ? error.code : 'unknown',
        message: describeError(error),
      });
    } finally {
      setBusy(false);
    }
  };

  useEffect(() => {
    if (opening !== 'switching' || outcome?.kind !== 'ok' || !outcome.projectId)
      return;
    const result = project.switchProject(outcome.projectId);
    setOpening(null);
    if (result === 'switched') navigate('/console');
    else setOpenError(t('projects.acceptPage.openFailed'));
  }, [opening, outcome, project, navigate, t]);

  const openAccepted = async () => {
    setOpening('refreshing');
    setOpenError('');
    let projectId = outcome?.kind === 'ok' ? outcome.projectId : '';
    if (!projectId) {
      try {
        const result = await (await loadProjectsClient()).acceptInvite(token);
        projectId = result.project_id;
        setOutcome({ kind: 'ok', projectId });
      } catch (error) {
        setOpening(null);
        setOutcome({
          kind: 'error',
          code: isLazuApiError(error) ? error.code : 'unknown',
          message: describeError(error),
        });
        return;
      }
    }
    try {
      const response = await API.get('/api/user/self', {
        skipErrorHandler: true,
      } as never);
      if (!response.data?.success || !response.data.data)
        throw new Error(t('projects.acceptPage.openFailed'));
      userDispatch({ type: 'login', payload: response.data.data });
      announceProjectsChanged();
      setOpening('switching');
    } catch {
      setOpening(null);
      setOpenError(t('projects.acceptPage.openFailed'));
    }
  };

  const invite = preview.data;
  const now = useInviteClock(invite ? [invite.expires_at] : []);
  const expired = invite
    ? invite.status === 'expired' ||
      (invite.status !== 'accepted' &&
        invite.expires_at <= Math.floor(now / 1000))
    : false;
  const revoked = invite?.status === 'revoked';

  const inviteEmail = (invite?.email ?? '').trim().toLowerCase();
  const selfEmail = (self.data?.email ?? '').trim().toLowerCase();
  const errorCode = outcome?.kind === 'error' ? outcome.code : '';
  let gate: Gate = 'signin';
  /* A stored user whose session the server no longer recognises is a
     signed-out visitor: offering "Accept" would only fail. */
  if (signedIn && !self.isError) {
    if (self.data && typeof self.data.email_verified === 'boolean') {
      gate =
        self.data.email_verified && selfEmail
          ? selfEmail === inviteEmail
            ? 'accept'
            : 'mismatch'
          : 'verify';
    } else {
      gate =
        errorCode === 'invite_email_mismatch'
          ? 'mismatch'
          : errorCode === 'invite_email_unverified'
            ? 'verify'
            : 'accept';
    }
  }

  // Back from signing in with the invited address: accept straight away.
  useEffect(() => {
    if (
      !autoAccept ||
      autoAccepted.current ||
      !invite ||
      invite.status !== 'pending' ||
      expired ||
      gate !== 'accept' ||
      self.isPending
    )
      return;
    autoAccepted.current = true;
    void accept();
    // accept closes over the token, which is fixed for this page.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [autoAccept, invite, expired, gate, self.isPending]);

  const continueWithInvite = () =>
    navigate('/login', {
      state: {
        from: { pathname: location.pathname },
        invite: {
          token,
          email: invite?.email ?? '',
          project: invite?.project_name ?? '',
          inviter: invite?.inviter.username ?? '',
        },
      },
    });

  const switchAccount = async () => {
    setBusy(true);
    try {
      await API.get('/api/user/logout');
    } catch {
      // The session may already be gone; the local cleanup signs this browser out.
    }
    userDispatch({ type: 'logout' });
    localStorage.removeItem('user');
    setBusy(false);
    continueWithInvite();
  };

  if (preview.isLoading || (signedIn && self.isPending && !outcome)) {
    return <InviteWaiting label={t('projects.acceptPage.loading')} />;
  }

  if (
    preview.isError &&
    !(isLazuApiError(preview.error) && preview.error.status === 404)
  ) {
    return (
      <StatusPage
        action={
          <Button
            loading={preview.isFetching}
            onClick={() => void preview.refetch()}
            variant='primary'
          >
            {t('projects.common.retry')}
          </Button>
        }
        description={t('projects.acceptPage.loadFailedBody')}
        ground
        mark='Error'
        secondaryAction={
          <Button render={<Link to='/console' />} variant='ghost'>
            {t('projects.acceptPage.toConsole')}
          </Button>
        }
        title={t('projects.acceptPage.loadFailedTitle')}
      />
    );
  }
  if (!invite) {
    return (
      <StatusPage
        action={
          <Button render={<Link to='/console' />} variant='primary'>
            {t('projects.acceptPage.toConsole')}
          </Button>
        }
        description={t('projects.acceptPage.notFoundBody')}
        ground
        mark='404'
        title={t('projects.acceptPage.notFound')}
      />
    );
  }

  const acceptedUnverified =
    invite.status === 'accepted' && signedIn && outcome === null;
  if (outcome?.kind === 'ok' || acceptedUnverified) {
    return (
      <StatusPage
        action={
          <Button
            loading={busy || opening !== null}
            onClick={() => void openAccepted()}
            variant='primary'
          >
            {t('projects.acceptPage.openProject', {
              name: invite.project_name,
            })}
          </Button>
        }
        description={
          <>
            <span>
              {acceptedUnverified
                ? t('projects.acceptPage.acceptedUnverifiedBody')
                : t('projects.acceptPage.acceptedBody', {
                    name: invite.project_name,
                    budget: formatUSDShort(invite.budget_microusd),
                  })}
            </span>
            {openError ? <span role='alert'>{openError}</span> : null}
          </>
        }
        ground
        mark='Joined'
        secondaryAction={
          <Button render={<Link to='/console' />} variant='ghost'>
            {t('projects.acceptPage.later')}
          </Button>
        }
        title={
          acceptedUnverified
            ? t('projects.acceptPage.acceptedUnverified')
            : t('projects.acceptPage.accepted', { name: invite.project_name })
        }
      />
    );
  }

  /* Nothing on the invite card can be acted on once it has expired or been
   * withdrawn, so the reader gets the outcome as a page of its own. */
  if (revoked || expired) {
    return (
      <StatusPage
        action={
          <Button render={<Link to='/console' />} variant='primary'>
            {t('projects.acceptPage.toConsole')}
          </Button>
        }
        description={
          revoked
            ? t('projects.acceptPage.revoked', {
                inviter: invite.inviter.username,
              })
            : t('projects.acceptPage.expiredBody', {
                inviter: invite.inviter.username,
              })
        }
        ground
        mark={revoked ? 'Revoked' : 'Expired'}
        pill={invite.project_name}
        title={
          revoked
            ? t('projects.acceptPage.revokedTitle')
            : t('projects.acceptPage.expiredTitle')
        }
      />
    );
  }

  const inviter = invite.inviter.username;

  if (gate === 'mismatch') {
    return (
      <div className='pj-accept'>
        <div className='pj-accept__card'>
          <span className='pj-eyebrow'>{t('projects.acceptPage.eyebrow')}</span>
          <h1>{t('projects.acceptPage.mismatchTitle')}</h1>
          <p className='pj-muted'>
            {withAddress(
              t('projects.acceptPage.mismatchBody', {
                inviter,
                email: invite.email,
              }),
              invite.email,
            )}
          </p>
          <div className='pj-accept__compare'>
            <div className='pj-accept__addr pj-accept__addr--target'>
              <span>{t('projects.acceptPage.sentTo')}</span>
              <strong className='pj-mono'>{invite.email}</strong>
            </div>
            <div className='pj-accept__addr'>
              <span>{t('projects.acceptPage.signedInAs')}</span>
              <strong className='pj-mono'>{self.data?.email || '—'}</strong>
            </div>
          </div>
          <Button
            fullWidth
            loading={busy}
            onClick={() => void switchAccount()}
            variant='primary'
          >
            {withAddress(
              t('projects.acceptPage.switchAccount', { email: invite.email }),
              invite.email,
            )}
          </Button>
          <p className='pj-sub pj-accept__center'>
            {t('projects.acceptPage.askResend', {
              inviter,
              email: self.data?.email ?? '',
            })}
          </p>
          <div className='pj-accept__foot'>
            <span />
            <Link to='/console'>{t('projects.acceptPage.toConsole')}</Link>
          </div>
        </div>
      </div>
    );
  }

  if (gate === 'verify') {
    return (
      <VerifyInviteEmail
        accepting={busy}
        invite={invite}
        onVerified={async () => {
          await self.refetch();
          await accept();
        }}
        ownAddress={selfEmail === inviteEmail}
        serverError={
          outcome?.kind === 'error' && errorCode !== 'invite_email_unverified'
            ? outcome.message
            : ''
        }
      />
    );
  }

  const errorBody = outcome?.kind === 'error' ? outcome.message : '';

  return (
    <div className='pj-accept'>
      <div className='pj-accept__card'>
        <span className='pj-eyebrow'>{t('projects.acceptPage.eyebrow')}</span>
        <h1>
          {t('projects.acceptPage.title', {
            inviter,
            name: invite.project_name,
          })}
        </h1>
        <p className='pj-muted'>{t('projects.acceptPage.lede')}</p>
        <Kvs
          labelWidth={110}
          rows={[
            [
              t('projects.acceptPage.invitedEmail'),
              <span className='pj-mono'>{invite.email}</span>,
            ],
            [t('projects.acceptPage.role'), t(`projects.role.${invite.role}`)],
            [
              t('projects.acceptPage.budget'),
              invite.budget_microusd === null
                ? t('projects.budget.unlimited')
                : t('projects.acceptPage.budgetValue', {
                    amount: formatUSDShort(invite.budget_microusd),
                  }),
            ],
            [
              t('projects.acceptPage.validity'),
              <InviteExpiry
                expiresAt={invite.expires_at}
                now={now}
                expired={expired}
              />,
            ],
          ]}
        />
        {errorBody ? (
          <p className='pj-errt' role='alert'>
            {errorBody}
          </p>
        ) : null}
        {gate === 'signin' ? (
          <>
            <Button fullWidth onClick={continueWithInvite} variant='primary'>
              {withAddress(
                t('projects.acceptPage.continueWith', { email: invite.email }),
                invite.email,
              )}
            </Button>
            <p className='pj-sub pj-accept__center'>
              {t('projects.acceptPage.noAccountHint')}
            </p>
          </>
        ) : (
          <div className='pj-accept__acts'>
            <Button
              loading={busy}
              loadingLabel={t('projects.acceptPage.accepting')}
              onClick={() => void accept()}
              variant='primary'
            >
              {t('projects.acceptPage.accept')}
            </Button>
            <Button
              disabled={busy}
              render={<Link to='/console' />}
              variant='ghost'
            >
              {t('projects.acceptPage.later')}
            </Button>
          </div>
        )}
        <div className='pj-accept__foot'>
          <span>
            {gate === 'signin'
              ? t('projects.acceptPage.notYourEmail', { inviter })
              : t('projects.acceptPage.footnote')}
          </span>
          {gate === 'signin' ? (
            <Link to='/'>{t('projects.acceptPage.later')}</Link>
          ) : null}
        </div>
      </div>
    </div>
  );
}

/* A signed-in account without a verified address proves the invited one
 * here: the code binds it to this account, then the invitation is accepted. */
function VerifyInviteEmail({
  accepting,
  invite,
  onVerified,
  ownAddress,
  serverError,
}: {
  accepting: boolean;
  invite: InvitePreview;
  onVerified: () => Promise<void>;
  ownAddress: boolean;
  serverError: string;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const [code, setCode] = useState('');
  const [sentAt, setSentAt] = useState<number | null>(null);
  const [sending, setSending] = useState(false);
  const [verifying, setVerifying] = useState(false);
  const [error, setError] = useState('');
  const [clock, setClock] = useState(() => Date.now());
  useEffect(() => {
    if (sentAt === null) return undefined;
    const timer = setInterval(() => setClock(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [sentAt]);
  const wait =
    sentAt === null
      ? 0
      : Math.max(0, RESEND_SECONDS - Math.floor((clock - sentAt) / 1000));

  const send = async () => {
    setSending(true);
    setError('');
    try {
      unwrap(
        await API.get('/api/verification', {
          params: { email: invite.email },
          skipErrorHandler: true,
        } as never),
      );
      setSentAt(Date.now());
      setClock(Date.now());
    } catch (failure) {
      setError(describeError(failure));
    } finally {
      setSending(false);
    }
  };

  const verify = async () => {
    setVerifying(true);
    setError('');
    try {
      unwrap(
        await API.get('/api/user/email/bind', {
          params: { email: invite.email, code: code.trim() },
          skipErrorHandler: true,
        } as never),
      );
      await onVerified();
    } catch (failure) {
      setError(describeError(failure));
      setCode('');
    } finally {
      setVerifying(false);
    }
  };

  const shown = error || serverError;
  return (
    <div className='pj-accept'>
      <div className='pj-accept__card'>
        <span className='pj-eyebrow'>{t('projects.acceptPage.eyebrow')}</span>
        <h1>
          {withAddress(
            ownAddress
              ? t('projects.acceptPage.verifyOwnTitle', {
                  name: invite.project_name,
                })
              : t('projects.acceptPage.verifyTitle', {
                  email: invite.email,
                  name: invite.project_name,
                }),
            invite.email,
          )}
        </h1>
        <p className='pj-muted'>
          {ownAddress
            ? withAddress(
                t('projects.acceptPage.verifyOwnBody', { email: invite.email }),
                invite.email,
              )
            : t('projects.acceptPage.verifyBody')}
        </p>
        {sentAt === null ? (
          <Button
            fullWidth
            loading={sending}
            onClick={() => void send()}
            variant='primary'
          >
            {withAddress(
              t('projects.acceptPage.sendCodeTo', { email: invite.email }),
              invite.email,
            )}
          </Button>
        ) : (
          <form
            className='pj-accept__code'
            onSubmit={(event) => {
              event.preventDefault();
              if (code.trim().length > 0) void verify();
            }}
          >
            <label className='pj-sub' htmlFor='pj-accept-code'>
              {withAddress(
                t('projects.acceptPage.codeSentTo', { email: invite.email }),
                invite.email,
              )}
            </label>
            <Input
              autoComplete='one-time-code'
              id='pj-accept-code'
              maxLength={8}
              onChange={(e) => setCode(e.target.value.replace(/\s/g, ''))}
              value={code}
            />
            <Button
              disabled={code.trim().length === 0}
              fullWidth
              loading={verifying || accepting}
              type='submit'
              variant='primary'
            >
              {t('projects.acceptPage.verifyAndJoin')}
            </Button>
          </form>
        )}
        {shown ? (
          <p className='pj-errt' role='alert'>
            {shown}
          </p>
        ) : null}
        <div className='pj-accept__foot'>
          {sentAt !== null ? (
            <button
              className='pj-accept__link'
              disabled={wait > 0 || sending}
              onClick={() => void send()}
              type='button'
            >
              {wait > 0
                ? t('projects.acceptPage.resendIn', { seconds: wait })
                : t('projects.acceptPage.resend')}
            </button>
          ) : (
            <span />
          )}
          <Link to='/console'>{t('projects.acceptPage.later')}</Link>
        </div>
      </div>
    </div>
  );
}

/* Loading never gets a status page: the site-wide small ring, held back for
 * the first 300ms so a fast answer paints no wait at all. */
function InviteWaiting({ label }: { label: string }) {
  const revealed = useDelayedReveal();
  return (
    <div className='lazu-status-page lazu-status-page--ground'>
      {revealed ? <LoadingState title={label} /> : null}
    </div>
  );
}
