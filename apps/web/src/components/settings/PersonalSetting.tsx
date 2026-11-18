import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import React, {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react';
import { useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate } from 'react-router-dom';
import { z } from 'zod';
import {
  DEFAULT_QUOTA_WARNING_THRESHOLD_USD,
  defaultNotificationSettings,
  mailPrefsFromUser,
  settingsFromUser,
  toNotificationPayload,
  zheOf,
} from './personalHelpers';
import TelegramLinkField, { useTelegramLinkStatus } from './TelegramLinkField';
import { useSession } from '../../context/User';
import { API } from '../../helpers/api';
import { setUserData } from '../../helpers/data';
import { useDelayedReveal } from '../../hooks/common/useDelayedReveal';
import { useSelfQuery } from '../../hooks/common/useSelfQuery';
import {
  apiErrorHandler,
  errorMessage,
  isRefusal,
  unwrap,
} from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';
import useAuthStatus from '../auth/useAuthStatus';
import { Button } from '../ui/button';
import ConsolePage from '../ui/console-page';
import { Field, Label } from '../ui/field';
import { InlineNotice } from '../ui/inline-notice';
import { Input } from '../ui/input';
import { Modal } from '../ui/modal';
import { PendingBlock, PendingValue } from '../ui/pending';
import { GateModal } from '../ui/project-primitives';
import { Select, SelectItem } from '../ui/select';
import { SettingsCard, SettingsRow } from '../ui/settings-card';
import { Switch } from '../ui/switch';
import './personal.css';
import '../../styles/settings-surface.css';
import { notify } from '../ui/toast';
import type { ChangeEvent } from 'react';

const ns = 'page.console.personal';
const IDENTITY_QUERY_KEY = [...queryKeys.user, 'self', 'identities'] as const;
const DEFAULT_USERNAME_COOLDOWN_DAYS = 30;
const USERNAME_CHECK_QUERY_KEY = [
  ...queryKeys.user,
  'self',
  'username-check',
] as const;

const notificationSchema = z.object({
  warningType: z.string(),
  warningThreshold: z.coerce.number().min(0),
  webhookUrl: z.string().optional(),
  webhookSecret: z.string().optional(),
  notificationEmail: z.string().optional(),
  barkUrl: z.string().optional(),
  gotifyUrl: z.string().optional(),
  gotifyToken: z.string().optional(),
  gotifyPriority: z.coerce.number().min(0).max(10),
});

const emailSchema = z.object({
  email: z.string().email(),
  code: z.string().min(1),
  currentCode: z.string().optional(),
});

const passwordByteCount = (value: string) =>
  new TextEncoder().encode(value).byteLength;

const passwordSchema = z
  .object({
    originalPassword: z.string(),
    newPassword: z
      .string()
      .min(8, 'page.public.auth.errors.passwordMinLength')
      .max(64, 'page.public.auth.errors.passwordMaxLength')
      .refine((value) => passwordByteCount(value) <= 72, {
        message: 'page.public.auth.errors.passwordByteLimit',
      }),
    confirmPassword: z.string().min(1),
  })
  .refine((values) => values.newPassword === values.confirmPassword, {
    message: `${ns}.validation.passwordMatch`,
    path: ['confirmPassword'],
  });

type NotificationFormInput = z.input<typeof notificationSchema>;
type NotificationFormData = z.output<typeof notificationSchema>;
type PricePlanRule = {
  kind: 'vendor' | 'model';
  model_name?: string;
  pct: number;
  vendor_name?: string;
};

type PricePlanView = {
  default_pct?: number | null;
  ends_at?: string | null;
  lanes: 'all' | 'cheap' | 'direct';
  live: boolean;
  name: string;
  rules: PricePlanRule[];
};

type UserRecord = Record<string, unknown> & {
  email?: string;
  price_plan?: PricePlanView | null;
  id?: number | string;
  role?: number | string;
  setting?: string;
  username?: string;
};

const resolveTimeZone = () => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || '';
  } catch {
    return '';
  }
};

const listTimeZones = (fallback: string): string[] => {
  const intl = Intl as unknown as {
    supportedValuesOf?: (key: string) => string[];
  };
  try {
    const zones = intl.supportedValuesOf?.('timeZone') ?? [];
    if (zones.length > 0) {
      return fallback && !zones.includes(fallback)
        ? [fallback, ...zones]
        : zones;
    }
  } catch {
    // Older engines: offer the current zone only.
  }
  return fallback ? [fallback] : [];
};

type DigestKind = 'weekly' | 'monthly';

const SetCard = SettingsCard;

const PersonalEditorContext = createContext({ close: () => {}, busy: false });

const SetRow = ({
  actions,
  editor,
  editorMode = 'inline',
  hint,
  id,
  label,
  modalTitle,
  open,
  value,
}: {
  actions?: React.ReactNode;
  editor?: React.ReactNode;
  editorMode?: 'inline' | 'modal';
  hint?: React.ReactNode;
  id?: string;
  label: React.ReactNode;
  modalTitle?: React.ReactNode;
  open?: boolean;
  value?: React.ReactNode;
}) => {
  const { close, busy } = useContext(PersonalEditorContext);
  return (
    <SettingsRow
      actions={actions}
      hint={hint}
      id={id}
      label={label}
      open={editorMode === 'inline' ? open : false}
      value={value}
    >
      {editor ? (
        editorMode === 'modal' ? (
          <Modal
            className='personal-setting-editor'
            open={Boolean(open)}
            onOpenChange={(next) => {
              if (!next) close();
            }}
            title={modalTitle ?? label}
            busy={busy}
            body={editor}
          />
        ) : open ? (
          editor
        ) : null
      ) : null}
    </SettingsRow>
  );
};

