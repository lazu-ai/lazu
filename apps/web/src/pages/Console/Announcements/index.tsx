import { Plus, RefreshCcw } from 'lucide-react';
import { fromMarkdown } from 'mdast-util-from-markdown';
import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';
import './announcements.css';
import {
  Button,
  ConsolePage,
  ErrorState,
  Input,
  Skeleton,
  Modal,
  Popover,
  PopoverContent,
  PopoverTrigger,
  Select,
} from '../../../components/ui';
import { notify } from '../../../components/ui/toast';
import {
  ANNOUNCEMENTS_UPDATED_EVENT,
  type Announcement,
} from '../../../helpers/announcement';
import { API } from '../../../helpers/api';
import { showError, showSuccess } from '../../../helpers/utils';
import { unwrap } from '../../../lib/api-error';
import type { ReactNode } from 'react';

const MarkdownRenderer = lazy(
  () => import('../../../components/common/markdown/MarkdownRenderer'),
);

const LOCALES = ['zh', 'en', 'zh-TW', 'ja'] as const;
type LocaleCode = (typeof LOCALES)[number];

/* The kinds the console can author. `release_note` is authored by the deploy
   pipeline but still editable here, so it stays in the list. */
const KINDS = [
  { key: 'release_note', tone: 'feature' },
  { key: 'operational', tone: 'neutral' },
  { key: 'maintenance', tone: 'notice' },
  { key: 'incident', tone: 'danger' },
  { key: 'model_update', tone: 'model' },
] as const;

type KindKey = (typeof KINDS)[number]['key'];

const KIND_TONE: Record<string, string> = Object.fromEntries(
  KINDS.map((kind) => [kind.key, kind.tone]),
);

const KIND_LABEL_DEFAULTS: Record<KindKey, string> = {
  release_note: '功能上新',
  operational: '运营公告',
  maintenance: '维护',
  incident: '事故',
  model_update: '模型变更',
};

const deliveryLabel = (delivery?: string) =>
  (delivery || 'notice').toUpperCase();

/* Autonyms: each language is named in its own script, so these labels are
   intentionally identical in every console locale. */
const LOCALE_LABELS: Record<LocaleCode, string> = {
  zh: '中文',
  en: 'EN',
  'zh-TW': '繁中',
  ja: '日本語',
};

type LocaleDraft = { body: string; title: string };
type Draft = {
  affectedModels: string[];
  effectiveDate: string;
  id: number | null;
  kind: KindKey;
  locales: Record<LocaleCode, LocaleDraft>;
  summaries: Record<LocaleCode, string>;
};

type AdminAnnouncement = Announcement & {
  affected_models?: string[];
  body_i18n?: Record<string, unknown>;
  /* Declared explicitly: Announcement carries an index signature, so an
     inherited field would otherwise widen to unknown. */
  created_at?: string;
  effective_date?: string | null;
  id: number;
  kind?: string;
  published_at?: string | null;
  summary_i18n?: Record<string, string>;
  title_i18n?: Record<string, string>;
  unpublished_at?: string | null;
};

type AnnouncementPage = {
  items?: AdminAnnouncement[];
  next_before_id?: number | null;
};

type SavedAnnouncement = { id?: number };

const emptyLocales = (): Record<LocaleCode, LocaleDraft> =>
  Object.fromEntries(
    LOCALES.map((locale) => [locale, { body: '', title: '' }]),
  ) as Record<LocaleCode, LocaleDraft>;

const emptySummaries = (): Record<LocaleCode, string> =>
  Object.fromEntries(LOCALES.map((locale) => [locale, ''])) as Record<
    LocaleCode,
    string
  >;

const emptyDraft = (): Draft => ({
  affectedModels: [],
  effectiveDate: '',
  id: null,
  kind: 'operational',
  locales: emptyLocales(),
  summaries: emptySummaries(),
});

/* Stored bodies are arrays of paragraphs; the editor is one Markdown document.
   Join on blank lines so a round-trip through the editor is lossless. */
