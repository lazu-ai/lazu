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
import {
  Button,
  DataTableShell,
  EmptyState,
  ErrorState,
  IconButton,
  Modal,
  Select,
  SelectItem,
  Tag,
} from '../../components/ui';
import {
  ColumnVisibilityMenu,
  ConsoleTableFrame,
  MetricPill,
  TextStack,
  formatUnixTime,
  truncateMiddle,
  useColumnVisibility,
} from '../../components/ui/table-tools';
import { API } from '../../helpers/api';
import { showError, showSuccess } from '../../helpers/utils';
import { apiErrorHandler } from '../../lib/api-error';
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
  const [deleteTarget, setDeleteTarget] = useState<FileRecord | null>(null);

  const filesQuery = useQuery<FileRecord[], Error>({
    queryKey: queryKeys.console.table('files', { purpose }),
    queryFn: () => fetchFiles(purpose),
    placeholderData: keepPreviousData,
  });

  const invalidate = () =>
    queryClient.invalidateQueries({
      queryKey: queryKeys.console.root,
    });

  const deleteMutation = useMutation<void, Error, FileRecord>({
    mutationFn: async (file) => {
      await API.delete(`/api/files/${file.id}`, { skipErrorHandler: true });
    },
    onSuccess: () => {
      showSuccess(t('page.console.files.toast.deleteSuccess'));
      setDeleteTarget(null);
      invalidate();
    },
    onError: apiErrorHandler,
  });

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
          render: (_value: unknown, record: FileRecord) => (
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
          render: (_value: unknown, record: FileRecord) => (
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
                onClick={() => setDeleteTarget(record)}
              >
                <Trash2 size={14} />
              </IconButton>
            </div>
          ),
        },
      ].filter((column) =>
        columnVisibility.visibleColumns.some((meta) => meta.id === column.key),
      ),
    [columnVisibility.visibleColumns, downloadMutation, t],
  );

  return (
    <>
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

      <Modal
        open={Boolean(deleteTarget)}
        onOpenChange={(open) => {
          if (!open) {
            setDeleteTarget(null);
          }
        }}
        title={t('page.console.files.delete.title')}
        description={
          deleteTarget
            ? t('page.console.files.delete.description', {
                filename: deleteTarget.filename,
              })
            : undefined
        }
        footer={
          <div className='flex justify-end gap-2'>
            <Button variant='ghost' onClick={() => setDeleteTarget(null)}>
              {t('legacy.i18n.cancel.4d0b4688c7')}
            </Button>
            <Button
              variant='danger'
              loading={deleteMutation.isPending}
              disabled={!deleteTarget}
              onClick={() => {
                if (deleteTarget) {
                  deleteMutation.mutate(deleteTarget);
                }
              }}
            >
              <Trash2 size={15} />
              {t('page.console.files.action.delete')}
            </Button>
          </div>
        }
      />
    </>
  );
};

export default Files;
