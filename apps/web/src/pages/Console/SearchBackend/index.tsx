import { Plus } from 'lucide-react';
import { useState } from 'react';
import {
  Badge,
  Button,
  DataCard,
  Field,
  Input,
  InlineSecret,
  Label,
  Modal,
  MultiSelect,
  ConsolePage,
  ErrorState,
  Select,
  Switch,
} from '../../../components/ui';
import {
  SEARCH_CHANNEL_OPTIONS,
  type SearchCapability,
} from '../../../constants/search.constants';
import { API } from '../../../helpers/api';
import {
  useSearchBackendsAdmin,
  type SearchBackendRecord,
} from '../../../hooks/search/useSearchBackendsAdmin';
import { unwrap } from '../../../lib/api-error';

import './search-services.css';

const CAPABILITY_VALUES: SearchCapability[] = [
  'web_search',
  'web_fetch',
  'answer',
];

const emptyDraft = (): SearchBackendRecord => {
  const first = SEARCH_CHANNEL_OPTIONS[0];
  return {
    name: '',
    provider: first.provider,
    type: first.value,
    api_key: '',
    base_url: '',
    capabilities: [...first.capabilities],
    max_results: 10,
    timeout_ms: 8000,
    priority: 10,
    status: 1,
    search_price: 0,
    fetch_price: 0,
    use_provider_cost: false,
  };
};

