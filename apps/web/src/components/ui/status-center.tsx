import { Activity, CheckCircle2, X } from 'lucide-react';
import {
  useContext,
  useEffect,
  useLayoutEffect,
  useState,
  useSyncExternalStore,
} from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from './button';
import { IconButton } from './icon-button';
import { Popover, PopoverContent, PopoverTrigger } from './popover';
import { useConsoleScope } from '../../context/ConsoleScope';
import { UserContext } from '../../context/User';
import { cn } from '../../lib/cn';
import type { TFunction } from 'i18next';

export type StatusIssue = {
  count: number;
  firstSeenAt: number;
  id: string;
  kind: 'error' | 'warning' | 'unavailable';
  lastSeenAt: number;
  message: string;
  source?: string;
};

const LEGACY_STORAGE_KEY = 'lazu-status-center';
const STORAGE_KEY_PREFIX = 'lazu-status-center:';
const MAX_ISSUES = 30;
const OPEN_STATUS_CENTER_EVENT = 'lazu:status-center:open';
const listeners = new Set<() => void>();

const readStoredIssues = (storageKey: string): StatusIssue[] => {
  if (typeof window === 'undefined') return [];
  try {
    const value = JSON.parse(window.localStorage.getItem(storageKey) || '[]');
    return Array.isArray(value) ? (value as StatusIssue[]) : [];
  } catch {
    return [];
  }
};

const text = (value: unknown) =>
  typeof value === 'string' || typeof value === 'number'
    ? String(value).trim()
    : '';

const readStoredUser = (): Record<string, unknown> => {
  if (typeof window === 'undefined') return {};
  try {
    const value = JSON.parse(window.localStorage.getItem('user') || '{}');
    return value && typeof value === 'object'
      ? (value as Record<string, unknown>)
      : {};
  } catch {
    return {};
  }
};

const accountIdentity = (user: Record<string, unknown>) =>
  text(user.id) || text(user.username) || text(user.email) || 'anonymous';

const initialStatusScope = () => {
  const user = readStoredUser();
  const identity = accountIdentity(user);
  if (typeof window === 'undefined') return `${identity}:workspace:default`;
  const scope =
    window.localStorage.getItem(`lazu:console-scope:${identity}`) ||
    'workspace:default';
  return `${identity}:${scope}`;
};

const storageKeyForScope = (scope: string) =>
  `${STORAGE_KEY_PREFIX}${scope || 'anonymous:workspace:default'}`;

let activeStorageKey = storageKeyForScope(initialStatusScope());
let issues = readStoredIssues(activeStorageKey);
const emptyIssues: StatusIssue[] = [];

const emit = () => {
  if (typeof window !== 'undefined') {
    window.localStorage.setItem(activeStorageKey, JSON.stringify(issues));
  }
  listeners.forEach((listener) => listener());
};

export const setStatusIssueScope = (scope: string) => {
  const nextStorageKey = storageKeyForScope(scope);
  if (typeof window !== 'undefined') {
    window.localStorage.removeItem(LEGACY_STORAGE_KEY);
  }
  if (nextStorageKey === activeStorageKey) return;
  activeStorageKey = nextStorageKey;
  issues = readStoredIssues(activeStorageKey);
  listeners.forEach((listener) => listener());
};

const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => listeners.delete(listener);
};

const getSnapshot = () => issues;
const getServerSnapshot = () => emptyIssues;

export const recordStatusIssue = ({
  dedupeKey,
  kind = 'error',
  message,
  source,
}: Pick<StatusIssue, 'message'> &
  Partial<Pick<StatusIssue, 'kind' | 'source'>> & {
    dedupeKey?: string;
  }) => {
  const normalized = message.trim();
  if (!normalized) return;
  const id = dedupeKey
    ? `${kind}:dedupe:${dedupeKey}`
    : `${kind}:${source || 'app'}:${normalized}`;
  const now = Date.now();
  const existing = issues.find((issue) => issue.id === id);

  issues = existing
    ? [
        {
          ...existing,
          count: existing.count + 1,
          lastSeenAt: now,
          message: normalized,
          source,
        },
        ...issues.filter((issue) => issue.id !== id),
      ]
    : [
        {
          count: 1,
          firstSeenAt: now,
          id,
          kind,
          lastSeenAt: now,
          message: normalized,
          source,
        },
        ...issues,
      ];
  issues = issues.slice(0, MAX_ISSUES);

  // A caller may add operation-specific context after the interceptor reports
  // this failure. Enrich that occurrence without counting it twice or replacing
  // a newer occurrence, a dismissed issue, or another account's history.
  const recordedIssue = issues[0];
  const recordedScope = activeStorageKey;
  emit();
  return (message: string) => {
    if (
      !message.trim() ||
      activeStorageKey !== recordedScope ||
      !issues.includes(recordedIssue)
    )
      return;
    issues = issues.map((issue) =>
      issue === recordedIssue ? { ...issue, message: message.trim() } : issue,
    );
    emit();
  };
};

export const openStatusCenter = () => {
  if (typeof window === 'undefined') return;
  window.dispatchEvent(new Event(OPEN_STATUS_CENTER_EVENT));
};

export const clearStatusIssues = () => {
  issues = [];
  emit();
};

