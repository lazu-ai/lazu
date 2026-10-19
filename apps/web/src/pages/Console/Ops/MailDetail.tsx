/* 内容 › 邮件 › 一封邮件: who got it, what happened to it, the data it was
 * written from, and — unless it carried a credential or aged out — exactly
 * what the reader saw. */
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { OpsNav } from '.';
import { mailApi, mailKeys, type MailDelivery } from './mailApi';
import {
  MailStatus,
  formatMailTime,
  kindLabel,
  statusLabel,
} from './mailShared';
import {
  Button,
  ConsolePage,
  ErrorState,
  Modal,
  PendingBlock,
  PendingValue,
  notify,
} from '../../../components/ui';
import type { TFunction } from 'i18next';
import type { ReactNode } from 'react';

type Confirm = 'resend' | 'regenerate';

const REGENERABLE = new Set(['weekly_digest', 'monthly_digest']);
const FRAME_HEIGHT = 900;

type Step = { at: number; label: string; detail?: string; tone: string };

const stepsOf = (t: TFunction, mail: MailDelivery): Step[] => {
  const steps: Step[] = [];
  if (mail.generated_at) {
    steps.push({
      at: mail.generated_at,
      label: t('page.console.ops.mail.timeline.generated', {
        defaultValue: '生成',
      }),
      tone: 'neutral',
    });
  }
  if (mail.status === 'failed') {
    steps.push({
      at: mail.sent_at || mail.event_at || mail.created_at,
      label: t('page.console.ops.mail.timeline.failed', {
        defaultValue: '发送失败',
      }),
      detail: mail.error || undefined,
      tone: 'danger',
    });
  } else if (mail.sent_at) {
    steps.push({
      at: mail.sent_at,
      label: t('page.console.ops.mail.timeline.sent', {
        defaultValue: '交给 Resend',
      }),
      tone: 'neutral',
    });
  }
  if (
    mail.event_at &&
    ['delivered', 'bounced', 'complained', 'delayed'].includes(mail.status)
  ) {
    steps.push({
      at: mail.event_at,
      label: statusLabel(t, mail.status),
      tone:
        mail.status === 'delivered'
          ? 'done'
          : mail.status === 'delayed'
            ? 'warn'
            : 'danger',
    });
  }
  return steps;
};

const prettySnapshot = (raw: string) => {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
};

