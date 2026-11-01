/* 执行 + 撤销: a destructive action runs at once and offers a short undo
 * instead of a confirmation dialog. brand/foundations/feedback-system.md §10.
 *
 * Two kinds:
 * - deferred: nothing is sent yet. The rows named by `keys` read 删除中 until
 *   the countdown ends, then `commit` runs. Undo only clears the mark.
 * - immediate: the caller already did it and the server can take it back
 *   (a removed member). Undo calls `undo`.
 *
 * One undo is open at a time: starting another commits the previous one
 * first. Leaving the page commits too, with a keepalive request, so a reader
 * never walks away believing something was deleted that was not. */
import { useEffect, useState, useSyncExternalStore } from 'react';
import { notify } from './toast';
import { buildAPIURL } from '../../helpers/apiUrl';
import { getUserIdFromLocalStorage, showError } from '../../helpers/utils';
import { MOBILE_BREAKPOINT } from '../../hooks/common/useIsMobile';
import i18n from '../../i18n/i18n';
import { errorMessage } from '../../lib/api-error';

type UndoRequest = {
  body?: unknown;
  method: 'DELETE' | 'POST' | 'PUT';
  path: string;
};

type CommonOptions = {
  /** 「已删除 Smoke token」 */
  message: string;
  /** 「已恢复 Smoke token」. Leave empty when `undo` reports its own result. */
  restored?: string;
  seconds?: number;
};

type DeferredUndoOptions = CommonOptions & {
  /** Sends the deletion. Errors put the rows back with a 重试. */
  commit: () => Promise<unknown>;
  /** 「没删成，Smoke token 已放回」 */
  failed: string;
  /** The same deletion as a plain request, sent with keepalive when the page
   *  closes mid-countdown. Without it `commit` is tried as is. */
  keepalive?: UndoRequest;
  /** Rows to mark 删除中 while the countdown runs, e.g. `token:12`. */
  keys: string[];
  onCommitted?: () => void;
};

type ImmediateUndoOptions = CommonOptions & {
  onUndone?: () => void;
  undo: () => Promise<unknown>;
};

type Pending = {
  committedNote: string;
  deadline: number;
  id: string;
  options: DeferredUndoOptions | ImmediateUndoOptions;
  timer: number;
};

const UNDO_SECONDS = 10;

let pending: Pending | null = null;
let pendingKeys: ReadonlySet<string> = new Set();
const listeners = new Set<() => void>();

const emit = () => listeners.forEach((listener) => listener());
const setKeys = (next: Set<string>) => {
  pendingKeys = next;
  emit();
};

const isDeferred = (
  options: DeferredUndoOptions | ImmediateUndoOptions,
): options is DeferredUndoOptions => 'commit' in options;

