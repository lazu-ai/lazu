import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Trash2 } from 'lucide-react';
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
import { UserContext } from '../../context/User';
import {
  API,
  getMicroUSDPerUSD,
  setUserData,
  showSuccess,
} from '../../helpers';
import { useDelayedReveal } from '../../hooks/common/useDelayedReveal';
import { useSelfQuery } from '../../hooks/common/useSelfQuery';
import { apiErrorHandler, errorMessage, unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';
import useAuthStatus from '../auth/useAuthStatus';
import {
  Button,
  Modal,
  PendingBlock,
  PendingValue,
  Field,
  FieldMessage,
  InlineNotice,
  Input,
  Label,
  Select,
  SelectItem,
  Switch,
  SettingsCard,
  SettingsRow,
} from '../ui';
import ConsolePage from '../ui/console-page';
import './personal.css';
import '../../styles/settings-surface.css';
import type { ChangeEvent } from 'react';

const ns = 'page.console.personal';
const IDENTITY_QUERY_KEY = [...queryKeys.user, 'self', 'identities'] as const;
// Shown only until the first verdict arrives; the server publishes the real
// number in every response.
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
});

const passwordByteCount = (value: string) =>
  new TextEncoder().encode(value).byteLength;

const passwordSchema = z
  .object({
    // Optional here because a social-only account has no password its owner
    // could type; the backend still demands it once one exists.
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

const DEFAULT_QUOTA_WARNING_THRESHOLD_USD = 0.5;

type NotificationFormInput = z.input<typeof notificationSchema>;
type NotificationFormData = z.output<typeof notificationSchema>;
type PricePlanRule = {
  kind: 'vendor' | 'model';
  model_name?: string;
  pct: number;
  vendor_name?: string;
};

/** The customer's own 专属折扣, as /api/user/self reports it. */
type PricePlanView = {
  default_pct?: number | null;
  ends_at?: string | null;
  lanes: 'all' | 'cheap' | 'direct';
  live: boolean;
  name: string;
  rules: PricePlanRule[];
};

const zheOf = (pct: number) => Math.round((100 - pct) * 10) / 100;

type UserRecord = Record<string, unknown> & {
  email?: string;
  price_plan?: PricePlanView | null;
  id?: number | string;
  role?: number | string;
  setting?: string;
  username?: string;
};

const defaultNotificationSettings: NotificationFormData = {
  warningType: 'email',
  // Form operates in USD; converted to/from microUSD at the API boundary.
  warningThreshold: DEFAULT_QUOTA_WARNING_THRESHOLD_USD,
  webhookUrl: '',
  webhookSecret: '',
  notificationEmail: '',
  barkUrl: '',
  gotifyUrl: '',
  gotifyToken: '',
  gotifyPriority: 5,
};

const settingsFromUser = (
  user: UserRecord | null | undefined,
): NotificationFormData => {
  if (!user?.setting) return defaultNotificationSettings;
  try {
    const settings = JSON.parse(user.setting);
    return {
      warningType: settings.notify_type || 'email',
      warningThreshold:
        settings.quota_warning_threshold != null
          ? settings.quota_warning_threshold / getMicroUSDPerUSD()
          : DEFAULT_QUOTA_WARNING_THRESHOLD_USD,
      webhookUrl: settings.webhook_url || '',
      webhookSecret: settings.webhook_secret || '',
      notificationEmail: settings.notification_email || '',
      barkUrl: settings.bark_url || '',
      gotifyUrl: settings.gotify_url || '',
      gotifyToken: settings.gotify_token || '',
      gotifyPriority:
        settings.gotify_priority !== undefined ? settings.gotify_priority : 5,
    };
  } catch (error) {
    return defaultNotificationSettings;
  }
};

const toNotificationPayload = (values: NotificationFormData) => ({
  notify_type: String(values.warningType || ''),
  quota_warning_threshold: Math.round(
    Number(values.warningThreshold) * getMicroUSDPerUSD(),
  ),
  webhook_url: String(values.webhookUrl || ''),
  webhook_secret: String(values.webhookSecret || ''),
  notification_email: String(values.notificationEmail || ''),
  bark_url: String(values.barkUrl || ''),
  gotify_url: String(values.gotifyUrl || ''),
  gotify_token: String(values.gotifyToken || ''),
  gotify_priority:
    Number.parseInt(String(values.gotifyPriority || ''), 10) || 5,
});

const resolveTimeZone = () => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || '';
  } catch {
    return '';
  }
};

type MailPrefs = {
  weeklyDigest: boolean;
  productUpdates: boolean;
  timezone: string;
};