const bodyToMarkdown = (value: unknown): string => {
  if (Array.isArray(value)) return value.map(String).join('\n\n');
  if (typeof value === 'string') return value;
  return '';
};

type MarkdownNode = {
  alt?: unknown;
  children?: unknown;
  position?: {
    end?: { offset?: number };
    start?: { offset?: number };
  };
  type?: unknown;
  value?: unknown;
};

const markdownNodeToText = (value: unknown): string => {
  if (!value || typeof value !== 'object') return '';
  const node = value as MarkdownNode;
  if (node.type === 'html') return '';
  if (node.type === 'image') {
    return typeof node.alt === 'string' ? node.alt : '';
  }
  if (
    ['text', 'inlineCode', 'code'].includes(String(node.type)) &&
    typeof node.value === 'string'
  ) {
    return node.value;
  }
  if (!Array.isArray(node.children)) return '';
  const separator = ['root', 'blockquote', 'list', 'listItem'].includes(
    String(node.type),
  )
    ? ' '
    : '';
  return node.children.map(markdownNodeToText).filter(Boolean).join(separator);
};

/* AnnouncementView.Summary is printed as prose on the public status page.
   Parse the first block as Markdown and extract semantic text, including image
   alt text and code, instead of maintaining an incomplete syntax regex. */
const toSummary = (blocks: string[]): string =>
  markdownNodeToText(fromMarkdown(blocks[0] || ''))
    .replace(/\s+/g, ' ')
    .trim();

const markdownToBody = (value: string): string[] => {
  // Strip only surrounding blank lines. String.trim() would also remove the
  // indentation that can be meaningful inside Markdown code blocks.
  const source = value.replace(/^(?:\r?\n)+|(?:\r?\n)+$/g, '');
  if (!source) return [];
  const tree = fromMarkdown(source) as MarkdownNode;
  if (!Array.isArray(tree.children)) return [source];
  const nodes = tree.children
    .map((child) => (child as MarkdownNode).position)
    .filter(
      (
        position,
      ): position is {
        end: { offset: number };
        start: { offset: number };
      } =>
        typeof position?.start?.offset === 'number' &&
        typeof position?.end?.offset === 'number',
    );
  if (nodes.length === 0) return [source];

  const blocks: string[] = [];
  let blockStart = nodes[0].start.offset;
  let previousEnd = nodes[0].end.offset;
  for (const position of nodes.slice(1)) {
    const gap = source.slice(previousEnd, position.start.offset);
    if (/\r?\n[\t ]*\r?\n/.test(gap)) {
      blocks.push(source.slice(blockStart, previousEnd));
      blockStart = position.start.offset;
    }
    previousEnd = position.end.offset;
  }
  blocks.push(source.slice(blockStart, previousEnd));
  return blocks;
};

const announcementState = (
  item: AdminAnnouncement,
): 'published' | 'draft' | 'unpublished' => {
  if (item.unpublished_at) return 'unpublished';
  return item.published_at ? 'published' : 'draft';
};

const toDateInput = (value?: string | null): string =>
  value ? String(value).slice(0, 10) : '';

type AnnouncementsPageProps = {
  /** Rendered as the 运营 › 公告 tab: the 运营 title and tab row replace this
   *  page's own title and its 站内公告 / 运营配图 switcher. */
  opsTitle?: ReactNode;
  opsTabs?: ReactNode;
};

