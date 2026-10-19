/* 运营 › 邮件: the weekly and monthly usage digests in one bar above the send
 * log — whether they are going out, when the next ones go, to how many
 * readers, this period's drafts and the thresholds operators tune. */
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { mailApi, mailKeys, type DigestSettings } from './mailApi';
import { zonedParts } from './mailShared';
import {
  Button,
  Input,
  Modal,
  PendingValue,
  notify,
} from '../../../components/ui';

type Draft = {
  min_spend_usd: string;
  min_saving_usd: string;
  min_saving_pct: string;
};

const toDraft = (s?: DigestSettings): Draft => ({
  min_spend_usd: String(s?.min_spend_usd ?? 1),
  min_saving_usd: String(s?.min_saving_usd ?? 1),
  min_saving_pct: String(s?.min_saving_pct ?? 15),
});

export default function MailDigestBar() {
  const { t, i18n } = useTranslation();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const overview = useQuery({
    queryKey: mailKeys.digest,
    queryFn: mailApi.digest,
  });
  const data = overview.data;
  const locale = i18n.resolvedLanguage || i18n.language || 'zh';
  const [enableOpen, setEnableOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [draft, setDraft] = useState<Draft>(toDraft());

  const save = useMutation({
    mutationFn: mailApi.setDigest,
    onSuccess: async (result, vars) => {
      setEnableOpen(false);
      setSettingsOpen(false);
      if (vars.enabled !== undefined) {
        notify.success(
          result?.enabled
            ? t('page.console.ops.mail.digest.toast.enabled', {
                defaultValue: '用量邮件已开始发送',
              })
            : t('page.console.ops.mail.digest.toast.paused', {
                defaultValue: '用量邮件已暂停',
              }),
        );
      } else {
        notify.success(
          t('page.console.ops.mail.digest.toast.saved', {
            defaultValue: '设置已保存，立即生效',
          }),
        );
      }
      await queryClient.invalidateQueries({ queryKey: mailKeys.digest });
    },
  });
  const drafts = useMutation({
    mutationFn: async () => {
      const monthly = await mailApi.generateDrafts('monthly');
      const weekly = await mailApi.generateDrafts('weekly');
      return { monthly: monthly?.drafts ?? 0, weekly: weekly?.drafts ?? 0 };
    },
    onSuccess: async (result) => {
      notify.success(
        t('page.console.ops.mail.drafts.generatedBoth', {
          defaultValue:
            '已生成月报 {{monthly}} 封、周报 {{weekly}} 封草稿，没有发给任何人',
          ...result,
        }),
      );
      await queryClient.invalidateQueries({ queryKey: mailKeys.root });
      navigate('/console/ops/mail?kind=digest&status=draft');
    },
  });

  const next = (unix?: number) => {
    if (!unix) return '—';
    const p = zonedParts(unix, 'Asia/Shanghai', locale);
    return t('page.console.ops.mail.digest.beijing', {
      defaultValue: '{{date}} {{weekday}} {{time}} 北京时间',
      ...p,
    });
  };

  const field = (key: keyof Draft, label: string, unit: string) => (
    <label className='ops-field'>
      <span className='ops-field__label'>{label}</span>
      <span className='ops-mail-bar__input'>
        <Input
          inputMode='decimal'
          min='0'
          onChange={(event) =>
            setDraft((d) => ({ ...d, [key]: event.target.value }))
          }
          step='0.01'
          type='number'
          value={draft[key]}
        />
        <span className='ops-muted ops-small'>{unit}</span>
      </span>
    </label>
  );

  return (
    <section
      className='ops-panel ops-mail-bar'
      aria-label={t('page.console.ops.mail.digest.title', {
        defaultValue: '用量邮件',
      })}
    >
      <div className='ops-mail-bar__facts'>
        <span className='ops-mail-bar__name'>
          {t('page.console.ops.mail.digest.title', {
            defaultValue: '用量邮件',
          })}
          {data ? (
            <span
              className={`ops-mail-bar__state${data.enabled ? ' is-on' : ''}`}
            >
              {data.enabled
                ? t('page.console.ops.mail.digest.on', {
                    defaultValue: '发送中',
                  })
                : t('page.console.ops.mail.digest.off', {
                    defaultValue: '已暂停',
                  })}
            </span>
          ) : (
            <PendingValue chars={4} />
          )}
        </span>
        <span className='ops-mail-bar__fact'>
          <span className='ops-muted'>
            {t('page.console.ops.mail.digest.nextMonthly', {
              defaultValue: '下一封月报',
            })}
          </span>
          {data ? (
            next(data.next.monthly.asia_shanghai)
          ) : (
            <PendingValue chars={18} />
          )}
        </span>
        <span className='ops-mail-bar__fact'>
          <span className='ops-muted'>
            {t('page.console.ops.mail.digest.nextWeekly', {
              defaultValue: '下一封周报',
            })}
          </span>
          {data ? (
            next(data.next.weekly.asia_shanghai)
          ) : (
            <PendingValue chars={18} />
          )}
        </span>
        <span className='ops-mail-bar__fact'>
          <span className='ops-muted'>
            {t('page.console.ops.mail.digest.reach', {
              defaultValue: '付费用户',
            })}
          </span>
          {data ? (
            t('page.console.ops.mail.digest.reachValue', {
              defaultValue: '{{count}} 人',
              count: data.candidates,
            })
          ) : (
            <PendingValue chars={4} />
          )}
        </span>
      </div>
      <div className='ops-mail-bar__actions'>
        <Button
          loading={drafts.isPending}
          onClick={() => drafts.mutate()}
          size='sm'
          variant='secondary'
        >
          {t('page.console.ops.mail.drafts.generate', {
            defaultValue: '生成本期草稿',
          })}
        </Button>
        <Button
          disabled={!data}
          onClick={() => {
            setDraft(toDraft(data?.settings));
            setSettingsOpen(true);
          }}
          size='sm'
          variant='secondary'
        >
          {t('page.console.ops.mail.digest.settings', { defaultValue: '设置' })}
        </Button>
        {data?.enabled ? (
          <Button
            loading={save.isPending}
            onClick={() => save.mutate({ enabled: false })}
            size='sm'
            variant='secondary'
          >
            {t('page.console.ops.mail.digest.pause', {
              defaultValue: '暂停发送',
            })}
          </Button>
        ) : (
          <Button
            disabled={!data}
            onClick={() => setEnableOpen(true)}
            size='sm'
            variant='primary'
          >
            {t('page.console.ops.mail.digest.enable', {
              defaultValue: '开始发送',
            })}
          </Button>
        )}
      </div>

      <Modal
        body={
          <p className='ops-note'>
            {t('page.console.ops.mail.digest.enableBody', {
              defaultValue:
                '打开后，每位付过款的客户会在各自时区的下一个 09:30 收到用量回顾。当前收件范围 {{count}} 人。',
              count: data?.candidates ?? 0,
            })}
          </p>
        }
        busy={save.isPending}
        footer={
          <>
            <Button onClick={() => setEnableOpen(false)} variant='secondary'>
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              loading={save.isPending}
              onClick={() => save.mutate({ enabled: true })}
              variant='primary'
            >
              {t('page.console.ops.mail.digest.enable', {
                defaultValue: '开始发送',
              })}
            </Button>
          </>
        }
        onOpenChange={setEnableOpen}
        open={enableOpen}
        title={t('page.console.ops.mail.digest.enableTitle', {
          defaultValue: '开始自动发送用量邮件？',
        })}
      />

      <Modal
        body={
          <div className='ops-mail-bar__settings'>
            {field(
              'min_spend_usd',
              t('page.console.ops.mail.digest.minSpend', {
                defaultValue: '本期花费至少',
              }),
              'USD',
            )}
            {field(
              'min_saving_usd',
              t('page.console.ops.mail.digest.minSaving', {
                defaultValue: '显示「比官方价少付」：省下至少',
              }),
              'USD',
            )}
            {field(
              'min_saving_pct',
              t('page.console.ops.mail.digest.minSavingPct', {
                defaultValue: '并且节省比例至少',
              }),
              '%',
            )}
            <p className='ops-hint'>
              {t('page.console.ops.mail.digest.settingsHint', {
                defaultValue:
                  '花费不到门槛的用户那一期不发；节省没达到两个条件时，邮件里不显示「比官方价少付」。保存后立即生效。',
              })}
            </p>
          </div>
        }
        busy={save.isPending}
        footer={
          <>
            <Button onClick={() => setSettingsOpen(false)} variant='secondary'>
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              loading={save.isPending}
              onClick={() =>
                save.mutate({
                  settings: {
                    min_spend_usd: Number(draft.min_spend_usd) || 0,
                    min_saving_usd: Number(draft.min_saving_usd) || 0,
                    min_saving_pct: Number(draft.min_saving_pct) || 0,
                  },
                })
              }
              variant='primary'
            >
              {t('common.save', { defaultValue: '保存' })}
            </Button>
          </>
        }
        onOpenChange={setSettingsOpen}
        open={settingsOpen}
        title={t('page.console.ops.mail.digest.settingsTitle', {
          defaultValue: '用量邮件设置',
        })}
      />
    </section>
  );
}
