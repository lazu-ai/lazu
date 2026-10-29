import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { isAxiosError } from 'axios';
import { Download, FileText, Trash2 } from 'lucide-react';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { DataTableShell } from '../../components/ui/data-table-shell';
import { EmptyState } from '../../components/ui/empty-state';
import { ErrorState } from '../../components/ui/error-state';
import { IconButton } from '../../components/ui/icon-button';
import { Select, SelectItem } from '../../components/ui/select';
import {
  ColumnVisibilityMenu,
  ConsoleTableFrame,
  MetricPill,
  TextStack,
  formatUnixTime,
  truncateMiddle,
  useColumnVisibility,
} from '../../components/ui/table-tools';
import { Tag } from '../../components/ui/tag';
import { deferUndoable, useUndoPending } from '../../components/ui/undo-toast';
import { API } from '../../helpers/api';
import { showError } from '../../helpers/utils';
import { unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';

type FileRecord = Record<string, unknown> & {
  bytes: number;
  created_at: number;
  filename: string;
  id: string;
  object: 'file';
  purpose: 'user_data' | 'vision' | string;
  status?: string;
};

type FileListResponse = {
  data?: FileRecord[];
  object?: string;
};

const PURPOSE_ALL = '__all__';

const normalizeFile = (value: unknown): FileRecord | null => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return null;
  }
  const record = value as Record<string, unknown>;
  const id = String(record.id || '');
  if (!id) {
    return null;
  }
  return {
    ...record,
    bytes: Number(record.bytes || 0),
    created_at: Number(record.created_at || 0),
    filename: String(record.filename || id),
    id,
    object: 'file',
    purpose: String(record.purpose || 'user_data'),
    status: record.status ? String(record.status) : 'processed',
  };
};

const formatBytes = (bytes: number) => {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return '0 B';
  }
  const units = ['B', 'KB', 'MB', 'GB'];
  let value = bytes;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  return `${new Intl.NumberFormat(undefined, {
    maximumFractionDigits: value >= 10 || unitIndex === 0 ? 0 : 1,
  }).format(value)} ${units[unitIndex]}`;
};

async function fetchFiles(purpose: string): Promise<FileRecord[]> {
  const params = new URLSearchParams({ limit: '10000', order: 'desc' });
  if (purpose !== PURPOSE_ALL) {
    params.set('purpose', purpose);
  }
  // A failed list renders inline (ErrorState), not as a toast.
  const response = await API.get(`/api/files?${params.toString()}`, {
    skipErrorHandler: true,
  });
  const payload = response.data as FileListResponse;
  return Array.isArray(payload.data)
    ? payload.data
        .map(normalizeFile)
        .filter((item): item is FileRecord => Boolean(item))
    : [];
}

/* A download asks for a Blob, so axios hands a failure's JSON error body
 * over as a Blob too. Read it back so the failure keeps its code. */
const withJsonErrorBody = async (error: unknown): Promise<unknown> => {
  const response = isAxiosError(error) ? error.response : undefined;
  if (!response || !(response.data instanceof Blob)) return error;
  try {
    response.data = JSON.parse(await response.data.text());
  } catch {
    // Not JSON: the status alone describes the failure.
  }
  return error;
};