const AnnouncementsPage = ({
  opsTitle,
  opsTabs,
}: AnnouncementsPageProps = {}) => {
  const { t } = useTranslation();
  const [items, setItems] = useState<AdminAnnouncement[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [nextBeforeID, setNextBeforeID] = useState<number | null>(null);
  const [saving, setSaving] = useState(false);
  const [query, setQuery] = useState('');
  const [loadError, setLoadError] = useState(false);
  const [retryBeforeID, setRetryBeforeID] = useState<number>();
  const [kindFilter, setKindFilter] = useState<KindKey | 'all'>('all');
  const [draft, setDraft] = useState<Draft>(emptyDraft);
  const [activeLocale, setActiveLocale] = useState<LocaleCode>('zh');
  const [preview, setPreview] = useState(false);
  const [publishConfirm, setPublishConfirm] = useState(false);
  const [modelEntryOpen, setModelEntryOpen] = useState(false);
  const [modelEntry, setModelEntry] = useState('');
  const draftSessionRef = useRef(0);

  const kindLabel = useCallback(
    (kind: string) =>
      t(`page.console.announcements.kind.${kind}`, {
        defaultValue:
          KIND_LABEL_DEFAULTS[kind as KindKey] ??
          KIND_LABEL_DEFAULTS.release_note,
      }),
    [t],
  );

  const load = useCallback(async (beforeID?: number) => {
    const append = typeof beforeID === 'number';
    if (append) setLoadingMore(true);
    else setLoading(true);
    setLoadError(false);
    try {
      const data = unwrap<AnnouncementPage | undefined>(
        await API.get('/api/admin/announcements', {
          params: { before_id: beforeID, limit: 50 },
          disableDuplicate: true,
        }),
      );
      const page = Array.isArray(data?.items) ? data.items : [];
      setItems((current) => {
        if (!append) return page;
        const existing = new Set(current.map((item) => item.id));
        return [...current, ...page.filter((item) => !existing.has(item.id))];
      });
      const next = data?.next_before_id;
      setNextBeforeID(typeof next === 'number' && next > 0 ? next : null);
    } catch (error) {
      setRetryBeforeID(beforeID);
      setLoadError(true);
      showError(error);
    } finally {
      if (append) setLoadingMore(false);
      else setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const visibleItems = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return items.filter((item) => {
      if (kindFilter !== 'all' && (item.kind || '') !== kindFilter) {
        return false;
      }
      if (!needle) return true;
      const haystack = [
        item.title,
        ...LOCALES.map((locale) => item.title_i18n?.[locale] || ''),
      ]
        .join(' ')
        .toLowerCase();
      return haystack.includes(needle);
    });
  }, [items, kindFilter, query]);

  const startNew = useCallback(() => {
    draftSessionRef.current += 1;
    setDraft(emptyDraft());
    setActiveLocale('zh');
    setPreview(false);
  }, []);

  const startEdit = useCallback((item: AdminAnnouncement) => {
    draftSessionRef.current += 1;
    const locales = emptyLocales();
    LOCALES.forEach((locale) => {
      locales[locale] = {
        title: item.title_i18n?.[locale] || '',
        body: bodyToMarkdown(item.body_i18n?.[locale]),
      };
    });
    setDraft({
      affectedModels: Array.isArray(item.affected_models)
        ? item.affected_models
        : [],
      effectiveDate: toDateInput(item.effective_date),
      id: item.id,
      kind: (item.kind as KindKey) || 'operational',
      locales,
      summaries: Object.fromEntries(
        LOCALES.map((locale) => [locale, item.summary_i18n?.[locale] || '']),
      ) as Record<LocaleCode, string>,
    });
    setActiveLocale('zh');
    setPreview(false);
  }, []);

  const patchLocale = useCallback(
    (field: keyof LocaleDraft, value: string) => {
      setDraft((current) => ({
        ...current,
        locales: {
          ...current.locales,
          [activeLocale]: { ...current.locales[activeLocale], [field]: value },
        },
      }));
    },
    [activeLocale],
  );

  const save = useCallback(
    async (publish: boolean) => {
      const requestSession = draftSessionRef.current;
      const titles = Object.fromEntries(
        LOCALES.map((locale) => [locale, draft.locales[locale].title]),
      );
      if (!titles.zh?.trim() && !titles.en?.trim()) {
        showError(
          t('page.console.announcements.titleRequired', {
            defaultValue: '请至少填写中文或英文标题',
          }),
        );
        return false;
      }
      const bodies = Object.fromEntries(
        LOCALES.map((locale) => [
          locale,
          markdownToBody(draft.locales[locale].body),
        ]),
      );
      const summaries =
        draft.id === null
          ? Object.fromEntries(
              LOCALES.map((locale) => [
                locale,
                toSummary(bodies[locale] as string[]),
              ]),
            )
          : draft.summaries;
      const payload: Record<string, unknown> = {
        affected_models:
          draft.kind === 'model_update' ? draft.affectedModels : [],
        body_i18n: bodies,
        default_locale: 'zh',
        effective_date:
          draft.kind === 'model_update' ? draft.effectiveDate : '',
        kind: draft.kind,
        summary_i18n: summaries,
        title_i18n: titles,
      };
      // Existing records keep their current publication state when the editor
      // is merely saved. Publishing and the dedicated unpublish endpoint are
      // the only state transitions.
      if (publish || draft.id === null) payload.published = publish;
      setSaving(true);
      try {
        const saved = unwrap<SavedAnnouncement | undefined>(
          draft.id
            ? await API.put(`/api/admin/announcements/${draft.id}`, payload)
            : await API.post('/api/admin/announcements', payload),
        );
        showSuccess(
          publish
            ? t('page.console.announcements.published', {
                defaultValue: '公告已发布',
              })
            : t('page.console.announcements.savedDraft', {
                defaultValue: '草稿已保存',
              }),
        );
        // Every unread surface reads the same store, so tell them to refresh.
        window.dispatchEvent(new CustomEvent(ANNOUNCEMENTS_UPDATED_EVENT));
        await load();
        // Stay on what was just saved. Resetting to a blank draft here meant a
        // mid-write "存草稿" silently cleared the editor and the operator had to
        // hunt the item down in the list again.
        const savedID = draft.id ?? saved?.id;
        if (savedID && draftSessionRef.current === requestSession) {
          setDraft((current) => ({ ...current, id: Number(savedID) }));
        }
        return true;
      } catch (error) {
        showError(error);
        return false;
      } finally {
        setSaving(false);
      }
    },
    [draft, load, t],
  );

  const unpublish = useCallback(
    async (item: AdminAnnouncement) => {
      try {
        unwrap(await API.post(`/api/admin/announcements/${item.id}/unpublish`));
        notify.success(
          t('page.console.announcements.unpublished', {
            defaultValue: '公告已下架',
          }),
        );
        window.dispatchEvent(new CustomEvent(ANNOUNCEMENTS_UPDATED_EVENT));
        await load();
      } catch (error) {
        showError(error);
      }
    },
    [load, t],
  );

  const addModel = () => {
    const trimmed = modelEntry.trim();
    if (!trimmed) return;
    setDraft((current) =>
      current.affectedModels.includes(trimmed)
        ? current
        : { ...current, affectedModels: [...current.affectedModels, trimmed] },
    );
    setModelEntry('');
    setModelEntryOpen(false);
  };

  const currentLocale = draft.locales[activeLocale];
  const isModelUpdate = draft.kind === 'model_update';
  const editingItem = draft.id
    ? items.find((row) => row.id === draft.id)
    : undefined;
  const editingState = editingItem ? announcementState(editingItem) : null;

  return (
    <ConsolePage
      appearance='review'
      title={
        opsTitle ??
        t('page.console.announcements.title', { defaultValue: '公告' })
      }
      tabs={opsTabs}
      className='announcements-page--review'
      actions={
        <Button onClick={startNew} size='sm' variant='primary'>
          <Plus aria-hidden='true' size={14} />
          {t('page.console.announcements.new', {
            defaultValue: '新建公告',
          })}
        </Button>
      }
    >
      <div className='ann-workbench'>
        <aside className='ann-panel ann-list'>
          <div className='ann-list__search'>
            <Input
              aria-label={t('page.console.announcements.search', {
                defaultValue: '搜索公告',
              })}
              onChange={(event) => setQuery(event.target.value)}
              placeholder={t('page.console.announcements.search', {
                defaultValue: '搜索公告',
              })}
              value={query}
            />
          </div>
          <div className='ann-list__filters'>
            <Select
              aria-label={t('page.console.announcements.kindLabel', {
                defaultValue: '种类',
              })}
              value={kindFilter}
              onValueChange={(value) => setKindFilter(value as KindKey | 'all')}
              options={[
                {
                  value: 'all',
                  label: t('page.console.announcements.filterAll', {
                    defaultValue: '全部',
                  }),
                },
                ...KINDS.map((kind) => ({
                  value: kind.key,
                  label: kindLabel(kind.key),
                })),
              ]}
            />
          </div>
          <div className='ann-list__items'>
            {loadError ? (
              <ErrorState
                title={t('page.console.announcements.loadError', {
                  defaultValue: '公告读取失败',
                })}
                action={
                  <Button onClick={() => void load(retryBeforeID)}>
                    {t('common.retry', { defaultValue: '重试' })}
                  </Button>
                }
              />
            ) : null}
            {loading ? (
              <div style={{ padding: '14px' }}>
                <Skeleton />
              </div>
            ) : visibleItems.length === 0 ? (
              loadError ? null : (
                <p className='ann-list__empty'>
                  {t('page.console.announcements.empty', {
                    defaultValue: '没有匹配的公告',
                  })}
                </p>
              )
            ) : (
              visibleItems.map((item) => (
                <button
                  className={`ann-list__item${draft.id === item.id ? ' is-active' : ''}`}
                  data-owns-chrome=''
                  key={item.id}
                  onClick={() => startEdit(item)}
                  type='button'
                >
                  <span className='ann-list__title'>
                    {item.title ||
                      t('page.console.announcements.untitled', {
                        defaultValue: '未命名公告',
                      })}
                  </span>
                  <span className='ann-list__meta'>
                    <span
                      className='ann-kind'
                      data-tone={KIND_TONE[item.kind || ''] || 'neutral'}
                    >
                      {kindLabel(item.kind || 'release_note')}
                    </span>
                    <span
                      className='ann-state'
                      data-state={announcementState(item)}
                    >
                      {t(
                        `page.console.announcements.state.${announcementState(item)}`,
                        {
                          defaultValue: {
                            published: '已发布',
                            draft: '草稿',
                            unpublished: '已下架',
                          }[announcementState(item)],
                        },
                      )}
                    </span>
                    <span
                      className='ann-delivery'
                      data-delivery={item.delivery || 'notice'}
                    >
                      {deliveryLabel(item.delivery)}
                    </span>
                    <span className='ann-list__date'>
                      {toDateInput(item.published_at || item.created_at)}
                    </span>
                  </span>
                </button>
              ))
            )}
          </div>
          {nextBeforeID ? (
            <div className='ann-list__more'>
              <Button
                loading={loadingMore}
                onClick={() => void load(nextBeforeID)}
                size='sm'
                variant='secondary'
              >
                {t('page.console.announcements.loadMore', {
                  defaultValue: '加载更多',
                })}
              </Button>
            </div>
          ) : null}
        </aside>

        <section className='ann-panel ann-editor'>
          <div className='ann-editor__head'>
            <h2>
              {draft.id
                ? t('page.console.announcements.editing', {
                    defaultValue: '编辑公告',
                  })
                : t('page.console.announcements.new', {
                    defaultValue: '新建公告',
                  })}
            </h2>
            {/* The header restates the stored state so 发布 and 下架 read as
                transitions from something, not as bare verbs. */}
            {editingState ? (
              <span className='ann-state' data-state={editingState}>
                {t(`page.console.announcements.state.${editingState}`, {
                  defaultValue: {
                    published: '已发布',
                    draft: '草稿',
                    unpublished: '已下架',
                  }[editingState],
                })}
              </span>
            ) : null}
            {editingItem ? (
              <span
                className='ann-delivery'
                data-delivery={editingItem.delivery || 'notice'}
              >
                {deliveryLabel(editingItem.delivery)}
              </span>
            ) : null}
          </div>

          {/* The field stack is the only part that scrolls; head and footer
              stay put so 发布 is reachable without scrolling back up. */}
          <div className='ann-editor__body'>
            <div className='ann-field'>
              <span className='ann-label'>
                {t('page.console.announcements.titleLabel', {
                  defaultValue: '标题',
                })}
              </span>
              <Input
                onChange={(event) => patchLocale('title', event.target.value)}
                placeholder={t('page.console.announcements.titlePlaceholder', {
                  defaultValue: '一句话说明这次更新',
                })}
                value={currentLocale.title}
              />
            </div>
            <div className='ann-editor__selectors'>
              <label className='ann-field'>
                <span className='ann-label'>
                  {t('page.console.announcements.kindLabel', {
                    defaultValue: '种类',
                  })}
                </span>
                <Select
                  value={draft.kind}
                  onValueChange={(value) =>
                    setDraft((current) => ({
                      ...current,
                      kind: value as KindKey,
                    }))
                  }
                  options={KINDS.map((kind) => ({
                    value: kind.key,
                    label: kindLabel(kind.key),
                  }))}
                />
              </label>
              <label className='ann-field'>
                <span className='ann-label'>
                  {t('common.language', { defaultValue: '语言' })}
                </span>
                <Select
                  value={activeLocale}
                  onValueChange={(value) =>
                    setActiveLocale(value as LocaleCode)
                  }
                  options={LOCALES.map((locale) => ({
                    value: locale,
                    label: LOCALE_LABELS[locale],
                  }))}
                />
              </label>
            </div>

            <div className='ann-field ann-field--body'>
              <span className='ann-label'>
                {t('page.console.announcements.bodyLabel', {
                  defaultValue: '正文 · MARKDOWN',
                })}
              </span>
              <div className='ann-md'>
                <div className='ann-md__bar'>
                  <span className='ann-md__spacer' />
                  <button
                    aria-pressed={!preview}
                    className='ann-md__view'
                    data-owns-chrome=''
                    onClick={() => setPreview(false)}
                    type='button'
                  >
                    {t('page.console.announcements.write', {
                      defaultValue: '编辑',
                    })}
                  </button>
                  <button
                    aria-pressed={preview}
                    className='ann-md__view'
                    data-owns-chrome=''
                    onClick={() => setPreview(true)}
                    type='button'
                  >
                    {t('page.console.announcements.preview', {
                      defaultValue: '预览',
                    })}
                  </button>
                </div>
                {preview ? (
                  <div className='ann-md__preview'>
                    {currentLocale.body.trim() ? (
                      <Suspense fallback={null}>
                        <MarkdownRenderer
                          content={currentLocale.body}
                          fontSize={13}
                        />
                      </Suspense>
                    ) : (
                      <p className='ann-md__preview-empty'>
                        {t('page.console.announcements.previewEmpty', {
                          defaultValue: '还没有正文可预览',
                        })}
                      </p>
                    )}
                  </div>
                ) : (
                  <textarea
                    className='ann-md__input'
                    onChange={(event) =>
                      patchLocale('body', event.target.value)
                    }
                    placeholder={t(
                      'page.console.announcements.bodyPlaceholder',
                      {
                        defaultValue:
                          '支持 Markdown：**加粗**、`代码`、- 列表、[链接](https://…)',
                      },
                    )}
                    value={currentLocale.body}
                  />
                )}
              </div>
            </div>

            {isModelUpdate ? (
              <div className='ann-editor__row'>
                <div className='ann-field'>
                  <span className='ann-label'>
                    {t('page.console.announcements.affectedModels', {
                      defaultValue: '关联模型',
                    })}
                  </span>
                  <div className='ann-models'>
                    {draft.affectedModels.map((name) => (
                      <span className='ann-model' key={name}>
                        {name}
                        <button
                          aria-label={t('legacy.i18n.delete.f24e0e29f4', {
                            defaultValue: '删除',
                          })}
                          onClick={() =>
                            setDraft((current) => ({
                              ...current,
                              affectedModels: current.affectedModels.filter(
                                (model) => model !== name,
                              ),
                            }))
                          }
                          type='button'
                        >
                          ✕
                        </button>
                      </span>
                    ))}
                    <Popover
                      open={modelEntryOpen}
                      onOpenChange={(open) => {
                        setModelEntryOpen(open);
                        if (!open) setModelEntry('');
                      }}
                    >
                      <PopoverTrigger>
                        <button
                          className='ann-model-add'
                          data-owns-chrome=''
                          type='button'
                        >
                          {t('page.console.announcements.addModel', {
                            defaultValue: '＋添加',
                          })}
                        </button>
                      </PopoverTrigger>
                      <PopoverContent
                        align='start'
                        className='w-[300px] max-w-[calc(100vw-32px)]'
                      >
                        <form
                          className='grid gap-3'
                          onSubmit={(event) => {
                            event.preventDefault();
                            addModel();
                          }}
                        >
                          <Input
                            aria-label={t(
                              'page.console.announcements.addModelPrompt',
                              { defaultValue: '输入模型名称' },
                            )}
                            placeholder='gpt-4o-mini'
                            value={modelEntry}
                            onChange={(event) =>
                              setModelEntry(event.target.value)
                            }
                          />
                          <Button type='submit' disabled={!modelEntry.trim()}>
                            {t('page.console.announcements.addModel', {
                              defaultValue: '＋添加',
                            })}
                          </Button>
                        </form>
                      </PopoverContent>
                    </Popover>
                  </div>
                </div>
                <div className='ann-field'>
                  <span className='ann-label'>
                    {t('page.console.announcements.effectiveDate', {
                      defaultValue: '生效日期',
                    })}
                  </span>
                  <Input
                    className='ann-date'
                    onChange={(event) =>
                      setDraft((current) => ({
                        ...current,
                        effectiveDate: event.target.value,
                      }))
                    }
                    type='date'
                    value={draft.effectiveDate}
                  />
                </div>
              </div>
            ) : null}
          </div>

          <div className='ann-editor__foot'>
            {draft.id && editingState === 'published' ? (
              <Button
                onClick={() => {
                  if (editingItem) void unpublish(editingItem);
                }}
                size='sm'
                variant='ghost-danger'
              >
                {t('page.console.announcements.unpublish', {
                  defaultValue: '下架',
                })}
              </Button>
            ) : null}
            <Button
              loading={saving}
              onClick={() => void save(false)}
              size='sm'
              variant='secondary'
            >
              {t('page.console.announcements.saveDraft', {
                defaultValue: '存草稿',
              })}
            </Button>
            <Button
              loading={saving}
              onClick={() => setPublishConfirm(true)}
              size='sm'
              variant='primary'
            >
              {t('page.console.announcements.publish', {
                defaultValue: '发布',
              })}
            </Button>
          </div>
        </section>
      </div>
      <Modal
        open={publishConfirm}
        onOpenChange={setPublishConfirm}
        busy={saving}
        title={t('announcements.confirmPublish', {
          defaultValue: '确认发布公告',
        })}
        body={
          <div>
            <h3>
              {draft.locales[activeLocale].title ||
                draft.locales.zh.title ||
                draft.locales.en.title}
            </h3>
            <p>
              {t('announcements.publishHint', {
                defaultValue: '发布后会立即出现在用户的公告列表中。',
              })}
            </p>
          </div>
        }
        footer={
          <>
            <Button
              variant='secondary'
              disabled={saving}
              onClick={() => setPublishConfirm(false)}
            >
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              loading={saving}
              onClick={async () => {
                if (await save(true)) setPublishConfirm(false);
              }}
            >
              {t('page.console.announcements.publish', {
                defaultValue: '发布',
              })}
            </Button>
          </>
        }
      />
    </ConsolePage>
  );
};

export default AnnouncementsPage;
