/* 生成任务 › 上传的文件: what the caller uploaded through /v1/files (a
 * generated image or video is listed with its task instead). Download and
 * delete work as they did on the old 文件 tab; a delete offers undo. */
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { isAxiosError } from 'axios';
import { Download, FileText, Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { formatTaskRemaining } from './format';
import { Button } from '../../components/ui/button';
import { EmptyState } from '../../components/ui/empty-state';
import { ErrorState } from '../../components/ui/error-state';
import { PendingValue } from '../../components/ui/pending';
import { deferUndoable, useUndoPending } from '../../components/ui/undo-toast';
import { API } from '../../helpers/api';
import { showError } from '../../helpers/utils';
import { unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';

type UploadedFile = {
  bytes: number;
  created_at: number;
  expires_at?: number;
  filename: string;
  id: string;
  mime_type: string;
  purpose: string;
  status: string;
};

const formatBytes = (bytes: number) => {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${new Intl.NumberFormat(undefined, {
    maximumFractionDigits: value >= 10 || unit === 0 ? 0 : 1,
  }).format(value)} ${units[unit]}`;
};

const uploadsQueryKey = queryKeys.console.table('uploads', {});

export async function fetchUploads(limit = 10000): Promise<UploadedFile[]> {
  const response = await API.get(
    `/api/files?source=upload&limit=${limit}&order=desc`,
    { skipErrorHandler: true },
  );
  const data = (response.data as { data?: unknown })?.data;
  return (Array.isArray(data) ? data : []).map((item) => {
    const record = (item ?? {}) as Record<string, unknown>;
    return {
      bytes: Number(record.bytes || 0),
      created_at: Number(record.created_at || 0),
      expires_at: record.expires_at ? Number(record.expires_at) : undefined,
      filename: String(record.filename || record.id || ''),
      id: String(record.id || ''),
      mime_type: String(record.mime_type || ''),
      purpose: String(record.purpose || 'user_data'),
      status: String(record.status || 'processed'),
    };
  });
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

export function UploadsTable({
  hasActiveFilters = false,
  nowSec,
  onClearFilters,
  q,
}: {
  hasActiveFilters?: boolean;
  nowSec: number;
  onClearFilters?: () => void;
  q: string;
}) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const undoPending = useUndoPending();
  const query = useQuery({
    queryKey: uploadsQueryKey,
    queryFn: () => fetchUploads(),
  });
  const needle = q.trim().toLowerCase();
  const files = (query.data ?? []).filter(
    (file) =>
      !needle ||
      file.filename.toLowerCase().includes(needle) ||
      file.id.toLowerCase().includes(needle),
  );

  const remove = (file: UploadedFile) => {
    const path = `/api/files/${encodeURIComponent(file.id)}`;
    deferUndoable({
      keys: [`file:${file.id}`],
      message: t('undo.file.deleted', { name: file.filename }),
      restored: t('undo.file.restored', { name: file.filename }),
      failed: t('undo.file.failed', { name: file.filename }),
      commit: async () =>
        unwrap(await API.delete(path, { skipErrorHandler: true })),
      keepalive: { method: 'DELETE', path },
      onCommitted: () =>
        void queryClient.invalidateQueries({ queryKey: uploadsQueryKey }),
    });
  };

  const download = useMutation<void, Error, UploadedFile>({
    mutationFn: async (file) => {
      const response = await API.get(`/api/files/${file.id}/content`, {
        responseType: 'blob',
        skipErrorHandler: true,
      });
      const url = URL.createObjectURL(response.data as Blob);
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

  const purposeLabel = (file: UploadedFile) =>
    file.purpose === 'vision'
      ? t('page.console.files.purpose.vision')
      : t('page.console.files.purpose.userData');

  if (!query.isPending && query.isError) {
    return (
      <div className='logv2-card'>
        <ErrorState
          action={
            <Button
              onClick={() => void query.refetch()}
              size='sm'
              type='button'
              variant='secondary'
            >
              {t('page.console.log.session.retry', { defaultValue: '重试' })}
            </Button>
          }
          error={query.error}
          title={t('page.console.tasks.uploads.loadFailed')}
        />
      </div>
    );
  }

  if (!query.isPending && files.length === 0) {
    return (
      <div className='logv2-card'>
        <EmptyState
          action={
            (needle || hasActiveFilters) && onClearFilters ? (
              <Button
                onClick={onClearFilters}
                size='sm'
                type='button'
                variant='secondary'
              >
                {t('page.console.log.filter.clearAll', {
                  defaultValue: '清除筛选',
                })}
              </Button>
            ) : undefined
          }
          description={t('page.console.tasks.uploads.retention')}
          icon={<FileText size={22} />}
          title={t('page.console.tasks.uploads.empty')}
        />
      </div>
    );
  }

  return (
    <div className='logv2-card'>
      <div className='logv2-scroll'>
        <table className='logv2-table uploads-table'>
          <thead>
            <tr>
              <th className='uploads-table__content' scope='col'>
                {t('page.console.tasks.uploads.col.content')}
              </th>
              <th scope='col'>{t('page.console.tasks.uploads.col.source')}</th>
              <th scope='col'>{t('page.console.tasks.uploads.col.size')}</th>
              <th scope='col'>{t('page.console.tasks.col.status')}</th>
              <th scope='col'>{t('page.console.tasks.col.keep')}</th>
              <th className='uploads-table__actions' scope='col'>
                {t('page.console.tables.actions')}
              </th>
            </tr>
          </thead>
          <tbody>
            {query.isPending
              ? Array.from({ length: 4 }, (_, index) => (
                  <tr aria-hidden='true' key={index}>
                    <td>
                      <PendingValue chars={18} />
                    </td>
                    <td>
                      <PendingValue chars={6} />
                    </td>
                    <td>
                      <PendingValue chars={6} />
                    </td>
                    <td>
                      <PendingValue chars={6} />
                    </td>
                    <td>
                      <PendingValue chars={8} />
                    </td>
                    <td className='uploads-table__actions'>
                      <PendingValue chars={4} />
                    </td>
                  </tr>
                ))
              : null}
            {files.map((file) => {
              const deleting = undoPending.has(`file:${file.id}`);
              return (
                <tr className='uploads-row' key={file.id}>
                  <td>
                    <div className='uploads-file'>
                      <span aria-hidden='true' className='uploads-file__icon'>
                        <FileText size={18} />
                      </span>
                      <span className='uploads-file__text'>
                        <span
                          className='uploads-file__name'
                          title={file.filename}
                        >
                          {file.filename}
                        </span>
                        <span className='uploads-file__sub'>
                          {purposeLabel(file)} · {file.id}
                        </span>
                      </span>
                    </div>
                  </td>
                  <td>
                    <span className='uploads-tag'>
                      {t('page.console.tasks.uploads.uploaded')}
                    </span>
                  </td>
                  <td className='uploads-mono'>{formatBytes(file.bytes)}</td>
                  <td>
                    {deleting
                      ? t('undo.deleting')
                      : t(`page.console.files.status.${file.status}`, {
                          defaultValue: file.status,
                        })}
                  </td>
                  <td className='logv2-soft tasks-nowrap'>
                    {file.expires_at
                      ? formatTaskRemaining(file.expires_at, nowSec, t)
                      : '—'}
                  </td>
                  <td className='uploads-table__actions'>
                    <button
                      aria-label={t('page.console.files.action.download')}
                      className='uploads-icon'
                      disabled={deleting || download.isPending}
                      onClick={() => download.mutate(file)}
                      type='button'
                    >
                      <Download aria-hidden='true' size={16} />
                    </button>
                    <button
                      aria-label={t('page.console.files.action.delete')}
                      className='uploads-icon'
                      disabled={deleting}
                      onClick={() => remove(file)}
                      type='button'
                    >
                      <Trash2 aria-hidden='true' size={16} />
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}