const SetEditor = ({
  children,
  tone,
}: {
  children: React.ReactNode;
  tone?: 'danger' | 'warn';
}) => (
  <div className={tone ? `set-ed set-ed--${tone}` : 'set-ed'}>{children}</div>
);

const RowAction = ({
  children,
  disabled,
  loading,
  onClick,
  tone,
}: {
  children: React.ReactNode;
  disabled?: boolean;
  loading?: boolean;
  onClick: () => void;
  tone?: 'danger';
}) => (
  <Button
    disabled={disabled}
    loading={loading}
    onClick={onClick}
    size='sm'
    type='button'
    variant={tone === 'danger' ? 'danger' : 'secondary'}
  >
    {children}
  </Button>
);

type ExpandedRow =
  | 'timezone'
  | 'delete'
  | 'email'
  | 'notification'
  | 'password'
  | 'username'
  | null;

const USERNAME_PATTERN = /^[a-zA-Z0-9][a-zA-Z0-9_-]{1,19}$/;

type UsernameVerdict = {
  available: boolean;
  code?: string;
  cooldown_days: number;
  next_change_at?: number;
  normalized: string;
  status:
    | 'available'
    | 'cooldown'
    | 'invalid_format'
    | 'reserved'
    | 'taken'
    | 'too_long'
    | 'too_short'
    | 'unchanged';
};

const catalogCopy = (code: string, details: Record<string, unknown> = {}) =>
  errorMessage({ error: { code, details } });

const useDebounced = <T,>(value: T, delay: number) => {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setSettled(value), delay);
    return () => clearTimeout(timer);
  }, [value, delay]);
  return settled;
};

function DigestPreviewPending() {
  const revealed = useDelayedReveal();
  return (
    <PendingBlock
      height={720}
      className={!revealed ? 'invisible' : undefined}
    />
  );
}