const narrowScreen = () =>
  typeof window !== 'undefined' &&
  typeof window.matchMedia === 'function' &&
  window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`).matches;

const t = (key: string, defaultValue: string, values = {}) =>
  String(i18n.t(key, { defaultValue, ...values }));

/* The page is going away: an axios request would be cancelled with it, so the
   same request leaves as a keepalive fetch carrying the same auth. */
function sendKeepalive(request: UndoRequest) {
  const headers: Record<string, string> = {
    'Cache-Control': 'no-store',
    'Content-Type': 'application/json',
  };
  const userId = getUserIdFromLocalStorage();
  if (userId !== -1) headers['X-Lazu-User'] = String(userId);
  void fetch(buildAPIURL(request.path), {
    body: request.body === undefined ? undefined : JSON.stringify(request.body),
    credentials: 'include',
    headers,
    keepalive: true,
    method: request.method,
  }).catch(() => undefined);
}

function release(keys: string[]) {
  if (!keys.length) return;
  const next = new Set(pendingKeys);
  keys.forEach((key) => next.delete(key));
  setKeys(next);
}

async function commitDeferred(options: DeferredUndoOptions) {
  try {
    await options.commit();
    options.onCommitted?.();
  } catch (error) {
    // Put the row back and say so; 重试 sends the same request again.
    notify.error(options.failed, {
      action: {
        label: t('undo.retry', '重试'),
        onClick: () => void commitDeferred(options),
      },
      description: errorMessage(error) || undefined,
    });
    return;
  } finally {
    release(options.keys);
  }
}

/** Close the open undo and carry it out now. Returns the name of what was
 *  committed, for the next toast's 「…已删除，不能再撤销」 line. */
function settle(mode: 'commit' | 'keepalive'): string {
  const current = pending;
  if (!current) return '';
  pending = null;
  window.clearTimeout(current.timer);
  notify.dismiss(current.id);
  detachGlobals();
  if (isDeferred(current.options)) {
    if (mode === 'keepalive' && current.options.keepalive) {
      sendKeepalive(current.options.keepalive);
    } else {
      void commitDeferred(current.options);
    }
  }
  return current.options.message;
}

function undoNow() {
  const current = pending;
  if (!current) return;
  pending = null;
  window.clearTimeout(current.timer);
  notify.dismiss(current.id);
  detachGlobals();
  const { options } = current;
  if (isDeferred(options)) {
    release(options.keys);
    if (options.restored) notify.success(options.restored);
    return;
  }
  options
    .undo()
    .then(() => {
      options.onUndone?.();
      if (options.restored) notify.success(options.restored);
    })
    .catch((error) => showError(error));
}

const editableTarget = (target: EventTarget | null) =>
  target instanceof HTMLElement &&
  (target.isContentEditable ||
    ['INPUT', 'SELECT', 'TEXTAREA'].includes(target.tagName));

function onKeydown(event: KeyboardEvent) {
  if (
    event.key.toLowerCase() !== 'z' ||
    event.shiftKey ||
    !(event.metaKey || event.ctrlKey) ||
    editableTarget(event.target)
  ) {
    return;
  }
  event.preventDefault();
  undoNow();
}

function onPagehide() {
  settle('keepalive');
}

let globalsAttached = false;
function attachGlobals() {
  if (globalsAttached) return;
  globalsAttached = true;
  window.addEventListener('keydown', onKeydown);
  window.addEventListener('pagehide', onPagehide);
}
function detachGlobals() {
  if (!globalsAttached) return;
  globalsAttached = false;
  window.removeEventListener('keydown', onKeydown);
  window.removeEventListener('pagehide', onPagehide);
}

function start(options: DeferredUndoOptions | ImmediateUndoOptions) {
  const committedNote = settle('commit');
  const seconds = options.seconds ?? UNDO_SECONDS;
  const id = `undo-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
  const deadline = Date.now() + seconds * 1000;
  if (isDeferred(options)) {
    const next = new Set(pendingKeys);
    options.keys.forEach((key) => next.add(key));
    setKeys(next);
  }
  pending = {
    committedNote,
    deadline,
    id,
    options,
    timer: window.setTimeout(() => {
      if (pending?.id === id) settle('commit');
    }, seconds * 1000),
  };
  attachGlobals();
  notify.custom(
    () => (
      <UndoToast
        committedNote={committedNote}
        deadline={deadline}
        message={options.message}
        seconds={seconds}
      />
    ),
    {
      duration: Infinity,
      id,
      unstyled: true,
      ...(narrowScreen() ? { position: 'bottom-center' as const } : {}),
    },
  );
}

/** Mark `keys` as 删除中 and send `request` when the countdown ends. */
export function deferUndoable(options: DeferredUndoOptions) {
  start(options);
}

/** The action already happened; offer `undo` for the same countdown. */
export function offerUndo(options: ImmediateUndoOptions) {
  start(options);
}

const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => listeners.delete(listener);
};

/** Keys waiting on an undo countdown, for drawing their rows as 删除中. */
export function useUndoPending(): ReadonlySet<string> {
  return useSyncExternalStore(
    subscribe,
    () => pendingKeys,
    () => pendingKeys,
  );
}

function UndoToast({
  committedNote,
  deadline,
  message,
  seconds,
}: {
  committedNote: string;
  deadline: number;
  message: string;
  seconds: number;
}) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 250);
    return () => window.clearInterval(timer);
  }, []);
  const left = Math.max(0, deadline - now);
  const secondsLeft = Math.max(1, Math.ceil(left / 1000));
  const narrow = narrowScreen();
  return (
    <div className='lazu-undo-toast' role='status'>
      <div className='lazu-undo-toast__row'>
        <span className='lazu-undo-toast__message'>{message}</span>
        <span className='lazu-undo-toast__time'>
          {narrow
            ? t('undo.secondsShort', '{{n}} 秒', { n: secondsLeft })
            : t('undo.secondsLeft', '{{n}} 秒内可撤销', { n: secondsLeft })}
        </span>
        <button
          className='lazu-undo-toast__button'
          data-owns-chrome
          onClick={undoNow}
          type='button'
        >
          {t('undo.action', '撤销')}
        </button>
      </div>
      {committedNote ? (
        <span className='lazu-undo-toast__note'>
          {t('undo.committed', '{{name}}，不能再撤销', {
            name: committedNote,
          })}
        </span>
      ) : null}
      <span
        aria-hidden='true'
        className='lazu-undo-toast__bar'
        style={{ width: `${(left / (seconds * 1000)) * 100}%` }}
      />
    </div>
  );
}