const SearchBackendPage = () => {
  const admin = useSearchBackendsAdmin();
  const { t } = admin;
  const [editorOpen, setEditorOpen] = useState(false);
  const [draft, setDraft] = useState<SearchBackendRecord>(emptyDraft);

  const [deleteTarget, setDeleteTarget] = useState<SearchBackendRecord | null>(
    null,
  );

  const isEdit = draft.id !== undefined && draft.id !== null;

  const patch = (next: Partial<SearchBackendRecord>) =>
    setDraft((prev) => ({ ...prev, ...next }));

  const openCreate = () => {
    setDraft(emptyDraft());
    setEditorOpen(true);
  };

  const openEdit = (record: SearchBackendRecord) => {
    setDraft({ ...record, api_key: '' });
    setEditorOpen(true);
  };

  const onProviderChange = (provider: string) => {
    const option = SEARCH_CHANNEL_OPTIONS.find((o) => o.provider === provider);
    if (!option) return;
    patch({
      provider,
      type: option.value,
      capabilities: [...option.capabilities],
    });
  };

  const onSave = () => {
    admin.saveMutation.mutate(draft, {
      onSuccess: () => setEditorOpen(false),
    });
  };

  const providerOptions = SEARCH_CHANNEL_OPTIONS.map((o) => ({
    label: o.label,
    value: o.provider,
  }));

  const capabilityLabel = (value: string) =>
    t(`search.capability.${value}`, { defaultValue: value });
  const capabilityOptions = CAPABILITY_VALUES.map((value) => ({
    label: capabilityLabel(value),
    value,
  }));

  return (
    <ConsolePage
      appearance='review'
      className='search-services-page'
      title={t('page.console.searchBackend.title')}
      actions={
        <Button variant='primary' onClick={openCreate}>
          <Plus size={15} />
          {t('page.console.searchBackend.add')}
        </Button>
      }
    >
      <DataCard
        footer={
          admin.backends.length ? (
            <span>
              {t('search.lastBusinessUse', {
                defaultValue:
                  '最近使用指供应商收到的最近一次真实业务请求；测试不计入。',
              })}
            </span>
          ) : undefined
        }
      >
        {admin.error ? (
          <ErrorState
            error={admin.error}
            action={
              <Button onClick={admin.refresh}>{t('overview.retry')}</Button>
            }
          />
        ) : admin.backends.length === 0 ? (
          <div
            style={{
              padding: '28px 8px',
              textAlign: 'center',
              color: 'var(--ui-text-tertiary)',
              fontSize: 13,
            }}
          >
            {admin.isLoading
              ? t('page.console.searchBackend.loading')
              : t('page.console.searchBackend.empty')}
          </div>
        ) : (
          <div className='lazu-table-wrap'>
            <table className='lazu-table lazu-table--compact search-services-table'>
              <thead>
                <tr>
                  <th>{t('page.console.searchBackend.column.name')}</th>
                  <th>{t('page.console.searchBackend.column.provider')}</th>
                  <th>API Key</th>
                  <th>{t('secret.lastUsed', { defaultValue: '最近使用' })}</th>
                  <th>{t('page.console.searchBackend.column.capabilities')}</th>
                  <th>{t('page.console.searchBackend.column.priority')}</th>
                  <th>{t('page.console.searchBackend.column.status')}</th>
                  <th className='text-right'>
                    {t('page.console.tables.actions')}
                  </th>
                </tr>
              </thead>
              <tbody>
                {admin.backends.map((record) => {
                  const enabled = record.status === 1;
                  return (
                    <tr key={record.id ?? record.name}>
                      <td style={{ fontWeight: 600 }}>{record.name || '-'}</td>

                      <td>{record.provider || '-'}</td>
                      <td>
                        <InlineSecret
                          identity={String(record.id)}
                          masked={record.masked_key || '—'}
                          reveal={async () => {
                            // A refused reveal is toasted with its reason by
                            // the interceptor; InlineSecret marks the field.
                            const data = unwrap<{ key?: unknown } | null>(
                              await API.post(
                                `/api/search/backends/${record.id}/reveal`,
                              ),
                            );
                            if (typeof data?.key !== 'string') {
                              throw new Error(
                                t('secret.readFailed', {
                                  defaultValue: '无法读取，请重试',
                                }),
                              );
                            }
                            return data.key;
                          }}
                        />
                      </td>
                      <td>
                        {record.last_used_at
                          ? new Date(
                              record.last_used_at * 1000,
                            ).toLocaleString()
                          : t('secret.neverUsed', {
                              defaultValue: '暂无使用记录',
                            })}
                      </td>
                      <td>
                        <span
                          style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}
                        >
                          {(record.capabilities || []).map((cap) => (
                            <span key={cap}>{capabilityLabel(cap)}</span>
                          ))}
                        </span>
                      </td>
                      <td className='font-mono'>{record.priority ?? '-'}</td>
                      <td>
                        <Badge variant={enabled ? 'success' : 'neutral'}>
                          {enabled
                            ? t('page.console.tables.enabled')
                            : t('page.console.tables.disabled')}
                        </Badge>
                      </td>
                      <td>
                        <div
                          style={{
                            display: 'flex',
                            gap: 4,
                            justifyContent: 'flex-start',
                          }}
                        >
                          <Button
                            size='xs'
                            variant='ghost'
                            onClick={() => openEdit(record)}
                          >
                            {t('page.console.tables.edit', {
                              defaultValue: '编辑',
                            })}
                          </Button>
                          <Button
                            size='xs'
                            variant='ghost-danger'
                            onClick={() => setDeleteTarget(record)}
                          >
                            {t('page.console.tables.delete')}
                          </Button>
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </DataCard>

      <Modal
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
        busy={admin.deleteMutation.isPending}
        title={t('page.console.tables.delete')}
        body={
          <p>
            {t('search.deleteConfirm', {
              defaultValue: '删除搜索服务 {{name}}？后续请求将不再使用此配置。',
              name: deleteTarget?.name,
            })}
          </p>
        }
        footer={
          <>
            <Button
              disabled={admin.deleteMutation.isPending}
              variant='secondary'
              onClick={() => setDeleteTarget(null)}
            >
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              variant='danger'
              disabled={admin.deleteMutation.isPending}
              onClick={() => {
                if (deleteTarget?.id)
                  admin.deleteMutation.mutate(deleteTarget.id, {
                    onSuccess: () => setDeleteTarget(null),
                  });
              }}
            >
              {t('page.console.tables.delete')}
            </Button>
          </>
        }
      />
      <Modal
        busy={admin.saveMutation.isPending}
        open={editorOpen}
        onOpenChange={setEditorOpen}
        title={
          isEdit
            ? t('page.console.searchBackend.editor.titleEdit')
            : t('page.console.searchBackend.editor.titleAdd')
        }
        body={
          <div className='search-editor-grid'>
            <Field>
              <Label htmlFor='sb-name'>
                {t('page.console.searchBackend.editor.name')}
              </Label>
              <Input
                id='sb-name'
                value={draft.name || ''}
                onChange={(e) => patch({ name: e.target.value })}
                placeholder='tavily-default'
              />
            </Field>
            <Field>
              <Label>{t('page.console.searchBackend.editor.provider')}</Label>
              <Select
                value={draft.provider}
                onValueChange={onProviderChange}
                options={providerOptions}
              />
            </Field>
            <Field>
              <Label htmlFor='sb-key'>
                {t('page.console.searchBackend.editor.apiKey')}
              </Label>
              <Input
                id='sb-key'
                type='password'
                value={draft.api_key || ''}
                onChange={(e) => patch({ api_key: e.target.value })}
                placeholder={
                  isEdit
                    ? t('page.console.searchBackend.editor.apiKeyKeep')
                    : 'env/TAVILY_API_KEY'
                }
              />
            </Field>
            <Field>
              <Label htmlFor='sb-url'>
                {t('page.console.searchBackend.editor.baseUrl')}
              </Label>
              <Input
                id='sb-url'
                value={draft.base_url || ''}
                onChange={(e) => patch({ base_url: e.target.value })}
                placeholder={t('page.console.searchBackend.editor.optional')}
              />
            </Field>
            <Field>
              <Label>
                {t('page.console.searchBackend.editor.capabilities')}
              </Label>
              <MultiSelect
                options={capabilityOptions}
                value={draft.capabilities || []}
                onValueChange={(value) =>
                  patch({ capabilities: value as SearchCapability[] })
                }
              />
            </Field>
            <div className='search-editor-pair'>
              <Field>
                <Label htmlFor='sb-max'>
                  {t('page.console.searchBackend.editor.maxResults')}
                </Label>
                <Input
                  id='sb-max'
                  type='number'
                  value={draft.max_results ?? 10}
                  onChange={(e) =>
                    patch({ max_results: Number(e.target.value) || 0 })
                  }
                />
              </Field>
              <Field>
                <Label htmlFor='sb-timeout'>
                  {t('page.console.searchBackend.editor.timeout')}
                </Label>
                <Input
                  id='sb-timeout'
                  type='number'
                  value={draft.timeout_ms ?? 8000}
                  onChange={(e) =>
                    patch({ timeout_ms: Number(e.target.value) || 0 })
                  }
                />
              </Field>
            </div>
            <Field>
              <Label htmlFor='sb-priority'>
                {t('page.console.searchBackend.editor.priority')}
              </Label>
              <Input
                id='sb-priority'
                type='number'
                value={draft.priority ?? 10}
                onChange={(e) =>
                  patch({ priority: Number(e.target.value) || 0 })
                }
              />
            </Field>
            <div className='search-editor-pair'>
              <Field>
                <Label htmlFor='sb-search-price'>
                  {t('page.console.searchBackend.editor.searchPrice')}
                </Label>
                <Input
                  id='sb-search-price'
                  type='number'
                  step='0.0001'
                  value={draft.search_price ?? 0}
                  onChange={(e) =>
                    patch({ search_price: Number(e.target.value) || 0 })
                  }
                />
              </Field>
              <Field>
                <Label htmlFor='sb-fetch-price'>
                  {t('page.console.searchBackend.editor.fetchPrice')}
                </Label>
                <Input
                  id='sb-fetch-price'
                  type='number'
                  step='0.0001'
                  value={draft.fetch_price ?? 0}
                  onChange={(e) =>
                    patch({ fetch_price: Number(e.target.value) || 0 })
                  }
                />
              </Field>
            </div>

            <Switch
              checked={Boolean(draft.use_provider_cost)}
              onCheckedChange={(checked) =>
                patch({ use_provider_cost: checked })
              }
            >
              {t('page.console.searchBackend.editor.useProviderCost')}
            </Switch>
            <Switch
              checked={draft.status === 1}
              onCheckedChange={(checked) => patch({ status: checked ? 1 : 0 })}
            >
              {t('page.console.searchBackend.editor.enabled')}
            </Switch>
          </div>
        }
        footer={
          <div
            style={{ display: 'flex', justifyContent: 'flex-start', gap: 8 }}
          >
            <Button variant='ghost' onClick={() => setEditorOpen(false)}>
              {t('page.console.searchBackend.editor.cancel')}
            </Button>
            <Button
              variant='primary'
              onClick={onSave}
              loading={admin.saveMutation.isPending}
              disabled={!draft.name?.trim()}
            >
              {t('page.console.searchBackend.editor.save')}
            </Button>
          </div>
        }
      />
    </ConsolePage>
  );
};

export default SearchBackendPage;