export const dismissStatusIssue = (id: string) => {
  issues = issues.filter((issue) => issue.id !== id);
  emit();
};

const formatTime = (timestamp: number) =>
  new Intl.DateTimeFormat(undefined, {
    hour: '2-digit',
    minute: '2-digit',
  }).format(timestamp);

/** Raw transport errors read like stack traces. Map the ones we recognise onto
 *  a short human title; anything else keeps its own text. */
const presentStatusMessage = (
  message: string,
  t: TFunction,
): { detail?: string; title: string } => {
  const stripped = message.trim();
  if (/backend status could not be loaded/i.test(stripped)) {
    return {
      title: t('statusCenter.title.statusUnavailable', {
        defaultValue: '运行时状态读不到',
      }),
      detail: stripped,
    };
  }
  if (/this device is offline/i.test(stripped)) {
    return {
      title: t('statusCenter.title.offline', { defaultValue: '设备已离线' }),
      detail: stripped,
    };
  }
  return { title: stripped };
};

type StatusCenterProps = {
  className?: string;
  /* 'floating' is the desktop button pinned to the window corner; 'inline'
     sits in the phone top bar, where a floating button would cover the page. */
  placement?: 'floating' | 'inline';
};

export function StatusCenter({
  className,
  placement = 'floating',
}: StatusCenterProps) {
  const inline = placement === 'inline';
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [userState] = useContext(UserContext);
  const { activeScope } = useConsoleScope();
  const storedUser = readStoredUser();
  const user =
    userState.user && typeof userState.user === 'object'
      ? (userState.user as Record<string, unknown>)
      : storedUser;
  const statusScope = `${accountIdentity(user)}:${activeScope.id}`;

  useLayoutEffect(() => {
    setStatusIssueScope(statusScope);
  }, [statusScope]);

  useEffect(() => {
    const handleOpen = () => setOpen(true);
    window.addEventListener(OPEN_STATUS_CENTER_EVENT, handleOpen);
    return () =>
      window.removeEventListener(OPEN_STATUS_CENTER_EVENT, handleOpen);
  }, []);

  const currentIssues = useSyncExternalStore(
    subscribe,
    getSnapshot,
    getServerSnapshot,
  );

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger>
        <IconButton
          className={cn(
            'lazu-status-center__trigger',
            inline && 'lazu-status-center__trigger--inline',
            className,
          )}
          aria-label={
            currentIssues.length
              ? t('statusCenter.openWithCount', {
                  count: currentIssues.length,
                })
              : t('statusCenter.open')
          }
          title={t('statusCenter.title')}
          variant={inline ? 'ghost' : 'secondary'}
        >
          <Activity aria-hidden='true' size={inline ? 19 : 17} />
          {currentIssues.length ? (
            <span className='lazu-status-center__count'>
              {Math.min(currentIssues.length, 9)}
              {currentIssues.length > 9 ? '+' : ''}
            </span>
          ) : null}
        </IconButton>
      </PopoverTrigger>
      <PopoverContent
        align='end'
        className='lazu-status-sheet'
        side={inline ? 'bottom' : 'top'}
        sideOffset={10}
      >
        <header className='lazu-status-sheet__head'>
          <b>{t('statusCenter.title')}</b>
          <span>
            {currentIssues.length
              ? t('statusCenter.sheetHint', {
                  count: currentIssues.length,
                  defaultValue: '{{count}} 条 · 点叉只清这一条',
                })
              : t('statusCenter.emptyTitle')}
          </span>
        </header>
        {currentIssues.length ? (
          <div className='lazu-status-sheet__list'>
            {currentIssues.map((issue) => {
              const presented = presentStatusMessage(issue.message, t);
              return (
                <article
                  className='lazu-status-sheet__item'
                  data-kind={issue.kind}
                  key={issue.id}
                >
                  <div>
                    <strong>{presented.title}</strong>
                    {presented.detail &&
                    presented.detail !== presented.title ? (
                      <p>{presented.detail}</p>
                    ) : null}
                    <span>
                      {issue.count > 1
                        ? t('statusCenter.repeated', { count: issue.count })
                        : formatTime(issue.lastSeenAt)}
                      {issue.source
                        ? ` · ${issue.source}`
                        : ` · ${t('statusCenter.defaultSource')}`}
                    </span>
                  </div>
                  <button
                    aria-label={t('statusCenter.dismissItem', {
                      defaultValue: '清除这条',
                    })}
                    className='lazu-status-sheet__dismiss'
                    type='button'
                    onClick={() => dismissStatusIssue(issue.id)}
                  >
                    <X aria-hidden='true' size={14} />
                  </button>
                </article>
              );
            })}
          </div>
        ) : (
          <div className='lazu-status-sheet__empty'>
            <CheckCircle2 aria-hidden='true' size={18} />
            <p>{t('statusCenter.emptyDescription')}</p>
          </div>
        )}
        {currentIssues.length ? (
          <footer className='lazu-status-sheet__foot'>
            <Button
              size='sm'
              variant='ghost'
              onClick={() => {
                clearStatusIssues();
                setOpen(false);
              }}
            >
              {t('statusCenter.clearHistory')}
            </Button>
          </footer>
        ) : null}
      </PopoverContent>
    </Popover>
  );
}
