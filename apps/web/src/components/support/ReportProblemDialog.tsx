import { useMutation } from '@tanstack/react-query';
import { useContext, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  REPORT_PROBLEM_OPEN_EVENT,
  type ReportProblemDetail,
} from './reportChannel';
import { UserContext } from '../../context/User';
import { API } from '../../helpers/api';
import { getSupportEmail } from '../../helpers/brandUrls';
import { showSuccess } from '../../helpers/utils';
import { apiErrorHandler, readApiError, unwrap } from '../../lib/api-error';
import { Button, Modal, Textarea } from '../ui';

/* 报告问题: the reader's own words plus what the console already knows (the
 * error number and request id from the failure, the page). The server adds
 * the account and the request itself and mails support with Reply-To set,
 * so the answer comes back by mail. Where the deployment has no support
 * address the dialog hands the same text to the mail app instead. */

const ns = 'console.report';

const mailtoHref = (subject: string, body: string) =>
  `mailto:${getSupportEmail()}?subject=${encodeURIComponent(subject)}&body=${encodeURIComponent(body)}`;

export default function ReportProblemDialog() {
  const { t } = useTranslation();
  const [userState] = useContext(UserContext);
  const [open, setOpen] = useState(false);
  const [detail, setDetail] = useState<ReportProblemDetail>({});
  const [message, setMessage] = useState('');
  const [page, setPage] = useState('');

  useEffect(() => {
    const handleOpen = (event: Event) => {
      if (!userState?.user) return;
      const next = (event as CustomEvent<ReportProblemDetail>).detail || {};
      setDetail(next);
      setMessage(next.message ? `${next.message}\n\n` : '');
      setPage(window.location.pathname + window.location.search);
      setOpen(true);
    };
    window.addEventListener(REPORT_PROBLEM_OPEN_EVENT, handleOpen);
    return () =>
      window.removeEventListener(REPORT_PROBLEM_OPEN_EVENT, handleOpen);
  }, [userState?.user]);

  const facts = [
    detail.error
      ? `${t(`${ns}.error`, { defaultValue: '错误编号' })}: ${detail.error}`
      : '',
    detail.requestId
      ? `${t(`${ns}.requestId`, { defaultValue: '请求 ID' })}: ${detail.requestId}`
      : '',
    `${t(`${ns}.page`, { defaultValue: '页面' })}: ${page}`,
  ].filter(Boolean);

  const sendMutation = useMutation({
    mutationFn: async () =>
      unwrap(
        await API.post('/api/user/support/report', {
          message,
          error: detail.error || '',
          request_id: detail.requestId || '',
          page,
        }),
      ),
    onSuccess: () => {
      setOpen(false);
      showSuccess(
        t(`${ns}.sent`, {
          defaultValue: '已发送。客服会回复到你的邮箱。',
        }),
      );
    },
    onError: (error) => {
      if (readApiError(error).code === 'support_unavailable') {
        window.location.href = mailtoHref(
          t(`${ns}.mailSubject`, { defaultValue: '控制台问题报告' }),
          [message.trim(), '', ...facts].join('\n'),
        );
        setOpen(false);
        return;
      }
      apiErrorHandler(error);
    },
  });

  const tooShort = message.trim().length < 2;

  return (
    <Modal
      busy={sendMutation.isPending}
      className='report-problem'
      open={open}
      onOpenChange={setOpen}
      title={t(`${ns}.title`, { defaultValue: '报告问题' })}
      description={t(`${ns}.description`, {
        defaultValue:
          '说说你在做什么、看到了什么。我们会附上下面这些信息，回复会发到你的账户邮箱。',
      })}
      body={
        <div className='grid gap-3'>
          <label className='set-ed__lb' htmlFor='report-problem-message'>
            {t(`${ns}.message`, { defaultValue: '问题描述' })}
          </label>
          <Textarea
            className='min-h-32 w-full'
            id='report-problem-message'
            maxLength={4000}
            onChange={(event) => setMessage(event.target.value)}
            value={message}
          />
          <ul className='set-ed__d grid gap-1 font-mono text-xs'>
            {facts.map((line) => (
              <li key={line}>{line}</li>
            ))}
          </ul>
        </div>
      }
      footer={
        <>
          <Button onClick={() => setOpen(false)} size='sm' type='button'>
            {t(`${ns}.cancel`, { defaultValue: '取消' })}
          </Button>
          <Button
            disabled={tooShort}
            loading={sendMutation.isPending}
            onClick={() => sendMutation.mutate()}
            size='sm'
            type='button'
            variant='primary'
          >
            {t(`${ns}.send`, { defaultValue: '发送' })}
          </Button>
        </>
      }
    />
  );
}