// Usage digests and product updates default to on: the stored flags record
// only what the reader turned off.
const mailPrefsFromUser = (user: UserRecord | null | undefined): MailPrefs => {
  const prefs: MailPrefs = {
    weeklyDigest: true,
    productUpdates: true,
    timezone: '',
  };
  if (!user?.setting) return prefs;
  try {
    const settings = JSON.parse(user.setting) as Record<string, unknown>;
    prefs.weeklyDigest = settings.weekly_digest_off !== true;
    prefs.productUpdates = settings.product_updates_off !== true;
    prefs.timezone =
      typeof settings.timezone === 'string' ? settings.timezone : '';
  } catch {
    return prefs;
  }
  return prefs;
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

/* Row primitives for the settings surface.
 *
 * A row is label | value+actions | explanation. The actions sit in the same
 * cluster as the value rather than being pushed to the far margin, and the
 * explanation uses the muted token on a solid card so it actually reads.
 * Editing opens inside the row it belongs to, so the panel is never orphaned
 * from the thing it edits. Locked spec: brand/foundations/yunmu-console.md.
 */
const SetCard = SettingsCard;

const PersonalEditorContext = createContext({ close: () => {}, busy: false });

const SetRow = ({
  actions,
  editor,
  hint,
  id,
  label,
  open,
  value,
}: {
  actions?: React.ReactNode;
  editor?: React.ReactNode;
  hint?: React.ReactNode;
  id?: string;
  label: React.ReactNode;
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
      open={open}
      value={value}
    >
      {editor ? (
        <Modal
          className='personal-setting-editor'
          open={Boolean(open)}
          onOpenChange={(next) => {
            if (!next) close();
          }}
          title={label}
          busy={busy}
          body={editor}
        />
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

/** One action button in a row's value cluster. */
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

// The server decides whether a handle may be taken; this only spares a request
// for shapes it would certainly reject. Keep in step with usernamePattern in
// internal/controller/user_username.go.
const USERNAME_PATTERN = /^[a-zA-Z0-9][a-zA-Z0-9_-]{1,19}$/;

type UsernameVerdict = {
  available: boolean;
  /** The catalog code a save would fail with; absent when a save is fine. */
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

/** The console's sentence for a refusal the server would give, read from
 *  the same `errors` copy a failed save shows. */
const catalogCopy = (code: string, details: Record<string, unknown> = {}) =>
  errorMessage({ error: { code, details } });

/** Hold a value still until typing pauses, so the availability check runs once
 *  per candidate rather than once per keystroke. */
const useDebounced = <T,>(value: T, delay: number) => {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setSettled(value), delay);
    return () => clearTimeout(timer);
  }, [value, delay]);
  return settled;
};

// Each preview request starts after a user action, independently of route load.
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
  const [userState, userDispatch] = useContext(UserContext);
  const [expanded, setExpanded] = useState<ExpandedRow>(null);
  const [usernameDraft, setUsernameDraft] = useState('');
  const [deleteText, setDeleteText] = useState('');
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
    defaultValues: { email: '', code: '' },
  });
  const passwordForm = useForm({
    resolver: zodResolver(passwordSchema),
    defaultValues: {
      originalPassword: '',
      newPassword: '',
      confirmPassword: '',
    },
  });

  const userQuery = useSelfQuery(Boolean(userState?.user?.id));
  useEffect(() => {
    const data = userQuery.data;
    if (data) {
      userDispatch({ type: 'login', payload: data });
      setUserData(data);
    }
  }, [userQuery.data, userDispatch]);

  const revealPending = useDelayedReveal();
  const notificationReady = userQuery.isSuccess && Boolean(userQuery.data);
  const notificationSettings = JSON.stringify(settingsFromUser(userQuery.data));
  useEffect(() => {
    // A shared self refresh can change balance or membership while this form
    // is being edited. Keep the draft; only a successful save resets it.
    if (notificationReady && !notificationForm.formState.isDirty) {
      notificationForm.reset(
        JSON.parse(notificationSettings) as NotificationFormData,
      );
    }
  }, [notificationForm, notificationSettings, notificationReady]);

  const user = (userState?.user || userQuery.data || {}) as UserRecord;
  // The login payload in context can predate the plan; the fresh read has it.
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
      showSuccess(t(`${ns}.notification.saved`));
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
      showSuccess(t(`${ns}.notification.saved`));
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
      showSuccess(t(`${ns}.notification.saved`));
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
    mutationFn: async ({ email, code }) => {
      unwrap(
        await API.get('/api/user/email/bind', {
          params: { email, code },
        }),
      );
      return email;
    },
    onSuccess: (email) => {
      showSuccess(t(`${ns}.email.success`));
      setExpanded(null);
      emailForm.reset();
      userDispatch({ type: 'login', payload: { ...user, email } });
      queryClient.invalidateQueries({ queryKey: queryKeys.user });
    },
    onError: apiErrorHandler,
  });

  const sendCodeMutation = useMutation<void, Error, string>({
    mutationFn: async (email) => {
      unwrap(await API.get('/api/verification', { params: { email } }));
    },
    onSuccess: () => showSuccess(t(`${ns}.email.codeSent`)),
    onError: apiErrorHandler,
  });

  // Only a candidate that is new, well-shaped and settled is worth asking about;
  // anything else is answered locally and never reaches the endpoint.
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
    // A verdict is only true for the instant it was made; never serve a stale one.
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
      // The server re-decides under its allocation lock, so a handle taken while
      // this form was open comes back as its own code (username_unavailable)
      // rather than a generic failure.
      unwrap(
        await API.put(
          '/api/user/self',
          { username },
          { skipErrorHandler: true },
        ),
      );
    },
    onSuccess: async (_result, username) => {
      showSuccess(t(`${ns}.username.success`));
      setExpanded(null);
      setUsernameDraft('');
      userDispatch({ type: 'login', payload: { ...user, username } });
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
      showSuccess(t(`${ns}.password.success`));
      setExpanded(null);
      passwordForm.reset();
      await queryClient.invalidateQueries({ queryKey: IDENTITY_QUERY_KEY });
    },
    onError: apiErrorHandler,
  });

  const deleteAccountMutation = useMutation({
    mutationFn: async () => {
      if (deleteText !== user.username) {
        throw new Error(t(`${ns}.delete.confirmationError`));
      }
      unwrap(await API.delete('/api/user/self'));
    },
    onSuccess: async () => {
      showSuccess(t(`${ns}.delete.success`));
      await API.get('/api/user/logout');
      userDispatch({ type: 'logout' });
      localStorage.removeItem('user');
      navigate('/login');
    },
    onError: apiErrorHandler,
  });

  const notificationType = notificationForm.watch('warningType');
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

  // One line under the field, always saying exactly why saving is or is not
  // possible. The local checks answer instantly; the server answers the rest.
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
            : 'Gotify',
    threshold: Number(
      savedNotifications.warningThreshold ??
        DEFAULT_QUOTA_WARNING_THRESHOLD_USD,
    ).toFixed(2),
  });

  const hint = (key: string, fallback: string) =>
    t(`${ns}.hint.${key}`, { defaultValue: fallback });

  const emailUnbound = !user.email;
  const alertNeedsEmail =
    savedNotifications.warningType === 'email' &&
    emailUnbound &&
    !savedNotifications.notificationEmail;

  return (
    <ConsolePage
      appearance='review'
      title={
        <>
          <span aria-hidden='true' className='pm-avatar'>
            {(user.username || '?').slice(0, 1).toUpperCase()}
          </span>
          {t(`${ns}.title`)}
        </>
      }
      description={user.username || undefined}
      className='personal-page personal-page--minimal'
    >
      <PersonalEditorContext.Provider
        value={{
          close: () => {
            setDeleteText('');
            setExpanded(null);
          },
          busy:
            changeUsernameMutation.isPending ||
            changePasswordMutation.isPending ||
            bindEmailMutation.isPending ||
            deleteAccountMutation.isPending ||
            saveNotificationMutation.isPending,
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
              value={user.username || '--'}
              hint={hint(
                'username',
                '登录用，也是团队里显示的名字。30 天内只能改一次。',
              )}
              open={expanded === 'username'}
              actions={
                <RowAction onClick={openUsernameEditor}>
                  {t(`${ns}.username.change`)}
                </RowAction>
              }
              editor={
                <SetEditor>
                  <form
                    className='grid gap-3'
                    onSubmit={(event) => {
                      event.preventDefault();
                      if (!usernameSavable) return;
                      changeUsernameMutation.mutate(trimmedDraft);
                    }}
                  >
                    <p className='set-ed__t'>{t(`${ns}.username.title`)}</p>
                    <div>
                      <label className='set-ed__lb' htmlFor='account-username'>
                        {t(`${ns}.username.field`)}
                      </label>
                      <Input
                        autoCapitalize='none'
                        autoComplete='off'
                        autoCorrect='off'
                        className='set-ed__field'
                        id='account-username'
                        maxLength={20}
                        onChange={(event: ChangeEvent<HTMLInputElement>) =>
                          setUsernameDraft(event.target.value)
                        }
                        placeholder={t(`${ns}.username.placeholder`)}
                        spellCheck={false}
                        value={usernameDraft}
                      />
                      <div
                        aria-live='polite'
                        className={`set-ed__state set-ed__state--${usernameState.tone}`}
                      >
                        {usernameState.text}
                      </div>
                    </div>
                    <p className='set-ed__d'>
                      {t(`${ns}.username.hint`, {
                        days:
                          usernameCheckQuery.data?.cooldown_days ??
                          DEFAULT_USERNAME_COOLDOWN_DAYS,
                      })}
                    </p>
                    <div className='set-ed__actions'>
                      <Button
                        disabled={!usernameSavable}
                        loading={changeUsernameMutation.isPending}
                        size='sm'
                        type='submit'
                        variant='primary'
                      >
                        {t(`${ns}.username.submit`)}
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
              id='personal-email-row'
              label={t(`${ns}.account.email`)}
              value={
                user.email || (
                  <span className='pm-muted'>
                    {t(`${ns}.fallback.noEmail`)}
                  </span>
                )
              }
              hint={
                user.email
                  ? hint('email', '余额提醒、安全通知和找回账户都发到这里。')
                  : emailCodeAuthAvailable
                    ? hint('emailUnbound', '绑定后可以用邮箱验证码登录。')
                    : hint(
                        'emailUnboundNoCode',
                        '绑定后用于余额提醒和找回账户。',
                      )
              }
              open={expanded === 'email'}
              actions={
                <RowAction onClick={() => toggleExpanded('email')}>
                  {user.email ? t(`${ns}.email.change`) : t(`${ns}.email.bind`)}
                </RowAction>
              }
              editor={
                <SetEditor>
                  <p className='set-ed__t'>{t(`${ns}.email.bind`)}</p>
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
                            : hint('emailCode', '验证码 10 分钟内有效。')}
                      </div>
                    </div>
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
                  t(`${ns}.error.loadUser`)
                ) : identitiesQuery.data.has_password ? (
                  t(`${ns}.identity.passwordSet`)
                ) : (
                  <span className='pm-muted'>
                    {t(`${ns}.identity.passwordUnset`)}
                  </span>
                )
              }
              hint={hint(
                'password',
                '可选，CLI 和脚本登录要用。改密码会让其他设备下线。',
              )}
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
                      : t(`${ns}.identity.changePassword`)}
                  </RowAction>
                )
              }
              editor={
                <SetEditor>
                  <p className='set-ed__t'>
                    {identitiesQuery.data?.has_password === false
                      ? t(`${ns}.identity.setPassword`)
                      : t(`${ns}.identity.changePassword`)}
                  </p>
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
                          : hint(
                              'passwordScope',
                              '修改后其他所有设备会被登出，这台设备保持登录。',
                            )}
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
            {(identitiesQuery.data?.identities ?? []).map((identity) => (
              <SetRow
                key={identity.provider}
                label={identity.label}
                value={identity.email || t(`${ns}.identity.noEmail`)}
                hint={hint(
                  'linkedProvider',
                  '与邮箱验证码指向同一账户，不会新建账号。',
                )}
                actions={
                  <span className='set-row__ro'>
                    {catalogCopy('identity_unlink_unavailable')}
                  </span>
                }
              />
            ))}
            {pricePlan?.live ? (
              <>
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
                    <Link to='/console/models'>{t(`${ns}.plan.models`)}</Link>
                  }
                />
              </>
            ) : null}
          </SetCard>

          <SetCard title={t(`${ns}.mail.title`)}>
            <SetRow
              label={t(`${ns}.session.quotaAlert`)}
              value={
                !notificationReady ? (
                  userQuery.isError ? (
                    t(`${ns}.error.loadUser`)
                  ) : (
                    <PendingValue
                      chars={24}
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
              hint={hint(
                'quotaAlert',
                '低于阈值时发一次，充值后重置。设为 0 表示关闭。',
              )}
              open={expanded === 'notification'}
              actions={
                userQuery.isError ? (
                  <RowAction onClick={() => void userQuery.refetch()}>
                    {t('legacy.i18n.retry.e2d53a6d3a')}
                  </RowAction>
                ) : (
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
                  <p className='set-ed__t'>{t(`${ns}.session.quotaAlert`)}</p>
                  <form
                    className='grid gap-4'
                    onSubmit={notificationForm.handleSubmit((values) =>
                      saveNotificationMutation.mutate(values),
                    )}
                  >
                    <div className='grid gap-4 sm:grid-cols-2'>
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
                    <div className='set-ed__actions'>
                      <Button
                        disabled={!notificationReady}
                        loading={saveNotificationMutation.isPending}
                        size='sm'
                        type='submit'
                        variant='primary'
                      >
                        {t(`${ns}.notification.save`)}
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
              label={t(`${ns}.mail.weekly`)}
              hint={hint('weeklyDigest', '每周一 09:30。那一周没用就不发。')}
              actions={
                <>
                  {mailToggle(
                    t(`${ns}.mail.weekly`),
                    mailPrefs.weeklyDigest,
                    'weekly_digest',
                  )}
                  <Button
                    onClick={() => setDigestPreview('weekly')}
                    size='sm'
                    type='button'
                    variant='link'
                  >
                    {t(`${ns}.mail.previewLink`)}
                  </Button>
                </>
              }
            />
            <SetRow
              label={t(`${ns}.mail.productUpdates`)}
              hint={hint('productUpdates', '大功能上线时发，一年几次。')}
              actions={mailToggle(
                t(`${ns}.mail.productUpdates`),
                mailPrefs.productUpdates,
                'product_updates',
              )}
            />
            {digestTimeZone ? (
              <SetRow
                label={t(`${ns}.mail.timezone`)}
                value={digestTimeZone}
                hint={hint('mailTimezone', '周报和月报按这个时区的 09:30 发。')}
                open={expanded === 'timezone'}
                actions={
                  <RowAction
                    onClick={() => {
                      setTimezoneDraft(digestTimeZone);
                      toggleExpanded('timezone');
                    }}
                  >
                    {t(`${ns}.username.change`)}
                  </RowAction>
                }
                editor={
                  <SetEditor>
                    <p className='set-ed__t'>
                      {t(`${ns}.preference.timezone`)}
                    </p>
                    <Field>
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
                    </Field>
                    <div className='set-ed__actions'>
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
                      <Button
                        onClick={() => setExpanded(null)}
                        size='sm'
                        type='button'
                      >
                        {t(`${ns}.action.cancel`)}
                      </Button>
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

          <SetCard title={t(`${ns}.data.title`)}>
            <SetRow
              label={
                <span className='pm-danger-label'>
                  {t(`${ns}.delete.title`)}
                </span>
              }
              hint={t(`${ns}.delete.warning`)}
              actions={
                <Button
                  className='pm-delete'
                  onClick={() => toggleExpanded('delete')}
                  size='sm'
                  type='button'
                  variant='ghost-danger'
                >
                  {t(`${ns}.delete.action`)}
                </Button>
              }
            />
          </SetCard>
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
                <p className='set-ed__t'>
                  {errorMessage(digestPreviewQuery.error)}
                </p>
              )
            }
          />
          <Modal
            className='personal-setting-editor'
            open={expanded === 'delete'}
            title={t(`${ns}.delete.title`)}
            busy={deleteAccountMutation.isPending}
            onOpenChange={(open) => {
              if (!open) {
                setDeleteText('');
                setExpanded(null);
              }
            }}
            body={
              <SetEditor tone='danger'>
                <p className='set-ed__d'>{t(`${ns}.delete.warning`)}</p>
                <label className='set-ed__lb' htmlFor='delete-confirm'>
                  {t(`${ns}.delete.confirmation`, {
                    username: user.username,
                  })}
                </label>
                <div className='set-ed__row'>
                  <Input
                    autoComplete='off'
                    className='set-ed__field'
                    id='delete-confirm'
                    onChange={(event: ChangeEvent<HTMLInputElement>) =>
                      setDeleteText(event.target.value)
                    }
                    placeholder={user.username}
                    value={deleteText}
                  />
                  <Button
                    disabled={deleteText !== user.username}
                    loading={deleteAccountMutation.isPending}
                    onClick={() => deleteAccountMutation.mutate()}
                    size='sm'
                    type='button'
                    variant='danger'
                  >
                    <Trash2 aria-hidden='true' size={14} />
                    {t(`${ns}.delete.submit`)}
                  </Button>
                  <Button
                    disabled={deleteAccountMutation.isPending}
                    onClick={() => {
                      setDeleteText('');
                      setExpanded(null);
                    }}
                    size='sm'
                    type='button'
                  >
                    {t(`${ns}.action.cancel`)}
                  </Button>
                </div>
              </SetEditor>
            }
          />
        </div>
      </PersonalEditorContext.Provider>
    </ConsolePage>
  );
};

export default PersonalSetting;