const PersonalSetting = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const userSession = useSession();
  const [expanded, setExpanded] = useState<ExpandedRow>(null);
  const [usernameDraft, setUsernameDraft] = useState('');
  const { status: authStatus } = useAuthStatus();
  const timeZone = useMemo(resolveTimeZone, []);

  const notificationForm = useForm<
    NotificationFormInput,
    unknown,
    NotificationFormData
  >({
    resolver: zodResolver(notificationSchema),
    defaultValues: defaultNotificationSettings,
  });
  const emailForm = useForm({
    resolver: zodResolver(emailSchema),
    defaultValues: { email: '', code: '', currentCode: '' },
  });
  const passwordForm = useForm({
    resolver: zodResolver(passwordSchema),
    defaultValues: {
      originalPassword: '',
      newPassword: '',
      confirmPassword: '',
    },
  });

  const userQuery = useSelfQuery(Boolean(userSession.user?.id));
  useEffect(() => {
    const data = userQuery.data;
    if (data) {
      userSession.signIn(data);
      setUserData(data);
    }
  }, [userQuery.data, userSession.signIn]);

  const revealPending = useDelayedReveal();
  const notificationReady = userQuery.isSuccess && Boolean(userQuery.data);
  const notificationSettings = JSON.stringify(settingsFromUser(userQuery.data));
  useEffect(() => {
    if (notificationReady && !notificationForm.formState.isDirty) {
      notificationForm.reset(
        JSON.parse(notificationSettings) as NotificationFormData,
      );
    }
  }, [notificationForm, notificationSettings, notificationReady]);

  const user = (userSession.user || userQuery.data || {}) as UserRecord;
  const pricePlan = (userQuery.data as UserRecord | undefined)?.price_plan;
  const emailCodeAuthAvailable = Boolean(authStatus?.email_code_auth);

  const identitiesQuery = useQuery({
    queryKey: IDENTITY_QUERY_KEY,
    queryFn: async () => {
      const data = unwrap<{
        has_password?: unknown;
        identities?: unknown;
      } | null>(await API.get('/api/user/self/identities'));
      if (
        typeof data?.has_password !== 'boolean' ||
        !Array.isArray(data?.identities)
      ) {
        throw new Error(t(`${ns}.error.loadUser`));
      }
      return data as {
        has_password: boolean;
        identities: {
          email: string;
          label: string;
          linked_at: number;
          provider: string;
          removable: boolean;
        }[];
      };
    },
  });

  const saveNotificationMutation = useMutation<
    void,
    Error,
    NotificationFormData
  >({
    mutationFn: async (values) => {
      if (!notificationReady) throw new Error(t(`${ns}.error.loadUser`));
      unwrap(await API.put('/api/user/setting', toNotificationPayload(values)));
    },
    onSuccess: (_result, values) => {
      notificationForm.reset(values);
      setExpanded(null);
      notify.success(t(`${ns}.notification.saved`));
      queryClient.invalidateQueries({ queryKey: queryKeys.user });
    },
    onError: apiErrorHandler,
  });

  const saveMailPrefsMutation = useMutation({
    mutationFn: async (values: {
      weekly_digest?: boolean;
      product_updates?: boolean;
    }) => {
      unwrap(await API.put('/api/user/self/mail-prefs', values));
    },
    onSuccess: () => {
      notify.success(t(`${ns}.notification.saved`));
      queryClient.invalidateQueries({ queryKey: queryKeys.user });
    },
    onError: apiErrorHandler,
  });

  const [timezoneDraft, setTimezoneDraft] = useState('');
  const saveTimezoneMutation = useMutation({
    mutationFn: async (timezone: string) => {
      unwrap(await API.put('/api/user/self/timezone', { timezone }));
    },
    onSuccess: () => {
      notify.success(t(`${ns}.notification.saved`));
      setExpanded(null);
      queryClient.invalidateQueries({ queryKey: queryKeys.user });
    },
    onError: apiErrorHandler,
  });

  const [digestPreview, setDigestPreview] = useState<DigestKind | null>(null);
  const digestPreviewQuery = useQuery({
    queryKey: [...queryKeys.user, 'self', 'digest-preview', digestPreview],
    enabled: digestPreview !== null,
    queryFn: async () =>
      unwrap<{ subject: string; html: string }>(
        await API.get('/api/user/self/digest-preview', {
          params: { kind: digestPreview },
        }),
      ),
  });

  const bindEmailMutation = useMutation<
    string,
    Error,
    z.infer<typeof emailSchema>
  >({
    mutationFn: async ({ email, code, currentCode }) => {
      unwrap(
        await API.post('/api/user/email/bind', {
          email,
          code,
          current_code: currentCode,
        }),
      );
      return email;
    },
    onSuccess: (email) => {
      notify.success(t(`${ns}.email.success`));
      setExpanded(null);
      emailForm.reset();
      userSession.signIn({ ...user, email });
      queryClient.invalidateQueries({ queryKey: queryKeys.user });
    },
    onError: apiErrorHandler,
  });

  const sendCodeMutation = useMutation<void, Error, string>({
    mutationFn: async (email) => {
      unwrap(await API.post('/api/user/email/bind/start', { email }));
    },
    onSuccess: () => notify.success(t(`${ns}.email.codeSent`)),
    onError: apiErrorHandler,
  });

  const trimmedDraft = usernameDraft.trim();
  const settledUsername = useDebounced(trimmedDraft, 350);
  const draftIsSameAsCurrent = trimmedDraft === (user.username || '');
  const draftShapeValid = USERNAME_PATTERN.test(trimmedDraft);
  const usernameCheckEnabled =
    expanded === 'username' &&
    trimmedDraft.length > 0 &&
    !draftIsSameAsCurrent &&
    draftShapeValid &&
    settledUsername === trimmedDraft;

  const usernameCheckQuery = useQuery({
    queryKey: [...USERNAME_CHECK_QUERY_KEY, settledUsername],
    enabled: usernameCheckEnabled,
    staleTime: 0,
    gcTime: 0,
    retry: false,
    queryFn: async () => {
      return unwrap<UsernameVerdict>(
        await API.get('/api/user/self/username/check', {
          params: { username: settledUsername },
          skipErrorHandler: true,
        }),
      );
    },
  });

  const changeUsernameMutation = useMutation<void, Error, string>({
    mutationFn: async (username) => {
      unwrap(
        await API.put(
          '/api/user/self',
          { username },
          { skipErrorHandler: true },
        ),
      );
    },
    onSuccess: async (_result, username) => {
      notify.success(t(`${ns}.username.success`));
      setExpanded(null);
      setUsernameDraft('');
      userSession.signIn({ ...user, username });
      setUserData({ ...user, username });
      await queryClient.invalidateQueries({ queryKey: queryKeys.user });
    },
    onError: apiErrorHandler,
  });

  const changePasswordMutation = useMutation<
    void,
    Error,
    z.infer<typeof passwordSchema>
  >({
    mutationFn: async ({ originalPassword, newPassword }) => {
      unwrap(
        await API.put('/api/user/self', {
          original_password: originalPassword,
          password: newPassword,
        }),
      );
    },
    onSuccess: async () => {
      notify.success(t(`${ns}.password.success`));
      setExpanded(null);
      passwordForm.reset();
      await queryClient.invalidateQueries({ queryKey: IDENTITY_QUERY_KEY });
    },
    onError: apiErrorHandler,
  });

  const deleteAccountMutation = useMutation({
    mutationFn: async () => {
      unwrap(await API.delete('/api/user/self', { skipErrorHandler: true }));
    },
    onSuccess: async () => {
      notify.success(t(`${ns}.delete.success`));
      await API.get('/api/user/logout');
      userSession.signOut();
      localStorage.removeItem('user');
      navigate('/login');
    },
  });

  const deleteError = deleteAccountMutation.error
    ? isRefusal(deleteAccountMutation.error)
      ? t(`${ns}.delete.failed`, {
          reason: errorMessage(deleteAccountMutation.error),
        })
      : errorMessage(deleteAccountMutation.error)
    : undefined;

  const notificationType = notificationForm.watch('warningType');
  const telegramStatus = useTelegramLinkStatus();
  const toggleExpanded = (row: Exclude<ExpandedRow, null>) =>
    setExpanded((current) => (current === row ? null : row));

  const openUsernameEditor = () => {
    if (expanded === 'username') {
      setExpanded(null);
      return;
    }
    setUsernameDraft(user.username || '');
    setExpanded('username');
  };

  const openEmailBinding = () => setExpanded('email');

  const usernameState = (() => {
    if (!trimmedDraft) {
      return { tone: 'idle' as const, text: '' };
    }
    if (draftIsSameAsCurrent) {
      return {
        tone: 'idle' as const,
        text: t(`${ns}.username.unchanged`),
      };
    }
    if (!draftShapeValid) {
      return {
        tone: 'bad' as const,
        text: catalogCopy('username_invalid'),
      };
    }
    if (usernameCheckQuery.isFetching || settledUsername !== trimmedDraft) {
      return {
        tone: 'busy' as const,
        text: t(`${ns}.username.checking`),
      };
    }
    if (usernameCheckQuery.isError) {
      return {
        tone: 'bad' as const,
        text: errorMessage(usernameCheckQuery.error),
      };
    }
    const verdict = usernameCheckQuery.data;
    if (!verdict) return { tone: 'idle' as const, text: '' };
    const text = verdict.code
      ? catalogCopy(verdict.code, { cooldown_days: verdict.cooldown_days })
      : t(
          verdict.status === 'unchanged'
            ? `${ns}.username.unchanged`
            : `${ns}.username.available`,
        );
    if (verdict.status === 'cooldown' && verdict.next_change_at) {
      return {
        tone: 'bad' as const,
        text: `${text} ${t(`${ns}.username.cooldownUntil`, {
          date: new Date(verdict.next_change_at * 1000).toLocaleDateString(),
        })}`,
      };
    }
    return {
      tone: verdict.available ? ('ok' as const) : ('bad' as const),
      text,
    };
  })();

  const usernameSavable =
    usernameState.tone === 'ok' && !changeUsernameMutation.isPending;

  const savedNotifications = settingsFromUser(user);
  const mailPrefs = mailPrefsFromUser(user);
  const storedTimeZone = mailPrefs.timezone;
  const digestTimeZone = storedTimeZone || timeZone;
  const mailToggle = (
    label: string,
    checked: boolean,
    field: 'weekly_digest' | 'product_updates',
  ) => (
    <Switch
      aria-label={label}
      checked={checked}
      disabled={saveMailPrefsMutation.isPending}
      onCheckedChange={(next: boolean) =>
        saveMailPrefsMutation.mutate({ [field]: next })
      }
    />
  );

  const notificationSummary = t(`${ns}.session.alertSummary`, {
    defaultValue: '余额低于 ${{threshold}} 时通过{{channel}}提醒',
    channel:
      savedNotifications.warningType === 'email'
        ? t(`${ns}.notification.email`)
        : savedNotifications.warningType === 'webhook'
          ? t(`${ns}.notification.webhook`)
          : savedNotifications.warningType === 'bark'
            ? 'Bark'
            : savedNotifications.warningType === 'telegram'
              ? 'Telegram'
              : 'Gotify',
    threshold: Number(
      savedNotifications.warningThreshold ??
        DEFAULT_QUOTA_WARNING_THRESHOLD_USD,
    ).toFixed(2),
  });

  const emailUnbound = !user.email;
  const alertNeedsEmail =
    savedNotifications.warningType === 'email' &&
    emailUnbound &&
    !savedNotifications.notificationEmail;

  const linkedIdentities = identitiesQuery.data?.identities ?? [];

  return (
    <ConsolePage
      appearance='review'
      title={t(`${ns}.title`)}
      className='personal-page personal-page--minimal'
    >
      <PersonalEditorContext.Provider
        value={{
          close: () => setExpanded(null),
          busy:
            changeUsernameMutation.isPending ||
            changePasswordMutation.isPending ||
            bindEmailMutation.isPending ||
            deleteAccountMutation.isPending ||
            saveNotificationMutation.isPending ||
            saveTimezoneMutation.isPending,
        }}
      >
        <div className='pm-container'>
          {emailUnbound ? (
            <InlineNotice
              className='pm-notice'
              tone='warning'
              title={t(`${ns}.emailNotice.title`)}
              description={t(
                emailCodeAuthAvailable
                  ? `${ns}.emailNotice.body`
                  : `${ns}.emailNotice.bodyNoCode`,
              )}
              actions={
                <Button
                  onClick={openEmailBinding}
                  size='sm'
                  type='button'
                  variant='primary'
                >
                  {t(`${ns}.emailNotice.action`)}
                </Button>
              }
            />
          ) : null}

          <SetCard title={t(`${ns}.account.title`)}>
            <SetRow
              label={t(`${ns}.account.username`)}
              value={<span className='pm-mono'>{user.username || '—'}</span>}
              open={expanded === 'username'}
              actions={
                expanded === 'username' ? null : (
                  <RowAction onClick={openUsernameEditor}>
                    {t(`${ns}.username.change`)}
                  </RowAction>
                )
              }
              editor={
                <SetEditor>
                  <form
                    className='pm-inline-form'
                    onSubmit={(event) => {
                      event.preventDefault();
                      if (!usernameSavable) return;
                      changeUsernameMutation.mutate(trimmedDraft);
                    }}
                  >
                    <div className='pm-inline-form__row'>
                      <Input
                        aria-label={t(`${ns}.username.field`)}
                        autoCapitalize='none'
                        autoComplete='off'
                        autoCorrect='off'
                        className='set-ed__field pm-mono'
                        id='account-username'
                        maxLength={20}
                        onChange={(event: ChangeEvent<HTMLInputElement>) =>
                          setUsernameDraft(event.target.value)
                        }
                        placeholder={t(`${ns}.username.placeholder`)}
                        spellCheck={false}
                        value={usernameDraft}
                      />
                      <div className='set-ed__actions'>
                        <Button
                          onClick={() => setExpanded(null)}
                          size='sm'
                          type='button'
                          variant='secondary'
                        >
                          {t(`${ns}.action.cancel`)}
                        </Button>
                        <Button
                          disabled={!usernameSavable}
                          loading={changeUsernameMutation.isPending}
                          size='sm'
                          type='submit'
                          variant='primary'
                        >
                          {t(`${ns}.notification.save`)}
                        </Button>
                      </div>
                    </div>
                    {usernameState.text ? (
                      <div
                        aria-live='polite'
                        className={`set-ed__state set-ed__state--${usernameState.tone}`}
                      >
                        {usernameState.text}
                      </div>
                    ) : (
                      <p className='set-ed__note'>
                        {t(`${ns}.username.shortHint`, {
                          days:
                            usernameCheckQuery.data?.cooldown_days ??
                            DEFAULT_USERNAME_COOLDOWN_DAYS,
                          defaultValue: '{{days}} 天内可修改一次',
                        })}
                      </p>
                    )}
                  </form>
                </SetEditor>
              }
            />

            <SetRow
              id='personal-email-row'
              label={t(`${ns}.account.email`)}
              value={
                user.email ? (
                  <span className='pm-mono'>{user.email}</span>
                ) : (
                  <span className='pm-muted'>
                    {t(`${ns}.fallback.noEmail`)}
                  </span>
                )
              }
              editorMode='modal'
              modalTitle={
                user.email ? t(`${ns}.email.change`) : t(`${ns}.email.bind`)
              }
              open={expanded === 'email'}
              actions={
                <RowAction onClick={() => toggleExpanded('email')}>
                  {user.email ? t(`${ns}.email.change`) : t(`${ns}.email.bind`)}
                </RowAction>
              }
              editor={
                <SetEditor>
                  <form
                    className='grid gap-3'
                    onSubmit={emailForm.handleSubmit((values) =>
                      bindEmailMutation.mutate(values),
                    )}
                  >
                    <div className='set-ed__row'>
                      <div>
                        <label className='set-ed__lb' htmlFor='bind-email'>
                          {t(`${ns}.email.address`)}
                        </label>
                        <Input
                          className='set-ed__field'
                          id='bind-email'
                          {...emailForm.register('email')}
                        />
                      </div>
                      <Button
                        loading={sendCodeMutation.isPending}
                        onClick={() =>
                          sendCodeMutation.mutate(emailForm.getValues('email'))
                        }
                        size='sm'
                        style={{ alignSelf: 'flex-end' }}
                        type='button'
                      >
                        {t(`${ns}.email.sendCode`)}
                      </Button>
                    </div>
                    <div>
                      <label className='set-ed__lb' htmlFor='bind-email-code'>
                        {t(`${ns}.email.code`)}
                      </label>
                      <Input
                        className='set-ed__field'
                        id='bind-email-code'
                        inputMode='numeric'
                        {...emailForm.register('code')}
                      />
                      <div className='set-ed__state set-ed__state--idle'>
                        {emailForm.formState.errors.email
                          ? t(`${ns}.validation.email`)
                          : emailForm.formState.errors.code
                            ? t(`${ns}.validation.code`)
                            : t(`${ns}.hint.emailCode`, {
                                defaultValue: '验证码 10 分钟内有效。',
                              })}
                      </div>
                    </div>
                    {user.email ? (
                      <div>
                        <label
                          className='set-ed__lb'
                          htmlFor='bind-current-email-code'
                        >
                          {t(`${ns}.email.currentCode`)}
                        </label>
                        <Input
                          className='set-ed__field'
                          id='bind-current-email-code'
                          inputMode='numeric'
                          autoComplete='one-time-code'
                          {...emailForm.register('currentCode')}
                        />
                        <p className='set-ed__note'>
                          {t(`${ns}.email.changeHint`)}
                        </p>
                      </div>
                    ) : null}
                    <div className='set-ed__actions'>
                      <Button
                        loading={bindEmailMutation.isPending}
                        size='sm'
                        type='submit'
                        variant='primary'
                      >
                        {t(`${ns}.email.bind`)}
                      </Button>
                      <Button
                        onClick={() => setExpanded(null)}
                        size='sm'
                        type='button'
                      >
                        {t(`${ns}.action.cancel`)}
                      </Button>
                    </div>
                  </form>
                </SetEditor>
              }
            />

            <SetRow
              label={t(`${ns}.identity.password`)}
              value={
                identitiesQuery.isPending ? (
                  <PendingValue
                    chars={3}
                    className={!revealPending ? 'invisible' : undefined}
                  />
                ) : identitiesQuery.isError ? (
                  <span className='pm-muted'>{t(`${ns}.error.loadUser`)}</span>
                ) : identitiesQuery.data.has_password ? (
                  <span className='pm-mono'>
                    {t(`${ns}.identity.passwordSet`)}
                  </span>
                ) : (
                  <span className='pm-muted'>
                    {t(`${ns}.identity.passwordUnset`)}
                  </span>
                )
              }
              editorMode='modal'
              modalTitle={
                identitiesQuery.data?.has_password === false
                  ? t(`${ns}.identity.setPassword`)
                  : t(`${ns}.identity.changePassword`)
              }
              open={expanded === 'password'}
              actions={
                identitiesQuery.isError ? (
                  <RowAction onClick={() => void identitiesQuery.refetch()}>
                    {t('legacy.i18n.retry.e2d53a6d3a')}
                  </RowAction>
                ) : (
                  <RowAction
                    disabled={identitiesQuery.data === undefined}
                    onClick={() => toggleExpanded('password')}
                  >
                    {identitiesQuery.data?.has_password === false
                      ? t(`${ns}.identity.setPassword`)
                      : t(`${ns}.username.change`)}
                  </RowAction>
                )
              }
              editor={
                <SetEditor>
                  <form
                    className='grid gap-3'
                    onSubmit={passwordForm.handleSubmit((values) =>
                      changePasswordMutation.mutate(values),
                    )}
                  >
                    <input
                      autoComplete='username'
                      name='username'
                      readOnly
                      type='hidden'
                      value={user.username ?? ''}
                    />
                    {identitiesQuery.data?.has_password === false ? null : (
                      <div>
                        <label className='set-ed__lb' htmlFor='old-password'>
                          {t(`${ns}.password.old`)}
                        </label>
                        <Input
                          autoComplete='current-password'
                          className='set-ed__field'
                          id='old-password'
                          type='password'
                          {...passwordForm.register('originalPassword')}
                        />
                      </div>
                    )}
                    <div className='set-ed__row'>
                      <div>
                        <label className='set-ed__lb' htmlFor='new-password'>
                          {t(`${ns}.password.new`)}
                        </label>
                        <Input
                          autoComplete='new-password'
                          className='set-ed__field'
                          id='new-password'
                          type='password'
                          {...passwordForm.register('newPassword')}
                        />
                      </div>
                      <div>
                        <label
                          className='set-ed__lb'
                          htmlFor='confirm-password'
                        >
                          {t(`${ns}.password.confirm`)}
                        </label>
                        <Input
                          autoComplete='new-password'
                          className='set-ed__field'
                          id='confirm-password'
                          type='password'
                          {...passwordForm.register('confirmPassword')}
                        />
                      </div>
                    </div>
                    <div
                      aria-live='polite'
                      className={`set-ed__state set-ed__state--${
                        passwordForm.formState.errors.newPassword ||
                        passwordForm.formState.errors.confirmPassword
                          ? 'bad'
                          : 'idle'
                      }`}
                    >
                      {passwordForm.formState.errors.newPassword?.message
                        ? t(passwordForm.formState.errors.newPassword.message)
                        : passwordForm.formState.errors.confirmPassword?.message
                          ? t(
                              passwordForm.formState.errors.confirmPassword
                                .message,
                            )
                          : t(`${ns}.hint.passwordScope`, {
                              defaultValue:
                                '修改后其他所有设备会被登出，这台设备保持登录。',
                            })}
                    </div>
                    <div className='set-ed__actions'>
                      <Button
                        loading={changePasswordMutation.isPending}
                        size='sm'
                        type='submit'
                        variant='primary'
                      >
                        {t(`${ns}.password.submit`)}
                      </Button>
                      <Button
                        onClick={() => setExpanded(null)}
                        size='sm'
                        type='button'
                      >
                        {t(`${ns}.action.cancel`)}
                      </Button>
                    </div>
                  </form>
                </SetEditor>
              }
            />

            <SetRow
              label={t(`${ns}.identity.linkedTitle`, {
                defaultValue: '关联登录',
              })}
              value={
                identitiesQuery.isPending ? (
                  <PendingValue
                    chars={10}
                    className={!revealPending ? 'invisible' : undefined}
                  />
                ) : identitiesQuery.isError ? (
                  <span className='pm-muted'>—</span>
                ) : linkedIdentities.length > 0 ? (
                  <span className='pm-mono'>
                    {linkedIdentities
                      .map((identity) =>
                        identity.email
                          ? `${identity.label} (${identity.email})`
                          : `${identity.label} ${t(
                              `${ns}.identity.linkedShort`,
                              { defaultValue: '已关联' },
                            )}`,
                      )
                      .join(' · ')}
                  </span>
                ) : (
                  <span className='pm-muted'>
                    {t(`${ns}.identity.noneLinked`, {
                      defaultValue: '未关联第三方登录',
                    })}
                  </span>
                )
              }
              actions={
                linkedIdentities.length > 0 ? (
                  <span className='set-row__ro'>
                    {catalogCopy('identity_unlink_unavailable')}
                  </span>
                ) : null
              }
            />
          </SetCard>

          {pricePlan?.live ? (
            <SetCard title={t(`${ns}.plan.rules`, { defaultValue: '折扣' })}>
              <SetRow
                label={t(`${ns}.plan.until`)}
                value={
                  pricePlan.ends_at
                    ? new Date(pricePlan.ends_at).toLocaleDateString()
                    : t(`${ns}.plan.forever`)
                }
              />
              <SetRow
                label={t(`${ns}.plan.lanes`)}
                value={t(`${ns}.plan.lane.${pricePlan.lanes}`)}
              />
              <SetRow
                label={t(`${ns}.plan.rules`)}
                value={
                  <span className='pm-plan-rules'>
                    {pricePlan.rules.map((rule) => (
                      <span
                        className='pm-plan-rule'
                        key={`${rule.kind}:${rule.vendor_name ?? rule.model_name}`}
                      >
                        {rule.kind === 'vendor'
                          ? rule.vendor_name
                          : rule.model_name}
                        <b>
                          {t(`${ns}.plan.zhe`, {
                            off: rule.pct,
                            zhe: zheOf(rule.pct),
                          })}
                        </b>
                      </span>
                    ))}
                    {pricePlan.default_pct ? (
                      <span className='pm-plan-rule'>
                        {t(`${ns}.plan.others`)}
                        <b>
                          {t(`${ns}.plan.zhe`, {
                            off: pricePlan.default_pct,
                            zhe: zheOf(pricePlan.default_pct),
                          })}
                        </b>
                      </span>
                    ) : null}
                  </span>
                }
                actions={
                  <Link className='pm-inline-link' to='/console/models'>
                    {t(`${ns}.plan.models`)}
                  </Link>
                }
              />
            </SetCard>
          ) : null}

          <SetCard title={t(`${ns}.alertCard.title`, { defaultValue: '提醒' })}>
            <SetRow
              label={t(`${ns}.session.quotaAlert`)}
              hint={
                expanded === 'notification'
                  ? t(`${ns}.hint.quotaAlertShort`, {
                      defaultValue: '低于阈值发一次，设为 0 关闭',
                    })
                  : undefined
              }
              value={
                !notificationReady ? (
                  userQuery.isError ? (
                    <span className='pm-muted'>—</span>
                  ) : (
                    <PendingValue
                      chars={18}
                      className={!revealPending ? 'invisible' : undefined}
                    />
                  )
                ) : alertNeedsEmail ? (
                  <span className='pm-muted'>
                    {t(`${ns}.mail.alertNeedsEmail`)}
                  </span>
                ) : (
                  notificationSummary
                )
              }
              open={expanded === 'notification'}
              actions={
                userQuery.isError ? (
                  <RowAction onClick={() => void userQuery.refetch()}>
                    {t('legacy.i18n.retry.e2d53a6d3a')}
                  </RowAction>
                ) : expanded === 'notification' ? null : (
                  <RowAction
                    disabled={!notificationReady}
                    onClick={() => toggleExpanded('notification')}
                  >
                    {t(`${ns}.action.edit`)}
                  </RowAction>
                )
              }
              editor={
                <SetEditor>
                  <form
                    className='grid gap-3'
                    onSubmit={notificationForm.handleSubmit((values) =>
                      saveNotificationMutation.mutate(values),
                    )}
                  >
                    <div className='grid gap-3 sm:grid-cols-2'>
                      <Field>
                        <Label>{t(`${ns}.notification.type`)}</Label>
                        <Select
                          value={notificationType}
                          onValueChange={(value) =>
                            notificationForm.setValue('warningType', value, {
                              shouldDirty: true,
                            })
                          }
                        >
                          <SelectItem value='email'>
                            {t(`${ns}.notification.email`)}
                          </SelectItem>
                          <SelectItem value='webhook'>
                            {t(`${ns}.notification.webhook`)}
                          </SelectItem>
                          <SelectItem value='bark'>Bark</SelectItem>
                          <SelectItem value='gotify'>Gotify</SelectItem>
                          {telegramStatus.data?.available ||
                          notificationType === 'telegram' ? (
                            <SelectItem value='telegram'>Telegram</SelectItem>
                          ) : null}
                        </Select>
                      </Field>
                      <Field>
                        <Label htmlFor='warningThreshold'>
                          {t(`${ns}.notification.threshold`)} (USD)
                        </Label>
                        <Input
                          id='warningThreshold'
                          type='number'
                          min='0'
                          step='0.01'
                          inputMode='decimal'
                          {...notificationForm.register('warningThreshold')}
                        />
                      </Field>
                      {notificationType === 'email' ? (
                        <Field>
                          <Label htmlFor='notificationEmail'>
                            {t(`${ns}.notification.emailTarget`)}
                          </Label>
                          <Input
                            id='notificationEmail'
                            placeholder={t(`${ns}.notification.emailFallback`)}
                            {...notificationForm.register('notificationEmail')}
                          />
                        </Field>
                      ) : null}
                      {notificationType === 'webhook' ? (
                        <>
                          <Field>
                            <Label htmlFor='webhookUrl'>
                              {t(`${ns}.notification.webhookUrl`)}
                            </Label>
                            <Input
                              id='webhookUrl'
                              {...notificationForm.register('webhookUrl')}
                            />
                          </Field>
                          <Field>
                            <Label htmlFor='webhookSecret'>
                              {t(`${ns}.notification.webhookSecret`)}
                            </Label>
                            <Input
                              id='webhookSecret'
                              {...notificationForm.register('webhookSecret')}
                            />
                          </Field>
                        </>
                      ) : null}
                      {notificationType === 'bark' ? (
                        <Field>
                          <Label htmlFor='barkUrl'>
                            {t(`${ns}.notification.barkUrl`)}
                          </Label>
                          <Input
                            id='barkUrl'
                            {...notificationForm.register('barkUrl')}
                          />
                        </Field>
                      ) : null}
                      {notificationType === 'gotify' ? (
                        <>
                          <Field>
                            <Label htmlFor='gotifyUrl'>
                              {t(`${ns}.notification.gotifyUrl`)}
                            </Label>
                            <Input
                              id='gotifyUrl'
                              {...notificationForm.register('gotifyUrl')}
                            />
                          </Field>
                          <Field>
                            <Label htmlFor='gotifyToken'>
                              {t(`${ns}.notification.gotifyToken`)}
                            </Label>
                            <Input
                              id='gotifyToken'
                              {...notificationForm.register('gotifyToken')}
                            />
                          </Field>
                          <Field>
                            <Label htmlFor='gotifyPriority'>
                              {t(`${ns}.notification.gotifyPriority`)}
                            </Label>
                            <Input
                              id='gotifyPriority'
                              max={10}
                              min={0}
                              type='number'
                              {...notificationForm.register('gotifyPriority')}
                            />
                          </Field>
                        </>
                      ) : null}
                    </div>
                    {notificationType === 'telegram' ? (
                      <TelegramLinkField />
                    ) : null}
                    <div className='set-ed__actions'>
                      <Button
                        onClick={() => setExpanded(null)}
                        size='sm'
                        type='button'
                        variant='secondary'
                      >
                        {t(`${ns}.action.cancel`)}
                      </Button>
                      <Button
                        disabled={!notificationReady}
                        loading={saveNotificationMutation.isPending}
                        size='sm'
                        type='submit'
                        variant='primary'
                      >
                        {t(`${ns}.notification.save`)}
                      </Button>
                    </div>
                  </form>
                </SetEditor>
              }
            />

            <SetRow
              label={t(`${ns}.mail.weekly`)}
              value={
                <span className='pm-muted-inline'>
                  {t(`${ns}.mail.weeklySchedule`, {
                    defaultValue: '每周一 09:30',
                  })}{' '}
                  ·{' '}
                  <Button
                    onClick={() => setDigestPreview('weekly')}
                    size='sm'
                    type='button'
                    variant='link'
                  >
                    {t(`${ns}.mail.previewLink`)}
                  </Button>
                </span>
              }
              actions={mailToggle(
                t(`${ns}.mail.weekly`),
                mailPrefs.weeklyDigest,
                'weekly_digest',
              )}
            />

            <SetRow
              label={t(`${ns}.mail.productUpdates`)}
              value={
                <span className='pm-muted-inline'>
                  {t(`${ns}.mail.productUpdatesShort`, {
                    defaultValue: '新模型与重要变更',
                  })}
                </span>
              }
              actions={mailToggle(
                t(`${ns}.mail.productUpdates`),
                mailPrefs.productUpdates,
                'product_updates',
              )}
            />

            {digestTimeZone ? (
              <SetRow
                label={t(`${ns}.preference.timezone`)}
                value={<span className='pm-mono'>{digestTimeZone}</span>}
                open={expanded === 'timezone'}
                actions={
                  expanded === 'timezone' ? null : (
                    <RowAction
                      onClick={() => {
                        setTimezoneDraft(digestTimeZone);
                        toggleExpanded('timezone');
                      }}
                    >
                      {t(`${ns}.username.change`)}
                    </RowAction>
                  )
                }
                editor={
                  <SetEditor>
                    <div className='pm-inline-form__row'>
                      <div className='set-ed__field'>
                        <Select
                          aria-label={t(`${ns}.preference.timezone`)}
                          value={timezoneDraft || digestTimeZone}
                          onValueChange={(value) => setTimezoneDraft(value)}
                        >
                          {listTimeZones(digestTimeZone).map((zone) => (
                            <SelectItem key={zone} value={zone}>
                              {zone}
                            </SelectItem>
                          ))}
                        </Select>
                      </div>
                      <div className='set-ed__actions'>
                        <Button
                          onClick={() => setExpanded(null)}
                          size='sm'
                          type='button'
                          variant='secondary'
                        >
                          {t(`${ns}.action.cancel`)}
                        </Button>
                        <Button
                          loading={saveTimezoneMutation.isPending}
                          size='sm'
                          type='button'
                          variant='primary'
                          onClick={() =>
                            saveTimezoneMutation.mutate(
                              timezoneDraft || digestTimeZone,
                            )
                          }
                        >
                          {t(`${ns}.notification.save`)}
                        </Button>
                      </div>
                    </div>
                  </SetEditor>
                }
              />
            ) : null}

            <p className='pm-card-foot'>
              {t(`${ns}.mail.alwaysSent`)}
              <Button
                onClick={() => setDigestPreview('monthly')}
                size='sm'
                type='button'
                variant='link'
              >
                {t(`${ns}.mail.previewMonthlyLink`)}
              </Button>
            </p>
          </SetCard>

          <section className='set-card pm-danger-card'>
            <SetRow
              label={
                <span className='pm-danger-label'>
                  {t(`${ns}.delete.shortTitle`, { defaultValue: '注销账号' })}
                </span>
              }
              value={
                <span className='pm-muted-inline'>
                  {t(`${ns}.delete.shortWarning`, {
                    defaultValue: '停用全部 Key，30 天内重新登录可恢复',
                  })}
                </span>
              }
              actions={
                <Button
                  className='pm-delete'
                  onClick={() => toggleExpanded('delete')}
                  size='sm'
                  type='button'
                  variant='ghost-danger'
                >
                  {t(`${ns}.delete.shortAction`, { defaultValue: '注销' })}
                </Button>
              }
            />
          </section>

          <Modal
            className='personal-setting-editor personal-digest-preview'
            open={digestPreview !== null}
            title={
              digestPreviewQuery.data?.subject ??
              t(`${ns}.mail.preview`, { defaultValue: '看看最近一期' })
            }
            onOpenChange={(open) => {
              if (!open) setDigestPreview(null);
            }}
            body={
              digestPreviewQuery.isPending ? (
                <DigestPreviewPending key={digestPreview} />
              ) : digestPreviewQuery.data ? (
                <iframe
                  className='personal-digest-preview__frame'
                  sandbox=''
                  srcDoc={digestPreviewQuery.data.html}
                  title={digestPreviewQuery.data.subject}
                />
              ) : (
                <p className='set-ed__note'>
                  {errorMessage(digestPreviewQuery.error)}
                </p>
              )
            }
          />

          <GateModal
            busy={deleteAccountMutation.isPending}
            confirmLabel={t(`${ns}.delete.confirmAction`)}
            error={deleteError}
            gate={{
              type: 'typed',
              expected: user.username || '',
              inputId: 'delete-confirm',
              label: t(`${ns}.delete.typeUsername`, {
                username: user.username,
              }),
            }}
            onConfirm={() => deleteAccountMutation.mutate()}
            onOpenChange={(open) => {
              if (!open) {
                setExpanded(null);
                deleteAccountMutation.reset();
              }
            }}
            open={expanded === 'delete'}
            title={t(`${ns}.delete.question`, { username: user.username })}
            body={
              <div className='pj-gate__lines'>
                <p>{t(`${ns}.delete.what`)}</p>
                <p>{t(`${ns}.delete.final`)}</p>
              </div>
            }
          />
        </div>
      </PersonalEditorContext.Provider>
    </ConsolePage>
  );
};

export default PersonalSetting;
