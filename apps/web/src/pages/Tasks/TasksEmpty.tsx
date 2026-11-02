/* 生成任务 before the first image or video: what the list will hold, a way
 * to make one in the console (when an extension adds a place for it) and the
 * call that makes one from code. The code carries the reader's own Key,
 * masked on screen until they ask for it; the full value is read from the
 * server only when they show or copy it. */
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  ArrowUpRight,
  ChevronDown,
  Copy,
  Eye,
  EyeOff,
  KeyRound,
  Lock,
  Plus,
} from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import {
  type SnippetKind,
  type SnippetLang,
  snippetPieces,
  snippetText,
} from './snippets';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '../../components/ui/dropdown-menu';
import { useStatus } from '../../context/Status';
import { extensionSidebarEntries } from '../../extensions';
import { API } from '../../helpers/api';
import { getDocsPageURL } from '../../helpers/brandUrls';
import { copy } from '../../helpers/clipboard';
import { showError } from '../../helpers/utils';
import { useSidebar } from '../../hooks/common/useSidebar';
import { errorMessage, unwrap } from '../../lib/api-error';
import { connectBaseURL } from '../Token/connect/ConnectModal';

const TK = 'page.console.tasks.start';
const KEY_PREFIX = 'sk-lazu-';
const MASK = '••••••••••••';

type StartKey = {
  canReveal: boolean;
  id: string;
  last4: string;
  name: string;
};

const asKey = (value: unknown): StartKey | null => {
  if (!value || typeof value !== 'object') return null;
  const record = value as Record<string, unknown>;
  const id = String(record.id ?? '');
  const status = Number(record.status_code ?? record.status);
  if (!id || (Number.isFinite(status) && status !== 1)) return null;
  const masked = String(record.masked_key ?? '');
  return {
    canReveal: record.can_reveal_key !== false,
    id,
    last4: masked.replace(/\*/g, '').slice(-4),
    name: String(record.name || ''),
  };
};

const fullKey = (value: string) =>
  value.startsWith('sk-') ? value : `sk-${value}`;

/** The extension surface that makes images, if this build has one: its
 *  sidebar entry carries the image icon. */
function useMediaSurface() {
  const status = useStatus()?.status;
  const { isModuleVisible } = useSidebar();
  const { t } = useTranslation();
  const entry = extensionSidebarEntries().find(
    (item) =>
      item.iconKey === 'image' &&
      (item.enabled ? item.enabled(status) : true) &&
      isModuleVisible('workspace', item.moduleKey),
  );
  return entry ? { href: entry.to, label: entry.label(t) } : null;
}