export default function MailDetail({ mailId }: { mailId: string }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [confirm, setConfirm] = useState<Confirm>('resend');
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [snapshotOpen, setSnapshotOpen] = useState(true);
  const detail = useQuery({
    queryKey: mailKeys.detail(mailId),
    queryFn: () => mailApi.get(mailId),
  });
  const mail = detail.data?.mail;
  const html = detail.data?.html || '';
  const snapshot = useMemo(
    () => (detail.data?.snapshot ? prettySnapshot(detail.data.snapshot) : ''),
    [detail.data?.snapshot],
  );

  const refresh = () =>
    queryClient.invalidateQueries({ queryKey: mailKeys.root });

  const resend = useMutation({
    mutationFn: (id: number) => mailApi.resend(id),
    onSuccess: async () => {
      setConfirmOpen(false);
      notify.success(
        t('page.console.ops.mail.toast.resent', {
          defaultValue: '已重新发给 {{receiver}}',
          receiver: mail?.receiver || '',
        }),
      );
      await refresh();
    },
  });
  const regenerate = useMutation({
    mutationFn: (id: number) => mailApi.regenerate(id),
    onSuccess: async () => {
      setConfirmOpen(false);
      notify.success(
        t('page.console.ops.mail.toast.regenerated', {
          defaultValue: '已按当前模板重新生成并发出',
        }),
      );
      await refresh();
    },
  });
  const copy = useMutation({
    mutationFn: (id: number) => mailApi.copy(id),
    onSuccess: (data) => {
      notify.success(
        t('page.console.ops.mail.toast.copied', {
          defaultValue: '已发到 {{receiver}}',
          receiver: data?.receiver || '',
        }),
      );
    },
  });

  const downloadSnapshot = () => {
    const blob = new Blob([snapshot], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = `mail-${mailId}-snapshot.json`;
    link.click();
    URL.revokeObjectURL(url);
  };

  const field = (label: string, value: ReactNode, chars = 12) => (
    <div className='ops-mail-meta__row'>
      <dt>{label}</dt>
      <dd>{mail ? value : <PendingValue chars={chars} />}</dd>
    </div>
  );

  const title = t('page.console.ops.title', { defaultValue: '内容' });

  if (detail.isError) {
    return (
      <ConsolePage
        appearance='review'
        className='ops-page'
        tabs={<OpsNav />}
        title={title}
      >
        <ErrorState
          action={
            <Button onClick={() => void detail.refetch()} variant='secondary'>
              {t('common.retry', { defaultValue: '重试' })}
            </Button>
          }
        />
      </ConsolePage>
    );
  }

  const confirmBusy = resend.isPending || regenerate.isPending;

  return (
    <ConsolePage
      appearance='review'
      className='ops-page'
      tabs={<OpsNav />}
      title={title}
    >
      <div className='ops-mail-detail'>
        <aside className='ops-mail-detail__meta'>
          <nav className='ops-crumbs'>
            <Link to='/console/ops/mail'>
              {t('page.console.ops.mail.nav.log', {
                defaultValue: '发送记录',
              })}
            </Link>
            <span aria-hidden='true'>›</span>
            <span className='ops-mono'>#{mailId}</span>
          </nav>
          <h1 className='ops-mail-detail__subject'>
            {mail ? (
              mail.subject ||
              t('page.console.ops.mail.noSubject', { defaultValue: '无标题' })
            ) : (
              <PendingValue chars={24} />
            )}
          </h1>
          <div>{mail ? <MailStatus mail={mail} /> : <PendingValue />}</div>

          <dl className='ops-mail-meta'>
            {field(
              t('page.console.ops.mail.col.receiver', {
                defaultValue: '收件人',
              }),
              mail?.receiver || '—',
              20,
            )}
            {field(
              t('page.console.ops.mail.field.user', { defaultValue: '用户' }),
              mail && mail.user_id
                ? `${mail.username || '—'} · #${mail.user_id}`
                : '—',
            )}
            {field(
              t('page.console.ops.mail.col.kind', { defaultValue: '类型' }),
              mail ? kindLabel(t, mail.kind) : '',
              4,
            )}
            {field(
              t('page.console.ops.mail.col.period', { defaultValue: '期次' }),
              <span className='ops-mono'>{mail?.period || '—'}</span>,
              8,
            )}
            {field(
              t('page.console.ops.mail.field.zoneLang', {
                defaultValue: '时区 · 语言',
              }),
              `${mail?.timezone || '—'} · ${mail?.lang || '—'}`,
              18,
            )}
            {field(
              t('page.console.ops.mail.field.template', {
                defaultValue: '模板',
              }),
              <span className='ops-mono'>{mail?.template || '—'}</span>,
            )}
            {field(
              t('page.console.ops.mail.field.provider', {
                defaultValue: '服务商 ID',
              }),
              <span className='ops-mono ops-mail-break'>
                {mail?.provider_id || '—'}
              </span>,
              24,
            )}
            {field(
              t('page.console.ops.mail.field.lastEvent', {
                defaultValue: '最近事件',
              }),
              mail?.last_event || '—',
            )}
          </dl>

          <section className='ops-mail-section'>
            <h2 className='ops-eyebrow'>
              {t('page.console.ops.mail.timeline.title', {
                defaultValue: '经过',
              })}
            </h2>
            {mail ? (
              <ol className='ops-mail-steps'>
                {stepsOf(t, mail).map((step) => (
                  <li className='ops-mail-steps__item' key={step.label}>
                    <span className={`ops-dot ops-dot--${step.tone}`} />
                    <span className='ops-mail-steps__label'>
                      {step.label}
                      {step.detail ? (
                        <span className='ops-muted ops-small ops-mail-break'>
                          {step.detail}
                        </span>
                      ) : null}
                    </span>
                    <span className='ops-mono ops-small ops-muted'>
                      {formatMailTime(step.at)}
                    </span>
                  </li>
                ))}
              </ol>
            ) : (
              <PendingBlock height={96} />
            )}
          </section>

          {snapshot ? (
            <section className='ops-mail-section'>
              <div className='ops-mail-section__head'>
                <h2 className='ops-eyebrow'>
                  {t('page.console.ops.mail.snapshot.title', {
                    defaultValue: '数据快照',
                  })}
                </h2>
                <Button
                  aria-expanded={snapshotOpen}
                  onClick={() => setSnapshotOpen((open) => !open)}
                  size='xs'
                  variant='ghost'
                >
                  {snapshotOpen
                    ? t('page.console.ops.mail.snapshot.hide', {
                        defaultValue: '收起',
                      })
                    : t('page.console.ops.mail.snapshot.show', {
                        defaultValue: '展开',
                      })}
                </Button>
                <Button onClick={downloadSnapshot} size='xs' variant='ghost'>
                  {t('page.console.ops.mail.snapshot.download', {
                    defaultValue: '下载 JSON',
                  })}
                </Button>
              </div>
              {snapshotOpen ? (
                <pre className='ops-mail-snapshot'>{snapshot}</pre>
              ) : null}
            </section>
          ) : null}

          {mail?.status === 'draft' ? (
            <p className='ops-hint'>
              {t('page.console.ops.mail.draftNote', {
                defaultValue:
                  '这是草稿，没有发给客户，也不会发。正式发送时会按当时的数据重新生成。',
              })}
            </p>
          ) : null}
          {mail ? (
            <div className='ops-mail-actions'>
              {mail.has_body && mail.status !== 'draft' ? (
                <Button
                  onClick={() => {
                    setConfirm('resend');
                    setConfirmOpen(true);
                  }}
                  size='sm'
                  variant='secondary'
                >
                  {t('page.console.ops.mail.action.resend', {
                    defaultValue: '原样重发',
                  })}
                </Button>
              ) : null}
              {REGENERABLE.has(mail.kind) && mail.status !== 'draft' ? (
                <Button
                  onClick={() => {
                    setConfirm('regenerate');
                    setConfirmOpen(true);
                  }}
                  size='sm'
                  variant='secondary'
                >
                  {t('page.console.ops.mail.action.regenerate', {
                    defaultValue: '用当前模板重新生成',
                  })}
                </Button>
              ) : null}
              {mail.has_body ? (
                <Button
                  loading={copy.isPending}
                  onClick={() => copy.mutate(mail.id)}
                  size='sm'
                  variant='primary'
                >
                  {t('page.console.ops.mail.action.copy', {
                    defaultValue: '发一份给我',
                  })}
                </Button>
              ) : null}
            </div>
          ) : null}
        </aside>

        <section className='ops-mail-detail__body'>
          <h2 className='ops-eyebrow'>
            {t('page.console.ops.mail.body.label', {
              defaultValue: '用户收到的原样内容',
            })}
          </h2>
          {detail.isPending ? (
            <PendingBlock height={FRAME_HEIGHT} />
          ) : html ? (
            <iframe
              className='ops-mail-frame'
              sandbox=''
              srcDoc={html}
              style={{ height: FRAME_HEIGHT }}
              title={mail?.subject || `#${mailId}`}
            />
          ) : (
            <p className='ops-mail-frame-note'>
              {mail?.sensitive
                ? t('page.console.ops.mail.body.sensitive', {
                    defaultValue: '这类邮件带登录凭据，只保存标题和状态。',
                  })
                : t('page.console.ops.mail.body.purged', {
                    defaultValue: '正文已超过 180 天被清除。',
                  })}
            </p>
          )}
        </section>
      </div>

      <Modal
        body={
          <p className='ops-note'>
            {confirm === 'regenerate'
              ? t('page.console.ops.mail.confirm.regenerateBody', {
                  defaultValue:
                    '按当前模板和这一期的数据重新生成，再发给 {{receiver}}。客户会再收到一封，并多一条发送记录。',
                  receiver: mail?.receiver || '',
                })
              : t('page.console.ops.mail.confirm.resendBody', {
                  defaultValue:
                    '把归档里的原文再发给 {{receiver}} 一次。客户会再收到一封，并多一条发送记录。',
                  receiver: mail?.receiver || '',
                })}
          </p>
        }
        busy={confirmBusy}
        footer={
          <>
            <Button onClick={() => setConfirmOpen(false)} variant='secondary'>
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              loading={confirmBusy}
              onClick={() => {
                if (!mail) return;
                if (confirm === 'regenerate') regenerate.mutate(mail.id);
                else resend.mutate(mail.id);
              }}
              variant='primary'
            >
              {confirm === 'regenerate'
                ? t('page.console.ops.mail.action.regenerate', {
                    defaultValue: '用当前模板重新生成',
                  })
                : t('page.console.ops.mail.action.resend', {
                    defaultValue: '原样重发',
                  })}
            </Button>
          </>
        }
        onOpenChange={setConfirmOpen}
        open={confirmOpen}
        title={
          confirm === 'regenerate'
            ? t('page.console.ops.mail.confirm.regenerateTitle', {
                defaultValue: '用当前模板重新生成这封邮件？',
              })
            : t('page.console.ops.mail.confirm.resendTitle', {
                defaultValue: '原样重发这封邮件？',
              })
        }
      />
    </ConsolePage>
  );
}
