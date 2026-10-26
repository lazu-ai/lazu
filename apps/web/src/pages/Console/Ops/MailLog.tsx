/* 运营 › 邮件: every mail Lazu sent, newest first, filtered by
 * kind, status and reader. Filters live in the URL so a row's detail page can
 * go back to the same list. */
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useSearchParams } from 'react-router-dom';
import { useDebounce } from 'use-debounce';
import { OpsNav } from '.';
import { MAIL_PAGE_SIZE, mailApi, mailKeys } from './mailApi';
import MailDigestBar from './MailDigestBar';
import {
  MAIL_KINDS,
  MAIL_STATUSES,
  MailStatus,
  formatMailTime,
  kindLabel,
  statusLabel,
} from './mailShared';
import { Button } from '../../../components/ui/button';
import ConsolePage from '../../../components/ui/console-page';
import { ErrorState } from '../../../components/ui/error-state';
import { Input } from '../../../components/ui/input';
import { Pagination } from '../../../components/ui/pagination';
import { PendingBlock } from '../../../components/ui/pending';
import { Select } from '../../../components/ui/select';

const ROW_HEIGHT = 49;

export default function MailLog() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const kind = params.get('kind') || '';
  const status = params.get('status') || '';
  const period = params.get('period') || '';
  const q = params.get('q') || '';
  const page = Math.max(1, Number(params.get('p')) || 1);
  const [keyword, setKeyword] = useState(q);
  const [debounced] = useDebounce(keyword.trim(), 300);

  const update = (next: Record<string, string>, keepPage = false) => {
    setParams(
      (current) => {
        const merged = new URLSearchParams(current);
        Object.entries(next).forEach(([key, value]) => {
          if (value) merged.set(key, value);
          else merged.delete(key);
        });
        if (!keepPage) merged.delete('p');
        return merged;
      },
      { replace: true },
    );
  };

  useEffect(() => {
    if (debounced !== q) update({ q: debounced });
    // Only the debounced keyword drives the URL; q follows it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debounced]);

  const query = {
    kind,
    status,
    q,
    period,
    p: page,
    page_size: MAIL_PAGE_SIZE,
  };
  const list = useQuery({
    queryKey: mailKeys.list(query),
    queryFn: () => mailApi.list(query),
    placeholderData: keepPreviousData,
  });
  const items = list.data?.items ?? [];
  const filtered = Boolean(kind || status || q || period);

  return (
    <ConsolePage
      appearance='review'
      className='ops-page'
      tabs={<OpsNav />}
      title={t('page.console.ops.title', { defaultValue: '内容' })}
    >
      <MailDigestBar />
      <div className='ops-mail-tools'>
        <div
          aria-label={t('page.console.ops.mail.col.kind', {
            defaultValue: '类型',
          })}
          className='ops-mail-kinds'
          role='group'
        >
          {['', ...MAIL_KINDS].map((value) => (
            <Button
              aria-pressed={kind === value}
              key={value || 'all'}
              onClick={() => update({ kind: value })}
              size='sm'
              variant={kind === value ? 'soft' : 'ghost'}
            >
              {value
                ? kindLabel(t, value)
                : t('page.console.ops.filter.all', { defaultValue: '全部' })}
            </Button>
          ))}
        </div>
        <div className='ops-blog-tools'>
          <Select
            aria-label={t('page.console.ops.mail.col.status', {
              defaultValue: '状态',
            })}
            onValueChange={(value) =>
              update({ status: value === 'all' ? '' : value })
            }
            options={[
              {
                value: 'all',
                label: `${t('page.console.ops.mail.col.status', { defaultValue: '状态' })}：${t('page.console.ops.filter.all', { defaultValue: '全部' })}`,
              },
              ...MAIL_STATUSES.map((value) => ({
                value,
                label: statusLabel(t, value),
              })),
            ]}
            value={status || 'all'}
          />
          <Input
            aria-label={t('page.console.ops.mail.search', {
              defaultValue: '收件人邮箱或用户 ID',
            })}
            className='ops-blog-tools__search'
            onChange={(event) => setKeyword(event.target.value)}
            placeholder={t('page.console.ops.mail.search', {
              defaultValue: '收件人邮箱或用户 ID',
            })}
            type='search'
            value={keyword}
          />
          {period ? (
            <Button
              onClick={() => update({ period: '' })}
              size='sm'
              variant='soft'
            >
              {t('page.console.ops.mail.periodFilter', {
                defaultValue: '期次 {{period}} ×',
                period,
              })}
            </Button>
          ) : null}
        </div>
      </div>

      {list.isError ? (
        <ErrorState
          action={
            <Button onClick={() => void list.refetch()} variant='secondary'>
              {t('common.retry', { defaultValue: '重试' })}
            </Button>
          }
        />
      ) : (
        <div className='ops-panel ops-table ops-mail-table' role='table'>
          <div className='ops-table__row ops-table__row--head' role='row'>
            <span role='columnheader'>
              {t('page.console.ops.mail.col.time', { defaultValue: '时间' })}
            </span>
            <span role='columnheader'>
              {t('page.console.ops.mail.col.kind', { defaultValue: '类型' })}
            </span>
            <span role='columnheader'>
              {t('page.console.ops.mail.col.receiver', {
                defaultValue: '收件人',
              })}
            </span>
            <span role='columnheader'>
              {t('page.console.ops.mail.col.subject', {
                defaultValue: '标题',
              })}
            </span>
            <span role='columnheader'>
              {t('page.console.ops.mail.col.period', { defaultValue: '期次' })}
            </span>
            <span role='columnheader'>
              {t('page.console.ops.mail.col.status', { defaultValue: '状态' })}
            </span>
          </div>
          {list.isPending ? (
            <PendingBlock height={MAIL_PAGE_SIZE * ROW_HEIGHT} />
          ) : items.length === 0 ? (
            <p className='ops-table__empty'>
              {filtered
                ? t('page.console.ops.mail.emptyFiltered', {
                    defaultValue: '没有符合条件的邮件。',
                  })
                : t('page.console.ops.mail.empty', {
                    defaultValue: '还没有发出过邮件。',
                  })}
            </p>
          ) : (
            items.map((mail) => (
              <Link
                className='ops-table__row ops-table__row--link'
                key={mail.id}
                role='row'
                to={`/console/ops/mail/${mail.id}`}
              >
                <span className='ops-mono ops-small ops-muted' role='cell'>
                  {formatMailTime(mail.created_at)}
                </span>
                <span role='cell'>{kindLabel(t, mail.kind)}</span>
                <span className='ops-mail-table__who' role='cell'>
                  <span className='ops-mail-ellipsis'>
                    {mail.receiver || '—'}
                  </span>
                  {mail.username ? (
                    <span className='ops-muted ops-small ops-mail-ellipsis'>
                      {mail.username}
                    </span>
                  ) : null}
                </span>
                <span className='ops-mail-ellipsis' role='cell'>
                  {mail.subject || '—'}
                </span>
                <span className='ops-mono ops-small' role='cell'>
                  {mail.period || '—'}
                </span>
                <span role='cell'>
                  <MailStatus mail={mail} />
                </span>
              </Link>
            ))
          )}
        </div>
      )}

      {list.isError ? null : (
        <Pagination
          currentPage={page}
          onPageChange={(next) => update({ p: String(next) }, true)}
          pageSize={MAIL_PAGE_SIZE}
          pending={list.isPending}
          showTotal={(total) =>
            t('page.console.ops.mail.total', {
              defaultValue: '共 {{count}} 封',
              count: total,
            })
          }
          total={list.data?.total ?? 0}
        />
      )}

      <p className='ops-footnote'>
        {t('page.console.ops.mail.footnote', {
          defaultValue:
            '正文保留 180 天，发送记录保留 1 年。含登录链接、验证码的邮件只存标题和状态。不放追踪像素，不统计打开率。',
        })}
      </p>
    </ConsolePage>
  );
}