export function TasksEmpty() {
  const { t, i18n } = useTranslation();
  const queryClient = useQueryClient();
  const surface = useMediaSurface();
  const [kind, setKind] = useState<SnippetKind>('image');
  const [lang, setLang] = useState<SnippetLang>('curl');
  const [picked, setPicked] = useState('');
  const [shown, setShown] = useState(false);
  const [copied, setCopied] = useState(false);
  const [creating, setCreating] = useState(false);
  /* Full values read this visit, by Key id. Never cached beyond the page. */
  const [secrets, setSecrets] = useState<Record<string, string>>({});

  const keysQuery = useQuery({
    queryKey: ['tasks-start-keys'],
    queryFn: async () => {
      const data = unwrap<unknown>(await API.get('/api/token/?p=1&size=100'));
      const items =
        data && typeof data === 'object' && 'items' in data
          ? (data as { items: unknown }).items
          : data;
      return (Array.isArray(items) ? items : [])
        .map(asKey)
        .filter((item): item is StartKey => item !== null);
    },
  });
  const keys = keysQuery.data ?? [];
  const key = keys.find((item) => item.id === picked) ?? keys[0];
  const secret = key ? secrets[key.id] : undefined;

  const reveal = async (target: StartKey) => {
    if (secrets[target.id]) return secrets[target.id];
    const data = unwrap<{ key?: unknown } | null>(
      await API.post(`/api/token/${target.id}/reveal`),
    );
    if (typeof data?.key !== 'string') throw new Error(t(`${TK}.revealFailed`));
    const value = fullKey(data.key);
    setSecrets((current) => ({ ...current, [target.id]: value }));
    return value;
  };

  const toggleShown = async () => {
    if (!key) return;
    if (shown) {
      setShown(false);
      return;
    }
    try {
      await reveal(key);
      setShown(true);
    } catch (error) {
      showError(errorMessage(error));
    }
  };

  const base = connectBaseURL();
  const pieces = snippetPieces(kind, lang, base, {
    imagePrompt: t(`${TK}.imagePrompt`),
    taskIdLabel: t(`${TK}.taskId`),
    videoPrompt: t(`${TK}.videoPrompt`),
    waitComment: t(`${TK}.waitComment`),
  });

  const copyCode = async () => {
    if (!key) return;
    try {
      const value = await reveal(key);
      if (!(await copy(snippetText(pieces, value)))) {
        showError(t('page.console.tables.copyFailed'));
        return;
      }
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch (error) {
      showError(errorMessage(error));
    }
  };

  const createKey = async () => {
    setCreating(true);
    try {
      const data = unwrap<Record<string, unknown>>(
        await API.post('/api/token/', {
          expired_time: -1,
          name: t(`${TK}.newKeyName`),
          remain_quota: 0,
          unlimited_quota: true,
        }),
      );
      const created = asKey(
        data?.token && typeof data.token === 'object' ? data.token : data,
      );
      if (created && typeof data?.key === 'string') {
        setSecrets((current) => ({
          ...current,
          [created.id]: fullKey(String(data.key)),
        }));
        setPicked(created.id);
      }
      await queryClient.invalidateQueries({ queryKey: ['tasks-start-keys'] });
    } catch (error) {
      showError(errorMessage(error));
    } finally {
      setCreating(false);
    }
  };

  const keyText = key
    ? shown && secret
      ? secret
      : `${KEY_PREFIX}${MASK}${key.last4}`
    : '';
  const noKey = !keysQuery.isPending && !key;

  return (
    <div className='tasks-start'>
      <section
        aria-labelledby='tasks-start-title'
        className='tasks-start__make'
      >
        <div
          aria-label={t(`${TK}.previewLabel`)}
          className='tasks-start__ghosts'
        >
          {(['image', 'video'] as const).map((item) => (
            <div className='tasks-start__ghost' key={item}>
              <span
                aria-hidden='true'
                className={`tasks-start__thumb tasks-start__thumb--${item}`}
              />
              <span className='tasks-start__ghost-text'>
                <span className='tasks-start__ghost-title'>
                  {t(`${TK}.${item}Prompt`)}
                </span>
                <span className='tasks-start__ghost-meta'>
                  {t(`${TK}.${item}Meta`)}
                </span>
              </span>
              <span className='tasks-start__ghost-left'>{t(`${TK}.kept`)}</span>
            </div>
          ))}
        </div>
        <h2 className='tasks-start__title' id='tasks-start-title'>
          {t(`${TK}.title`)}
        </h2>
        <p className='tasks-start__lead'>
          {surface
            ? t(`${TK}.leadSurface`, { name: surface.label })
            : t(`${TK}.leadCode`)}
        </p>
        <span className='tasks-start__spacer' />
        {surface ? (
          <a className='tasks-start__go' href={surface.href}>
            {t(`${TK}.go`, { name: surface.label })}
            <ArrowUpRight aria-hidden='true' size={14} />
          </a>
        ) : null}
      </section>

      <section aria-labelledby='tasks-start-code' className='tasks-start__code'>
        <div className='tasks-start__code-head'>
          <div>
            <h3 id='tasks-start-code'>{t(`${TK}.codeTitle`)}</h3>
            <div className='tasks-start__sub'>
              {noKey ? t(`${TK}.subNoKey`) : t(`${TK}.sub`)}
            </div>
          </div>
          <div
            aria-label={t(`${TK}.kindLabel`)}
            className='logv2-seg usage-seg'
            role='group'
          >
            {(['image', 'video'] as const).map((item) => (
              <button
                aria-pressed={kind === item}
                className='logv2-seg__button'
                data-active={kind === item ? 'true' : undefined}
                key={item}
                onClick={() => setKind(item)}
                type='button'
              >
                {t(`${TK}.kind.${item}`)}
              </button>
            ))}
          </div>
        </div>

        <div className='tasks-start__window'>
          <div className='tasks-start__toolbar'>
            <div
              aria-label={t(`${TK}.langLabel`)}
              className='tasks-start__langs'
              role='tablist'
            >
              {(['curl', 'python'] as const).map((item) => (
                <button
                  aria-selected={lang === item}
                  className='tasks-start__lang'
                  key={item}
                  onClick={() => setLang(item)}
                  role='tab'
                  type='button'
                >
                  {item === 'curl' ? 'curl' : 'Python'}
                </button>
              ))}
            </div>
            <span className='tasks-start__spacer' />
            {key ? (
              <>
                {keys.length > 1 ? (
                  <DropdownMenu>
                    <DropdownMenuTrigger>
                      <button
                        aria-label={t(`${TK}.pickKey`)}
                        className='tasks-start__tb'
                        type='button'
                      >
                        <KeyRound aria-hidden='true' size={12} />
                        <span className='tasks-start__keyname'>
                          {key.name || key.last4}
                        </span>
                        <ChevronDown aria-hidden='true' size={11} />
                      </button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align='end'>
                      {keys.map((item) => (
                        <DropdownMenuItem
                          key={item.id}
                          onSelect={() => {
                            setPicked(item.id);
                            setShown(false);
                          }}
                        >
                          {item.name || item.last4}
                        </DropdownMenuItem>
                      ))}
                    </DropdownMenuContent>
                  </DropdownMenu>
                ) : (
                  <span className='tasks-start__tb tasks-start__tb--static'>
                    <KeyRound aria-hidden='true' size={12} />
                    <span className='tasks-start__keyname'>
                      {key.name || key.last4}
                    </span>
                  </span>
                )}
                {key.canReveal ? (
                  <>
                    <button
                      aria-label={
                        shown ? t(`${TK}.hideKey`) : t(`${TK}.showKey`)
                      }
                      aria-pressed={shown}
                      className='tasks-start__tb tasks-start__tb--icon'
                      onClick={() => void toggleShown()}
                      type='button'
                    >
                      {shown ? (
                        <EyeOff aria-hidden='true' size={14} />
                      ) : (
                        <Eye aria-hidden='true' size={14} />
                      )}
                    </button>
                    <button
                      className='tasks-start__tb tasks-start__tb--primary'
                      onClick={() => void copyCode()}
                      type='button'
                    >
                      <Copy aria-hidden='true' size={12} />
                      {copied ? t(`${TK}.copied`) : t(`${TK}.copy`)}
                    </button>
                  </>
                ) : (
                  <Link className='tasks-start__tb' to='/console/token'>
                    {t(`${TK}.openKeys`)}
                  </Link>
                )}
              </>
            ) : noKey ? (
              <button
                className='tasks-start__tb tasks-start__tb--primary'
                disabled={creating}
                onClick={() => void createKey()}
                type='button'
              >
                <Plus aria-hidden='true' size={12} />
                {t(`${TK}.createKey`)}
              </button>
            ) : null}
          </div>
          <pre className='tasks-start__pre'>
            {pieces.map((piece, index) =>
              'key' in piece ? (
                key ? (
                  <span className='tasks-start__key' key={index}>
                    {keyText}
                  </span>
                ) : (
                  <span className='tasks-start__slot' key={index}>
                    {t(`${TK}.keySlot`)}
                  </span>
                )
              ) : piece.tone ? (
                <span className={`tasks-start__${piece.tone}`} key={index}>
                  {piece.text}
                </span>
              ) : (
                piece.text
              ),
            )}
          </pre>
        </div>

        <div className='tasks-start__foot'>
          <Lock aria-hidden='true' size={13} />
          <span>
            {noKey
              ? t(`${TK}.footNoKey`)
              : key && !key.canReveal
                ? t(`${TK}.footLegacy`)
                : t(`${TK}.foot`)}
          </span>
          <span className='tasks-start__spacer' />
          <a
            href={getDocsPageURL('endpoints/media', i18n.language)}
            rel='noreferrer'
            target='_blank'
          >
            {t(`${TK}.docs`)}
          </a>
        </div>
      </section>
    </div>
  );
}
