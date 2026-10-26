import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, Plus, X } from 'lucide-react';
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import { useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';
import { useSettingsSaveGroup } from './headerActions';
import { readSettingInvalid } from './settingInvalid';
import { LegalDocumentPreview } from '../../components/common/DocumentRenderer/LegalDocument';
import {
  Button,
  FieldMessage,
  Input,
  Label,
  Modal,
  Select,
  SelectItem,
  Switch,
  Textarea,
} from '../../components/ui';
import { notify } from '../../components/ui/toast';
import { API, showWarning, toBoolean } from '../../helpers';
import { sanitizeMarkup } from '../../helpers/sanitize';
import '../../components/common/DocumentRenderer/legal-document.css';
import '../../styles/settings-surface.css';
import { apiErrorHandlerWith, unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';

/** A control that shares a row with its siblings — "1 分钟内最多 600 次". */
export type SettingPart = {
  key: string;
  kind: 'boolean' | 'number' | 'text';
  /** Short connective text rendered before the control. */
  lead?: string;
  prefix?: string;
  unit?: string;
  width?: 'xs' | 'sm' | 'md';
};

/** One column of a structured setting — a rate-limit group, a safety rule. */
export type SettingColumn = {
  key: string;
  kind: 'number' | 'select' | 'text';
  label: string;
  options?: Array<{ label: string; value: string }>;
  placeholder?: string;
  /** Fixed rows keep their key; only their values are editable. */
  readOnlyOnFixedRows?: boolean;
  width?: 'sm' | 'md' | 'lg';
};

/** One localized document behind a single row of language chips. */
export type SettingDoc = {
  key: string;
  label: string;
};

export type SettingField = {
  /** Short note on the row, e.g. 后端已有. */
  badge?: string;
  danger?: boolean;
  description?: string;
  docs?: SettingDoc[];
  /** Renders as a child of the row above it. */
  indent?: boolean;
  key: string;
  kind:
    | 'boolean'
    | 'chips'
    | 'docs'
    | 'group'
    | 'json'
    | 'money'
    | 'number'
    | 'rows'
    | 'select'
    | 'text'
    | 'textarea';
  label: string;
  /** rows: the label on the button that appends one. */
  addLabel?: string;
  /** rows: the columns of the structured editor. */
  columns?: SettingColumn[];
  /** rows: turns the stored JSON into editable rows. */
  decode?: (raw: string) => Array<Record<string, string>>;
  /** rows: turns the editable rows back into the stored JSON. */
  encode?: (rows: Array<Record<string, string>>) => string;
  /** rows: keys that always exist and cannot be removed or renamed. */
  fixedRows?: string[];
  /** rows: the columns that together name one entry. Defaults to the first. */
  identityColumns?: string[];
  /** rows: columns that must be filled before the setting can be saved. */
  requiredColumns?: string[];
  options?: Array<{ label: string; value: string }>;
  parts?: SettingPart[];
  placeholder?: string;
  prefix?: string;
  /** chips: what one entry may be. `number` stores a JSON array of integers. */
  chipKind?: 'number' | 'text';
  /** chips: entries that cannot be removed, because the backend adds them back. */
  lockedChips?: string[];
  /** chips: the same normalisation the backend applies, run before saving. */
  normalizeList?: (items: string[]) => string[];
  /** chips: how the list is stored. `csv` joins with ",", `lines` with "\n". */
  separator?: 'csv' | 'lines';
  /** Edits this text alongside the template in the same dialog. */
  subjectKey?: string;
  /** A switch rendered before the control, governing whether it applies. */
  toggleKey?: string;
  unit?: string;
  width?: 'xs' | 'sm' | 'md';
};

export type SettingSection = {
  /** Short note on a group that moved, e.g. 从「安全」移入. */
  badge?: string;
  /** Rendered instead of rows — a summary table, a read-only panel. */
  body?: ReactNode;
  description?: string;
  fields: SettingField[];
  /** Header action for the group, e.g. 编辑等级规则. */
  headerAction?: ReactNode;
  id: string;
  /** One line of deployment fact, rendered under the header. */
  status?: ReactNode;
  title: string;
};

type SettingsValues = Record<string, string | number | boolean>;

type LeafField = {
  key: string;
  kind: 'boolean' | 'money' | 'number' | 'text';
};

/* Money is stored in microUSD and shown in USD. Amounts are USD everywhere a
   reader can see them; microUSD never leaves the wire. */
const MICRO_USD_PER_USD = 1_000_000;

type SettingsGroupPageProps = {
  defaults: SettingsValues;
  schema: z.ZodTypeAny;
  sections: SettingSection[];
  title: string;
};

/* Settings keys are flat identifiers, but many of them contain dots
 * (`performance_setting.monitor_cpu_threshold`). React Hook Form reads every
 * name as a path, so it stored those nested while the schema, the error lookup
 * and the loaded values all used the literal string. Values still round-tripped
 * — register and getValues agreed with each other — but `form.trigger()` never
 * saw the fields the schema declared, so the 0-100 host thresholds and the 0-1
 * thinking budgets accepted anything and saved it, and an error on a dotted key
 * could never be rendered. The form layer works in aliases with no dots in
 * them; the real key is used for everything that talks to the API.
 */
const formKey = (key: string) => key.replace(/\./g, '__');

const toFormValues = (values: SettingsValues) =>
  Object.fromEntries(
    Object.entries(values).map(([key, value]) => [formKey(key), value]),
  ) as SettingsValues;

/** The same checks, keyed the way the form actually stores its values. */
const aliasSchema = (schema: z.ZodTypeAny) => {
  const shape = (schema as unknown as { shape?: Record<string, z.ZodTypeAny> })
    .shape;
  if (!shape) return schema;
  return z.object(
    Object.fromEntries(
      Object.entries(shape).map(([key, value]) => [formKey(key), value]),
    ),
  );
};

const serializeValue = (value: unknown) =>
  typeof value === 'boolean' ? String(value) : String(value ?? '');

/** The value as the API stores it: money leaves the form in microUSD. */
const wireValue = (leaf: LeafField, value: unknown) => {
  if (leaf.kind !== 'money') return serializeValue(value);
  const dollars = Number(value);
  if (!Number.isFinite(dollars)) return '0';
  return String(Math.round(dollars * MICRO_USD_PER_USD));
};

/** Every key a field owns. A group owns one per part, a docs row one per locale. */
const leavesOf = (field: SettingField): LeafField[] => {
  const extra: LeafField[] = [];
  if (field.toggleKey) extra.push({ key: field.toggleKey, kind: 'boolean' });
  if (field.subjectKey) extra.push({ key: field.subjectKey, kind: 'text' });

  if (field.kind === 'group') {
    return [
      ...extra,
      ...(field.parts || []).map(
        (part): LeafField => ({
          key: part.key,
          kind: part.kind === 'boolean' ? 'boolean' : part.kind,
        }),
      ),
    ];
  }
  if (field.kind === 'docs') {
    return [
      ...extra,
      ...(field.docs || []).map(
        (doc): LeafField => ({
          key: doc.key,
          kind: 'text',
        }),
      ),
    ];
  }
  if (field.kind === 'boolean') {
    return [...extra, { key: field.key, kind: 'boolean' }];
  }
  if (field.kind === 'rows') {
    return [...extra, { key: field.key, kind: 'text' }];
  }
  if (field.kind === 'money') {
    return [...extra, { key: field.key, kind: 'money' }];
  }
  if (field.kind === 'number') {
    return [...extra, { key: field.key, kind: 'number' }];
  }
  return [...extra, { key: field.key, kind: 'text' }];
};

const normalizeOptions = (
  items: unknown,
  leaves: LeafField[],
  defaults: SettingsValues,
) => {
  const next: SettingsValues = { ...defaults };
  const leafMap = new Map(leaves.map((leaf) => [leaf.key, leaf]));
  if (!Array.isArray(items)) return next;

  items.forEach((item) => {
    if (!item || typeof item !== 'object') return;
    const record = item as { key?: unknown; value?: unknown };
    if (typeof record.key !== 'string') return;
    const leaf = leafMap.get(record.key);
    if (!leaf) return;

    if (leaf.kind === 'boolean') {
      next[leaf.key] = toBoolean(record.value);
      return;
    }
    if (leaf.kind === 'number' || leaf.kind === 'money') {
      const parsed = Number(record.value);
      const fallback = Number(defaults[leaf.key] || 0);
      const value = Number.isFinite(parsed) ? parsed : fallback;
      next[leaf.key] =
        leaf.kind === 'money' ? value / MICRO_USD_PER_USD : value;
      return;
    }
    next[leaf.key] = String(record.value ?? defaults[leaf.key] ?? '');
  });
  return next;
};

/* ── list values ─────────────────────────────────────────────────── */
/* Two stored shapes reach the same chip row: a comma-joined string
   (EmailDomainWhitelist) and a JSON array (the reminder schedule). Both parse
   to a string list here so the control never has to know which it is. */
const parseList = (raw: unknown, separator: SettingField['separator']) => {
  const value = String(raw ?? '').trim();
  if (!value) return [];
  if (separator === 'csv' || separator === 'lines') {
    return value
      .split(separator === 'csv' ? ',' : '\n')
      .map((entry) => entry.trim())
      .filter(Boolean);
  }
  try {
    const parsed: unknown = JSON.parse(value);
    if (Array.isArray(parsed)) return parsed.map((entry) => String(entry));
  } catch {
    // A hand-edited value that no longer parses is treated as one entry so the
    // reader can see and fix it rather than having it silently disappear.
  }
  return [value];
};

const formatList = (items: string[], separator: SettingField['separator']) => {
  if (separator === 'csv') return items.join(',');
  if (separator === 'lines') return items.join('\n');
  return JSON.stringify(
    items.map((item) => {
      const parsed = Number(item);
      return Number.isFinite(parsed) ? parsed : item;
    }),
  );
};

/* ── row ─────────────────────────────────────────────────────────── */
/* label | value + actions | explanation.
 *
 * The control follows the label across a fixed gutter and the explanation
 * takes the remaining width — see settings-surface.css. */
const SettingRow = ({
  badge,
  control,
  danger,
  description,
  indent,
  label,
  toggle,
}: {
  badge?: string;
  control: ReactNode;
  danger?: boolean;
  description?: ReactNode;
  indent?: boolean;
  label: ReactNode;
  toggle?: ReactNode;
}) => (
  <div className={`set-row${indent ? ' set-row--child' : ''}`}>
    <span className='set-row__lb'>
      {label}
      {danger ? (
        <AlertTriangle
          aria-hidden='true'
          className='ml-1.5 inline align-[-2px] text-[var(--lazu-warning)]'
          size={13}
        />
      ) : null}
      {badge ? <span className='set-row__badge'>{badge}</span> : null}
    </span>
    <span className='set-row__vv'>
      {toggle}
      {control}
    </span>
    {description ? <span className='set-row__hint'>{description}</span> : null}
  </div>
);

/** A number or text box that carries its unit inside the control. */
const ValueInput = ({
  ariaLabel,
  form,
  numeric,
  prefix,
  settingKey,
  placeholder,
  unit,
  width,
}: {
  ariaLabel: string;
  form: ReturnType<typeof useForm<SettingsValues>>;
  numeric?: boolean;
  prefix?: string;
  settingKey: string;
  placeholder?: string;
  unit?: string;
  width?: SettingField['width'];
}) => (
  <span className={`set-num set-num--${width || 'md'}`}>
    {prefix ? <span className='set-num__pre'>{prefix}</span> : null}
    <Input
      aria-label={ariaLabel}
      className='set-num__in'
      id={settingKey}
      placeholder={placeholder}
      type={numeric ? 'number' : 'text'}
      {...form.register(formKey(settingKey))}
    />
    {unit ? <span className='set-num__u'>{unit}</span> : null}
  </span>
);

/** A long text value shown as one line, edited in a dialog.
 *
 *  The dialog holds a draft: closing it discards, "应用" writes the value into
 *  the form and marks the row dirty. Nothing is persisted until the page's own
 *  save bar runs, so a template edit behaves like every other settings edit.
 */
function TemplateField({
  errorMessage,
  field,
  form,
}: {
  errorMessage?: string;
  field: SettingField;
  form: ReturnType<typeof useForm<SettingsValues>>;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState('');
  const [subjectDraft, setSubjectDraft] = useState('');
  /* A mail template is HTML, and reading HTML is not the same as seeing the
     mail. The dialog shows the markup you edit and the message it produces,
     sanitised the same way every other operator-authored document on the site
     is. Template variables stay visible as {{name}} — they are filled at send
     time, and seeing where they land is the point of the preview. */
  const [previewing, setPreviewing] = useState(false);
  const subjectKey = field.subjectKey;
  const value = String(form.watch(formKey(field.key)) ?? '');
  const preview = value.replace(/\s+/g, ' ').trim();

  return (
    <>
      <div className='set-template'>
        <span className='set-template__preview' title={preview || undefined}>
          {preview ||
            t('page.console.settingV2.runtime.templateEmpty', {
              defaultValue: '（空）',
            })}
        </span>
        <Button
          onClick={() => {
            setDraft(value);
            setPreviewing(false);
            if (subjectKey) {
              setSubjectDraft(
                String(form.getValues(formKey(subjectKey)) ?? ''),
              );
            }
            setOpen(true);
          }}
          size='sm'
          variant='secondary'
        >
          {t('page.console.tables.edit')}
        </Button>
      </div>
      {errorMessage ? <FieldMessage error>{errorMessage}</FieldMessage> : null}
      <Modal
        open={open}
        onOpenChange={setOpen}
        title={field.label}
        description={field.description}
        className='lazu-modal-content--wide'
        body={
          <>
            {subjectKey ? (
              <div className='set-template__subject'>
                <Label htmlFor={subjectKey}>
                  {t('page.console.settingV2.runtime.mailSubject', {
                    defaultValue: '邮件主题',
                  })}
                </Label>
                <Input
                  id={subjectKey}
                  onChange={(event) => setSubjectDraft(event.target.value)}
                  value={subjectDraft}
                />
              </div>
            ) : null}
            <div className='set-docs__tabs'>
              <button
                className='set-docs__tab'
                data-active={!previewing || undefined}
                onClick={() => setPreviewing(false)}
                type='button'
              >
                {t('page.console.settingV2.runtime.source', {
                  defaultValue: '源码',
                })}
              </button>
              <button
                className='set-docs__tab'
                data-active={previewing || undefined}
                onClick={() => setPreviewing(true)}
                type='button'
              >
                {t('page.console.settingV2.runtime.preview', {
                  defaultValue: '预览',
                })}
              </button>
            </div>
            {previewing ? (
              <div
                className='set-template__preview-pane'
                // Operator-authored markup, sanitised by DOMPurify before it
                // reaches the DOM — the same path the footer and the legal
                // documents already take.
                dangerouslySetInnerHTML={{ __html: sanitizeMarkup(draft) }}
              />
            ) : (
              <Textarea
                aria-label={String(field.label)}
                className='set-template__editor'
                onChange={(event) => setDraft(event.target.value)}
                placeholder={field.placeholder}
                rows={14}
                value={draft}
              />
            )}
          </>
        }
        footer={
          <>
            <span className='set-template__note'>
              {t('page.console.settingV2.runtime.templateNote', {
                defaultValue: '改动会回填到表单，随「保存」一起生效。',
              })}
            </span>
            <Button onClick={() => setOpen(false)} variant='secondary'>
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              onClick={() => {
                form.setValue(formKey(field.key), draft, {
                  shouldDirty: true,
                });
                if (subjectKey) {
                  form.setValue(formKey(subjectKey), subjectDraft, {
                    shouldDirty: true,
                  });
                }
                setOpen(false);
              }}
            >
              {t('page.console.settingV2.runtime.templateApply', {
                defaultValue: '应用到表单',
              })}
            </Button>
          </>
        }
      />
    </>
  );
}

/** A short list edited as chips — allowed domains, reminder days. */
function ChipsField({
  field,
  form,
}: {
  field: SettingField;
  form: ReturnType<typeof useForm<SettingsValues>>;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState('');
  const items = parseList(form.watch(formKey(field.key)), field.separator);

  /* Save what the backend will actually use. The reminder schedule is sorted,
     de-duplicated and always starts at day 1 on the server; a list that differs
     from that reloads showing the operator's version while mail follows
     another. */
  const commit = (next: string[]) => {
    const normalized = field.normalizeList ? field.normalizeList(next) : next;
    form.setValue(formKey(field.key), formatList(normalized, field.separator), {
      shouldDirty: true,
    });
  };
  const locked = new Set(field.lockedChips || []);

  /* A chip list with no separator is stored as a JSON array, and the reminder
     schedule unmarshals into []int. Letting "abc" through saved ["abc"], which
     the backend could not parse: it kept the previous cadence, returned success
     anyway, and the page reported a change that never happened. */
  const numeric = field.chipKind === 'number';
  const invalid =
    numeric && draft.trim().length > 0 && !/^[1-9]\d*$/.test(draft.trim());

  const add = () => {
    const entry = draft.trim();
    if (!entry || items.includes(entry) || invalid) {
      if (!invalid) setDraft('');
      return;
    }
    commit([...items, entry]);
    setDraft('');
  };

  return (
    <span className='set-chips'>
      {items.map((item, index) => (
        <span className='set-chip' key={`${item}-${index}`}>
          {item}
          {locked.has(item) ? null : (
            <button
              aria-label={t('page.console.settingV2.runtime.chipRemove', {
                defaultValue: '移除 {{item}}',
                item,
              })}
              className='set-chip__x'
              onClick={() => commit(items.filter((_, at) => at !== index))}
              type='button'
            >
              <X aria-hidden='true' size={12} />
            </button>
          )}
        </span>
      ))}
      <span className='set-chip set-chip--add'>
        <Plus aria-hidden='true' size={12} />
        <input
          aria-label={field.label}
          className='set-chip__in'
          inputMode={numeric ? 'numeric' : undefined}
          onBlur={add}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault();
              add();
            }
          }}
          placeholder={
            field.placeholder ||
            t('page.console.settingV2.runtime.chipAdd', {
              defaultValue: '添加',
            })
          }
          value={draft}
        />
      </span>
      {invalid ? (
        <FieldMessage error>
          {t('page.console.settingV2.runtime.chipNumberOnly', {
            defaultValue: '只能填正整数天数。',
          })}
        </FieldMessage>
      ) : null}
    </span>
  );
}

/** One row for a document kept per UI locale.
 *
 *  Eight textareas — four languages of two documents — used to be eight rows of
 *  the tallest kind the page has. A filled/empty chip per language says the
 *  same thing in one line, and the editor opens on the language you click.
 */
function DocsField({
  field,
  form,
}: {
  field: SettingField;
  form: ReturnType<typeof useForm<SettingsValues>>;
}) {
  const { t } = useTranslation();
  const docs = field.docs || [];
  const [openKey, setOpenKey] = useState<string | null>(null);
  /* Every locale the reader touches stays in here until they apply.
   *
   * Switching locale inside the dialog used to write the previous draft
   * straight into the form, so Cancel — or Escape, or the close button — left
   * those edits dirty and the page's Save would persist a change the reader
   * had just backed out of. */
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [previewing, setPreviewing] = useState(false);
  const open = docs.find((doc) => doc.key === openKey) || null;
  const values = docs.map((doc) => ({
    doc,
    filled: String(form.watch(formKey(doc.key)) ?? '').trim().length > 0,
  }));

  const draftFor = (key: string) =>
    key in drafts ? drafts[key] : String(form.getValues(formKey(key)) ?? '');
  const draft = openKey ? draftFor(openKey) : '';

  const editDraft = (value: string) => {
    if (!openKey) return;
    setDrafts((current) => ({ ...current, [openKey]: value }));
  };

  const close = () => {
    setDrafts({});
    setPreviewing(false);
    setOpenKey(null);
  };

  const applyDrafts = () => {
    for (const [key, value] of Object.entries(drafts)) {
      if (value !== String(form.getValues(formKey(key)) ?? '')) {
        form.setValue(formKey(key), value, { shouldDirty: true });
      }
    }
    close();
  };

  /* One line, not a row of language chips.
   *
   * The four locales are how the document is stored, not a decision the reader
   * makes on this page: login shows whichever version matches the visitor's
   * language and falls back to a filled one. Putting the four languages on the
   * settings row made the storage shape look like a choice. The row says how
   * much of the document exists; the dialog is where a language is picked. */
  const written = values.filter((entry) => entry.filled);

  return (
    <>
      <span className='set-docs__summary'>
        <span
          className='set-docs__count'
          data-empty={!written.length || undefined}
        >
          {written.length
            ? t('page.console.settingV2.runtime.docsWritten', {
                defaultValue: '已填 {{written}} / {{total}} 种语言',
                written: written.length,
                total: values.length,
              })
            : t('page.console.settingV2.runtime.docsEmpty', {
                defaultValue: '未填写，登录页不会要求同意',
              })}
        </span>
        <Button
          onClick={() => setOpenKey(docs[0]?.key ?? null)}
          size='sm'
          variant='secondary'
        >
          {t('page.console.tables.edit')}
        </Button>
      </span>
      <Modal
        open={Boolean(open)}
        onOpenChange={(next) => {
          if (!next) close();
        }}
        title={open ? `${field.label} · ${open.label}` : field.label}
        description={field.description}
        className='lazu-modal-content--wide'
        body={
          <>
            <div className='set-docs__tabs'>
              {docs.map((doc) => (
                <button
                  className='set-docs__tab'
                  data-active={doc.key === openKey || undefined}
                  key={doc.key}
                  onClick={() => setOpenKey(doc.key)}
                  type='button'
                >
                  {doc.label}
                </button>
              ))}
              <span className='set-docs__spacer' />
              <button
                className='set-docs__tab'
                data-active={!previewing || undefined}
                onClick={() => setPreviewing(false)}
                type='button'
              >
                {t('page.console.settingV2.runtime.source', {
                  defaultValue: '源码',
                })}
              </button>
              <button
                className='set-docs__tab'
                data-active={previewing || undefined}
                onClick={() => setPreviewing(true)}
                type='button'
              >
                {t('page.console.settingV2.runtime.preview', {
                  defaultValue: '预览',
                })}
              </button>
            </div>
            {/* The public page's own renderer, not a lookalike: what renders
                here is what a visitor is asked to agree to. */}
            {previewing ? (
              <div className='set-template__preview-pane set-docs__preview'>
                <LegalDocumentPreview content={draft} title={field.label} />
              </div>
            ) : (
              <Textarea
                aria-label={open ? `${field.label} ${open.label}` : field.label}
                className='set-template__editor'
                onChange={(event) => editDraft(event.target.value)}
                placeholder={field.placeholder}
                rows={16}
                value={draft}
              />
            )}
          </>
        }
        footer={
          <>
            <span className='set-template__note'>
              {t('page.console.settingV2.runtime.templateNote', {
                defaultValue: '改动会回填到表单，随「保存」一起生效。',
              })}
            </span>
            <Button onClick={close} variant='secondary'>
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button onClick={applyDrafts}>
              {t('page.console.settingV2.runtime.templateApply', {
                defaultValue: '应用到表单',
              })}
            </Button>
          </>
        }
      />
    </>
  );
}

/** A stored JSON object edited as the table it actually is.
 *
 *  These settings were a textarea holding raw JSON: `{"vip":[600,300]}` for a
 *  rate-limit group, `{"HARM_CATEGORY_HARASSMENT":"BLOCK_NONE"}` for Gemini's
 *  safety rules. Nothing on the page said what the keys were, what the values
 *  could be, or — for the rate-limit groups — which number of the pair was
 *  which. The field owns the encode/decode, so the reader sees named columns
 *  and the stored shape never changes.
 */
/** True when a stored value is empty or a JSON object — anything a table can
 *  honestly represent. */
const isRepresentable = (raw: string) => {
  if (!raw.trim()) return true;
  try {
    const parsed: unknown = JSON.parse(raw);
    return (
      Boolean(parsed) && typeof parsed === 'object' && !Array.isArray(parsed)
    );
  } catch {
    return false;
  }
};

function RowsField({
  field,
  form,
  onBlockChange,
}: {
  field: SettingField;
  form: ReturnType<typeof useForm<SettingsValues>>;
  onBlockChange: (key: string, blocked: boolean) => void;
}) {
  const { t } = useTranslation();
  const columns = field.columns || [];
  const fixed = field.fixedRows || [];
  const raw = String(form.watch(formKey(field.key)) ?? '');
  const decode = field.decode;
  const encode = field.encode;

  /* The rows live here, not only in the stored JSON.
   *
   * Encoding drops a row with no key — it cannot exist in the stored object —
   * so a table that re-read the JSON after every edit threw away the row
   * "添加" had just appended, and the reader could never type its name. The
   * JSON is written on every edit as before; it is read back only when it
   * changes from outside (a load, a reset), which is when it no longer matches
   * what these rows encode to. */
  const [rows, setRows] = useState<Array<Record<string, string>>>(() =>
    decode ? decode(raw) : [],
  );
  useEffect(() => {
    if (!decode || !encode) return;
    if (encode(rows) !== raw) setRows(decode(raw));
    // Only an outside change to the stored value should replace local rows.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [raw]);

  const commit = (next: Array<Record<string, string>>) => {
    setRows(next);
    if (!encode) return;
    const encoded = encode(next);
    if (encoded !== raw) {
      form.setValue(formKey(field.key), encoded, { shouldDirty: true });
    }
  };

  const editCell = (index: number, key: string, value: string) =>
    commit(
      rows.map((row, at) => (at === index ? { ...row, [key]: value } : row)),
    );

  /* A stored value the table cannot represent — malformed JSON the old
     textarea accepted — is shown as text to repair, not as an empty table.
     Rendering it empty meant the first edit encoded that emptiness and
     overwrote the rules that were there. */
  const representable = isRepresentable(raw);

  const firstKey = columns[0]?.key ?? 'key';

  /* The stored value is a map, so two rows with the same name encode to one
     entry and the earlier row's numbers vanish on save with nothing said.
     Mark every repeat so the reader sees which row will not survive. */
  /* Identity is whatever names one stored entry. For most maps that is the
     first column; Claude's headers are keyed by model *and* header, so two
     headers for one model are two entries, not a duplicate. */
  const identity = field.identityColumns?.length
    ? field.identityColumns
    : [firstKey];
  const duplicates = new Set<number>();
  const seenKeys = new Set<string>();
  rows.forEach((row, index) => {
    const parts = identity.map((column) => (row[column] || '').trim());
    if (parts.some((part) => !part)) return;
    const name = parts.join('\u0000');
    if (seenKeys.has(name)) duplicates.add(index);
    seenKeys.add(name);
  });

  /* A named row with an empty required cell would store an explicit empty
     value — for a Gemini model, an empty API version, which the adaptor puts
     into the request path as `//models/...`. */
  const incomplete = new Set<number>();
  const required = field.requiredColumns || [];
  rows.forEach((row, index) => {
    const filled = columns.some((column) => (row[column.key] || '').trim());
    if (!filled) return;
    /* A row with values but no name cannot be stored — the encoder drops it —
       so the form value never changed, no unsaved bar appeared, and switching
       tabs threw the visible draft away without asking. */
    const missingIdentity = identity.some(
      (column) => !(row[column] || '').trim(),
    );
    const missingRequired = required.some(
      (column) => !(row[column] || '').trim(),
    );
    if (missingIdentity || missingRequired) incomplete.add(index);
  });

  const blocked = !representable || duplicates.size > 0 || incomplete.size > 0;
  useEffect(() => {
    onBlockChange(field.key, blocked);
    return () => onBlockChange(field.key, false);
  }, [blocked, field.key, onBlockChange]);

  if (!representable) {
    return (
      <div className='set-rows'>
        <FieldMessage error>
          {t('page.console.settingV2.runtime.rowsMalformed', {
            defaultValue:
              '存储的值不是合法的 JSON 对象，表格无法显示。先在下面修正它，保存后会恢复成表格。',
          })}
        </FieldMessage>
        <Textarea
          aria-label={field.label}
          className='set-template__editor'
          rows={6}
          {...form.register(formKey(field.key))}
        />
      </div>
    );
  }
  const isFixed = (row: Record<string, string>) =>
    fixed.includes(row[firstKey]);

  return (
    <div className='set-rows'>
      <div className='set-rows__head'>
        {columns.map((column) => (
          <span
            className={`set-rows__h set-rows__c--${column.width || 'md'}`}
            key={column.key}
          >
            {column.label}
          </span>
        ))}
        <span className='set-rows__h set-rows__c--act' />
      </div>
      {rows.map((row, index) => (
        // Keyed by position: the first column is editable, and a key built
        // from its value remounted the input — and lost focus — per keystroke.
        <div
          className='set-rows__row'
          data-duplicate={
            duplicates.has(index) || incomplete.has(index) || undefined
          }
          key={index}
        >
          {columns.map((column) => {
            const locked = column.readOnlyOnFixedRows && isFixed(row);
            if (locked) {
              return (
                <span
                  className={`set-rows__fixed set-rows__c--${column.width || 'md'}`}
                  key={column.key}
                >
                  {row[column.key]}
                </span>
              );
            }
            if (column.kind === 'select') {
              return (
                <span
                  className={`set-rows__c--${column.width || 'md'}`}
                  key={column.key}
                >
                  <Select
                    aria-label={`${field.label} ${column.label}`}
                    onValueChange={(next) =>
                      editCell(index, column.key, String(next))
                    }
                    value={row[column.key] ?? ''}
                  >
                    {(column.options || []).map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </Select>
                </span>
              );
            }
            return (
              <span
                className={`set-rows__c--${column.width || 'md'}`}
                key={column.key}
              >
                <Input
                  aria-label={`${field.label} ${column.label}`}
                  onChange={(event) =>
                    editCell(index, column.key, event.target.value)
                  }
                  placeholder={column.placeholder}
                  type={column.kind === 'number' ? 'number' : 'text'}
                  value={row[column.key] ?? ''}
                />
              </span>
            );
          })}
          <span className='set-rows__c--act'>
            {isFixed(row) ? null : (
              <button
                aria-label={t('page.console.settingV2.runtime.rowRemove', {
                  defaultValue: '移除这一行',
                })}
                className='set-chip__x'
                onClick={() => commit(rows.filter((_, at) => at !== index))}
                type='button'
              >
                <X aria-hidden='true' size={13} />
              </button>
            )}
          </span>
        </div>
      ))}
      <Button
        onClick={() =>
          commit([
            ...rows,
            Object.fromEntries(columns.map((column) => [column.key, ''])),
          ])
        }
        size='sm'
        variant='secondary'
      >
        <Plus aria-hidden='true' size={13} />
        {field.addLabel ||
          t('page.console.settingV2.runtime.rowAdd', { defaultValue: '添加' })}
      </Button>
      {incomplete.size ? (
        <FieldMessage error>
          {t('page.console.settingV2.runtime.rowIncomplete', {
            defaultValue: '有一行没填完整。补齐或删掉之前无法保存。',
          })}
        </FieldMessage>
      ) : null}
      {duplicates.size ? (
        <FieldMessage error>
          {t('page.console.settingV2.runtime.rowDuplicate', {
            defaultValue: '有重复的名称，保存时只会保留最后一行。',
          })}
        </FieldMessage>
      ) : null}
    </div>
  );
}

/** Settings that only read as one sentence together — a window and its two
 *  thresholds, a switch and the interval it governs. */
function GroupField({
  field,
  form,
  onDangerConfirm,
}: {
  field: SettingField;
  form: ReturnType<typeof useForm<SettingsValues>>;
  onDangerConfirm: (key: string, label: string) => void;
}) {
  return (
    <>
      {(field.parts || []).map((part) => {
        /* Zod reports against the part's own key, so a grouped row has to read
           the message there. Rendering only the synthetic group key left a
           negative interval failing validation with the Save button doing
           nothing and no field saying why. */
        const partError = form.formState.errors[formKey(part.key)]?.message as
          | string
          | undefined;
        return (
          <span className='set-group__part' key={part.key}>
            {part.lead ? (
              <span className='set-group__lead'>{part.lead}</span>
            ) : null}
            {part.kind === 'boolean' ? (
              <Switch
                aria-label={`${field.label} ${part.lead || ''}`.trim()}
                checked={Boolean(form.watch(formKey(part.key)))}
                onCheckedChange={(checked) => {
                  if (
                    field.danger &&
                    Boolean(form.getValues(formKey(part.key))) &&
                    !checked
                  ) {
                    onDangerConfirm(part.key, field.label);
                    return;
                  }
                  form.setValue(formKey(part.key), checked, {
                    shouldDirty: true,
                  });
                }}
              />
            ) : (
              <ValueInput
                ariaLabel={`${field.label} ${part.lead || part.unit || ''}`.trim()}
                form={form}
                numeric={part.kind === 'number'}
                prefix={part.prefix}
                settingKey={part.key}
                unit={part.unit}
                width={part.width || 'xs'}
              />
            )}
            {partError ? <FieldMessage error>{partError}</FieldMessage> : null}
          </span>
        );
      })}
    </>
  );
}

const EditableField = ({
  field,
  form,
  jsonHelperLabels,
  onBlockChange,
  onDangerConfirm,
}: {
  field: SettingField;
  form: ReturnType<typeof useForm<SettingsValues>>;
  jsonHelperLabels: { format: string; invalid: string };
  onBlockChange: (key: string, blocked: boolean) => void;
  onDangerConfirm: (key: string, label: string) => void;
}) => {
  const value = form.watch(formKey(field.key));
  const errorMessage = form.formState.errors[formKey(field.key)]?.message as
    | string
    | undefined;

  const toggleKey = field.toggleKey;
  const common = {
    badge: field.badge,
    danger: field.danger,
    description: field.description,
    indent: field.indent,
    label: field.label,
    toggle: toggleKey ? (
      <Switch
        aria-label={field.label}
        checked={Boolean(form.watch(formKey(toggleKey)))}
        onCheckedChange={(checked) =>
          form.setValue(formKey(toggleKey), checked, { shouldDirty: true })
        }
      />
    ) : undefined,
  };

  if (field.kind === 'boolean') {
    return (
      <SettingRow
        {...common}
        control={
          <Switch
            aria-label={field.label}
            checked={Boolean(value)}
            onCheckedChange={(checked) => {
              if (field.danger && Boolean(value) && !checked) {
                onDangerConfirm(field.key, field.label);
                return;
              }
              form.setValue(formKey(field.key), checked, {
                shouldDirty: true,
              });
            }}
          />
        }
      />
    );
  }

  if (field.kind === 'group') {
    return (
      <SettingRow
        {...common}
        control={
          <GroupField
            field={field}
            form={form}
            onDangerConfirm={onDangerConfirm}
          />
        }
      />
    );
  }

  if (field.kind === 'select') {
    return (
      <SettingRow
        {...common}
        control={
          <div className='w-[200px]'>
            <Select
              aria-label={field.label}
              value={String(value ?? '')}
              onValueChange={(nextValue) =>
                form.setValue(formKey(field.key), nextValue, {
                  shouldDirty: true,
                })
              }
            >
              {(field.options || []).map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {option.label}
                </SelectItem>
              ))}
            </Select>
          </div>
        }
      />
    );
  }

  if (field.kind === 'chips') {
    return (
      <SettingRow
        {...common}
        control={<ChipsField field={field} form={form} />}
      />
    );
  }

  if (field.kind === 'docs') {
    return (
      <SettingRow
        {...common}
        control={<DocsField field={field} form={form} />}
      />
    );
  }

  // Long-form text — mail templates, the footer, the keyword list — collapses
  // to a one-line preview with its own editor. Four of these as permanently
  // open textareas were most of the settings page's 6,900px height, and the
  // reader edits one of them a quarter.
  if (field.kind === 'textarea') {
    return (
      <SettingRow
        {...common}
        control={
          <TemplateField
            errorMessage={errorMessage}
            field={field}
            form={form}
          />
        }
      />
    );
  }

  if (field.kind === 'rows') {
    return (
      <div className='set-row set-row--stack'>
        <div className='set-row__jsonhd'>
          <Label>
            {field.label}
            {field.badge ? (
              <span className='set-row__badge'>{field.badge}</span>
            ) : null}
          </Label>
        </div>
        <RowsField field={field} form={form} onBlockChange={onBlockChange} />
        {field.description ? (
          <p className='set-row__hint set-row__hint--stack'>
            {field.description}
          </p>
        ) : null}
      </div>
    );
  }

  if (field.kind === 'json') {
    return (
      <div className='set-row set-row--stack'>
        <div className='set-row__jsonhd'>
          <Label htmlFor={field.key}>
            {field.label}
            {field.badge ? (
              <span className='set-row__badge'>{field.badge}</span>
            ) : null}
          </Label>
          <button
            className='set-row__jsonfmt'
            onClick={() => {
              const raw = String(
                form.getValues(formKey(field.key)) ?? '',
              ).trim();
              if (!raw) return;
              try {
                form.setValue(
                  formKey(field.key),
                  JSON.stringify(JSON.parse(raw), null, 2),
                  {
                    shouldDirty: true,
                  },
                );
              } catch (err) {
                const message =
                  err instanceof Error ? err.message : String(err);
                showWarning(`${jsonHelperLabels.invalid}: ${message}`);
              }
            }}
            type='button'
          >
            {jsonHelperLabels.format}
          </button>
        </div>
        <Textarea
          id={field.key}
          rows={6}
          placeholder={field.placeholder}
          {...form.register(formKey(field.key))}
        />
        {field.description ? (
          <p className='set-row__hint set-row__hint--stack'>
            {field.description}
          </p>
        ) : null}
        <FieldMessage error>{errorMessage}</FieldMessage>
      </div>
    );
  }

  return (
    <SettingRow
      {...common}
      control={
        <>
          <ValueInput
            ariaLabel={field.label}
            form={form}
            numeric={field.kind === 'number' || field.kind === 'money'}
            placeholder={field.placeholder}
            prefix={field.prefix}
            settingKey={field.key}
            unit={field.unit}
            width={field.width}
          />
          {errorMessage ? (
            <FieldMessage error>{errorMessage}</FieldMessage>
          ) : null}
        </>
      }
    />
  );
};

export const SettingsGroupPage = ({
  defaults,
  schema,
  sections,
  title,
}: SettingsGroupPageProps) => {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const leaves = useMemo(
    () => sections.flatMap((section) => section.fields.flatMap(leavesOf)),
    [sections],
  );

  const form = useForm<SettingsValues>({
    resolver: zodResolver(aliasSchema(schema) as never) as never,
    defaultValues: toFormValues(defaults),
  });
  /* Problems a field knows about that the schema cannot see — two structured
     rows sharing one name encode to one map entry, so the stored JSON is valid
     while a row is about to vanish. Save refuses while any is present. */
  const [blockers, setBlockers] = useState<Record<string, boolean>>({});
  const setBlocked = useCallback((key: string, blocked: boolean) => {
    setBlockers((current) =>
      Boolean(current[key]) === blocked
        ? current
        : { ...current, [key]: blocked },
    );
  }, []);
  const [confirmField, setConfirmField] = useState<{
    key: string;
    label: string;
  } | null>(null);

  const optionsQuery = useQuery({
    queryKey: [...queryKeys.console.detail('settings', title)],
    refetchOnWindowFocus: false,
    queryFn: async () => {
      const res = await API.get('/api/option/');
      return normalizeOptions(unwrap<unknown>(res), leaves, defaults);
    },
  });

  /* The loaded settings, once they are actually in the form.
   *
   * Counting unsaved edits against the query's data directly is a frame too
   * early: between the query resolving and this effect running there is one
   * render where the form still holds the hardcoded defaults, and every
   * default that differs from the server counted as an edit. The save bar is
   * handed that count and keeps it, so a freshly opened tab offered to save
   * settings nobody had touched. Holding the baseline in state means the count
   * is only ever compared against values the form has really been given. */
  const [baseline, setBaseline] = useState<SettingsValues | null>(null);

  useEffect(() => {
    if (!optionsQuery.data) return;
    form.reset(toFormValues(optionsQuery.data), { keepDirtyValues: true });
    setBaseline(optionsQuery.data);
  }, [form, optionsQuery.data]);

  const saveMutation = useMutation({
    mutationFn: async () => {
      const previous = (baseline || defaults) as SettingsValues;
      // getValues(key): RHF stores a dotted name as a nested path, so the zod
      // resolver output keyed by the literal string is stale for dotted keys.
      const changed = leaves.filter(
        (leaf) =>
          serializeValue(form.getValues(formKey(leaf.key))) !==
          serializeValue(previous[leaf.key]),
      );
      if (!changed.length) {
        notify.warning(t('page.console.settingV2.runtime.noChanges'));
        return [];
      }
      const submitted = changed.map((leaf) => ({
        key: leaf.key,
        kind: leaf.kind,
        value: form.getValues(formKey(leaf.key)),
        wire: wireValue(leaf, form.getValues(formKey(leaf.key))),
      }));
      /* Values before switches, one at a time.
       *
       * A switch is the thing the backend validates against its values:
       * enabling the email domain restriction is refused while the allowlist
       * is still empty. Sending the whole batch in parallel meant that adding
       * the first domain and enabling the restriction in one save came down to
       * which request the server happened to read first. */
      /* Switches that turn something off go first, values next, switches
         that turn something on last. Every intermediate state the server
         passes through is then one the operator could have saved on purpose:
         removing the last allowed domain while disabling the restriction no
         longer spends a request with the restriction on and no domains —
         which rejects every sign-up, and stays that way if the next request
         fails. */
      const isOff = (entry: (typeof submitted)[number]) =>
        entry.kind === 'boolean' && entry.value === false;
      const isOn = (entry: (typeof submitted)[number]) =>
        entry.kind === 'boolean' && entry.value !== false;
      const ordered = [
        ...submitted.filter(isOff),
        ...submitted.filter((entry) => entry.kind !== 'boolean'),
        ...submitted.filter(isOn),
      ];
      for (const entry of ordered) {
        unwrap(
          await API.put('/api/option/', {
            key: entry.key,
            value: entry.wire,
          }),
        );
      }
      return submitted;
    },
    onSuccess: (submitted) => {
      if (!submitted.length) return;
      const saved = Object.fromEntries(
        submitted.map((entry) => [entry.key, entry.value]),
      );
      for (const entry of submitted) {
        if (
          serializeValue(form.getValues(formKey(entry.key))) ===
          serializeValue(entry.value)
        )
          form.resetField(formKey(entry.key), { defaultValue: entry.value });
      }
      queryClient.setQueryData<SettingsValues>(
        [...queryKeys.console.detail('settings', title)],
        (current) => ({ ...(current || defaults), ...saved }),
      );
      notify.success(
        t('page.console.settingV2.runtime.saveSuccess', {
          count: submitted.length,
        }),
      );
    },
    onError: (error) => {
      /* A refused value names its field and the rule it broke. When this page
         shows that field, the precise sentence goes under it; the toast
         carries the general one either way. */
      const invalid = readSettingInvalid(error, t);
      if (invalid && leaves.some((leaf) => leaf.key === invalid.field)) {
        form.setError(formKey(invalid.field), {
          type: 'server',
          message: invalid.message,
        });
      }
      apiErrorHandlerWith(t('page.console.settingV2.runtime.saveFailed'))(
        error,
      );
    },
  });

  /* getValues does not subscribe, so without this the panel would not
     re-render as the reader edits and the count would stand still. */
  form.watch();

  // Only values that differ from the loaded settings are unsaved edits.
  const dirtyCount = baseline
    ? leaves.filter(
        (leaf) =>
          serializeValue(form.getValues(formKey(leaf.key))) !==
          serializeValue(baseline[leaf.key]),
      ).length
    : 0;

  const grouped = useSettingsSaveGroup({
    title,
    count: dirtyCount,
    busy: saveMutation.isPending,
    validate: async () => {
      const valid = await form.trigger();
      return valid && !Object.values(blockers).some(Boolean);
    },
    save: () => saveMutation.mutateAsync(),
    reset: () => form.reset(toFormValues(baseline || defaults)),
  });

  return (
    <>
      {!grouped && dirtyCount > 0 ? (
        <Button
          loading={saveMutation.isPending}
          onClick={form.handleSubmit(() => saveMutation.mutate())}
        >
          {t('page.console.settingV2.runtime.save')}
        </Button>
      ) : null}

      {sections.map((section) => {
        /* Edits wait for the save bar; each card says how many of them are
           its own, so the reader can see where the unsaved work is. */
        const sectionDirty = baseline
          ? section.fields
              .flatMap(leavesOf)
              .filter(
                (leaf) =>
                  serializeValue(form.getValues(formKey(leaf.key))) !==
                  serializeValue(baseline[leaf.key]),
              ).length
          : 0;
        return (
          <section
            className='set-card'
            id={`settings-section-${section.id}`}
            key={section.id}
          >
            <div className='set-card__hd'>
              <div className='set-card__copy'>
                <h2 className='set-card__t'>
                  {section.title}
                  {section.badge ? (
                    <span className='set-row__badge'>{section.badge}</span>
                  ) : null}
                </h2>
                {section.description ? (
                  <p className='set-card__d'>{section.description}</p>
                ) : null}
              </div>
              {sectionDirty ? (
                <span className='set-card__dirty' role='status'>
                  {t('page.console.settingV2.runtime.cardUnsaved', {
                    count: sectionDirty,
                    defaultValue: '有 {{count}} 处未保存',
                  })}
                </span>
              ) : null}
              {section.headerAction ? (
                <div className='set-card__act'>{section.headerAction}</div>
              ) : null}
            </div>
            {section.status}
            {section.body}
            {section.fields.length ? (
              <div>
                {section.fields.map((field) => (
                  <EditableField
                    field={field}
                    form={form}
                    jsonHelperLabels={{
                      format: t('page.console.settingV2.runtime.formatJson'),
                      invalid: t('page.console.settingV2.runtime.invalidJson'),
                    }}
                    key={field.key}
                    onBlockChange={setBlocked}
                    onDangerConfirm={(key, label) =>
                      setConfirmField({ key, label })
                    }
                  />
                ))}
              </div>
            ) : null}
          </section>
        );
      })}

      <Modal
        open={Boolean(confirmField)}
        onOpenChange={(open) => {
          if (!open) setConfirmField(null);
        }}
        title={t('page.console.settingV2.runtime.dangerConfirm')}
        description={confirmField?.label}
        body={
          <p className='text-sm leading-6 text-[var(--ui-text-secondary)]'>
            {t('page.console.settingV2.runtime.dangerConfirmHint')}
          </p>
        }
        footer={
          <>
            <Button onClick={() => setConfirmField(null)} variant='secondary'>
              {t('page.console.settingV2.runtime.cancel')}
            </Button>
            <Button
              variant='danger'
              onClick={() => {
                if (confirmField) {
                  form.setValue(formKey(confirmField.key), false, {
                    shouldDirty: true,
                  });
                }
                setConfirmField(null);
              }}
            >
              {t('page.console.settingV2.runtime.confirmDisable')}
            </Button>
          </>
        }
      />
    </>
  );
};