const Files = () => {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [purpose, setPurpose] = useState(PURPOSE_ALL);

  const filesQuery = useQuery<FileRecord[], Error>({
    queryKey: queryKeys.console.table('files', { purpose }),
    queryFn: () => fetchFiles(purpose),
    placeholderData: keepPreviousData,
  });

  const invalidate = () =>
    queryClient.invalidateQueries({
      queryKey: queryKeys.console.root,
    });

  /* 删除 runs at once and offers a 10-second undo instead of a dialog;
     the row reads 删除中 until the request actually goes out. */
  const undoPending = useUndoPending();
  const deleteNow = (file: FileRecord) => {
    const name = file.filename;
    const path = `/api/files/${encodeURIComponent(file.id)}`;
    deferUndoable({
      keys: [`file:${file.id}`],
      message: t('undo.file.deleted', {
        defaultValue: '已删除 {{name}}',
        name,
      }),
      restored: t('undo.file.restored', {
        defaultValue: '已恢复 {{name}}',
        name,
      }),
      failed: t('undo.file.failed', {
        defaultValue: '没删成，{{name}} 已放回',
        name,
      }),
      // The undo toast reports a failure itself, with 重试.
      commit: async () =>
        unwrap(await API.delete(path, { skipErrorHandler: true })),
      keepalive: { method: 'DELETE', path },
      onCommitted: () => void invalidate(),
    });
  };

  const downloadMutation = useMutation<void, Error, FileRecord>({
    mutationFn: async (file) => {
      const response = await API.get(`/api/files/${file.id}/content`, {
        responseType: 'blob',
        skipErrorHandler: true,
      });
      const blob = response.data as Blob;
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement('a');
      anchor.href = url;
      anchor.download = file.filename;
      anchor.click();
      URL.revokeObjectURL(url);
    },
    onError: async (error) =>
      showError(
        await withJsonErrorBody(error),
        t('page.console.files.toast.downloadFailed'),
      ),
  });

  const files = filesQuery.data || [];
  const totalBytes = files.reduce((sum, file) => sum + file.bytes, 0);

  const columnMeta = useMemo(
    () => [
      { id: 'filename', label: t('page.console.files.column.filename') },
      { id: 'purpose', label: t('page.console.files.column.purpose') },
      { id: 'bytes', label: t('page.console.files.column.bytes') },
      { id: 'status', label: t('page.console.files.column.status') },
      { id: 'created', label: t('page.console.files.column.created') },
      { id: 'actions', label: t('page.console.tables.actions') },
    ],
    [t],
  );
  const columnVisibility = useColumnVisibility(
    'page-console-files-columns',
    columnMeta,
  );

  const columns = useMemo(
    () =>
      [
        {
          id: 'filename',
          key: 'filename',
          title: t('page.console.files.column.filename'),
          render: (_value: unknown, record: FileRecord) => (
            <TextStack
              title={record.filename}
              description={truncateMiddle(record.id, 18, 8)}
              eyebrow={
                <span className='inline-flex items-center gap-1'>
                  <FileText size={13} />
                  {record.purpose === 'vision'
                    ? t('page.console.files.purpose.vision')
                    : t('page.console.files.purpose.userData')}
                </span>
              }
            />
          ),
        },
        {
          id: 'purpose',
          key: 'purpose',
          title: t('page.console.files.column.purpose'),
          render: (_value: unknown, record: FileRecord) => (
            <Tag>
              {record.purpose === 'vision'
                ? t('page.console.files.purpose.vision')
                : t('page.console.files.purpose.userData')}
            </Tag>
          ),
        },
        {
          id: 'bytes',
          key: 'bytes',
          title: t('page.console.files.column.bytes'),
          render: (_value: unknown, record: FileRecord) =>
            formatBytes(record.bytes),
        },
        {
          id: 'status',
          key: 'status',
          title: t('page.console.files.column.status'),
          render: (_value: unknown, record: FileRecord) =>
            undoPending.has(`file:${record.id}`) ? (
              <Badge variant='neutral'>
                {t('undo.deleting', { defaultValue: '删除中' })}
              </Badge>
            ) : (
              <Tag>
                {t(`page.console.files.status.${record.status}`, {
                  defaultValue: String(record.status || '-'),
                })}
              </Tag>
            ),
        },
        {
          id: 'created',
          key: 'created',
          title: t('page.console.files.column.created'),
          render: (_value: unknown, record: FileRecord) =>
            formatUnixTime(record.created_at),
        },
        {
          id: 'actions',
          key: 'actions',
          title: t('page.console.tables.actions'),
          render: (_value: unknown, record: FileRecord) =>
            undoPending.has(`file:${record.id}`) ? null : (
              <div className='flex items-center gap-1'>
                <IconButton
                  aria-label={t('page.console.files.action.download')}
                  size='sm'
                  variant='ghost'
                  disabled={downloadMutation.isPending}
                  onClick={() => downloadMutation.mutate(record)}
                >
                  <Download size={14} />
                </IconButton>
                <IconButton
                  aria-label={t('page.console.files.action.delete')}
                  size='sm'
                  variant='danger'
                  onClick={() => deleteNow(record)}
                >
                  <Trash2 size={14} />
                </IconButton>
              </div>
            ),
        },
      ].filter((column) =>
        columnVisibility.visibleColumns.some((meta) => meta.id === column.key),
      ),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- deleteNow only reads `t` and `invalidate`
    [columnVisibility.visibleColumns, downloadMutation, t, undoPending],
  );

  return (
    <ConsoleTableFrame
      title={t('page.console.files.title')}
      description={t('page.console.files.description')}
      t={t}
      isFetching={filesQuery.isFetching}
      onRefresh={() => filesQuery.refetch()}
      stats={
        <div className='flex flex-wrap gap-2'>
          <MetricPill
            label={t('page.console.files.metric.total')}
            value={files.length}
          />
          <MetricPill
            label={t('page.console.files.metric.bytes')}
            value={formatBytes(totalBytes)}
          />
        </div>
      }
      columns={
        <ColumnVisibilityMenu
          columns={columnMeta}
          value={columnVisibility.value}
          onChange={columnVisibility.setColumnVisible}
          t={t}
        />
      }
      filters={
        <label className='min-w-[12rem] text-xs font-medium text-[var(--ui-text-secondary)]'>
          <span className='mb-1 block'>
            {t('page.console.files.filter.purpose')}
          </span>
          <Select value={purpose} onValueChange={setPurpose}>
            <SelectItem value={PURPOSE_ALL}>
              {t('page.console.files.purpose.all')}
            </SelectItem>
            <SelectItem value='user_data'>
              {t('page.console.files.purpose.userData')}
            </SelectItem>
            <SelectItem value='vision'>
              {t('page.console.files.purpose.vision')}
            </SelectItem>
          </Select>
        </label>
      }
    >
      <DataTableShell
        rowKey='id'
        columns={columns}
        data={files}
        loading={filesQuery.isLoading}
        rowClassName={(record) =>
          undoPending.has(`file:${record.id}`) ? 'is-undo-pending' : undefined
        }
        empty={
          filesQuery.isError ? (
            <ErrorState
              error={filesQuery.error}
              action={
                <Button onClick={() => filesQuery.refetch()}>
                  {t('overview.retry')}
                </Button>
              }
            />
          ) : (
            <EmptyState
              title={t('page.console.files.emptyTitle')}
              description={t('page.console.files.emptyDescription')}
            />
          )
        }
        mobileCard={{
          titleIds: ['filename'],
          statusIds: ['status'],
          hideIds: ['id'],
        }}
      />
    </ConsoleTableFrame>
  );
};

export default Files;
