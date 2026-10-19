/* 项目设置, drawn the way personal settings are: each group is a card of
 * paper with a title and a one-line description, and each row reads
 * `label + explanation | value + action`. A value is a fact; changing it goes
 * through the row's own action (a small dialog, or a menu for a short list)
 * and saves on its own, so there is no page-wide save bar. The reasoning
 * effort cap for new members sits in its row as the A2 control and saves on
 * change. Locked 2026-09-28: brand/foundations/team-projects-2026-09.md. */
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import {
  Button,
  EffortCapControl,
  Input,
  Modal,
  Popover,
  PopoverContent,
  PopoverTrigger,
  SettingsCard,
  SettingsRow,
} from '../../../components/ui';
import ConsolePage from '../../../components/ui/console-page';
import { PendingBlock, PendingValue } from '../../../components/ui/pending';
import { useDelayedReveal } from '../../../hooks/common/useDelayedReveal';
import { errorMessage, isLazuApiError } from '../../../lib/api-error';
import { draftFromMicro, validateBudget, type BudgetDraft } from '../budget';
import {
  BudgetPicker,
  ErrLine,
  GateModal,
  PjBadge,
  budgetErrorText,
} from '../components/atoms';
import { formatUSD, formatUSDShort } from '../money';
import { useProject } from '../ProjectContext';
import {
  useInvalidateProject,
  useProjectDetail,
  useProjectMembers,
  useProjectTokens,
  useProjectWallet,
} from '../queries';
import { errorReceipt, receipt } from '../receipt';
import { type ProjectSettings } from '../types';

const WARNING_OPTIONS = [50, 70, 80, 90];

type Editor = 'name' | 'budget' | 'cap' | null;

export default function SettingsPage() {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const navigate = useNavigate();
  const project = useProject();
  const invalidate = useInvalidateProject();
  const detail = useProjectDetail();
  const wallet = useProjectWallet();
  const members = useProjectMembers();
  const tokens = useProjectTokens();
  const d = detail.data;
  const revealDetail = useDelayedReveal(detail.isPending);

  const [editor, setEditor] = useState<Editor>(null);
  const [nameDraft, setNameDraft] = useState('');
  const [budgetDraft, setBudgetDraft] = useState<BudgetDraft>({
    mode: 'unlimited',
  });
  const [menu, setMenu] = useState<'topup' | 'warning' | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [dissolveOpen, setDissolveOpen] = useState(false);
  const [dissolving, setDissolving] = useState(false);
  const [portalBusy, setPortalBusy] = useState(false);
  const [missingPortalProject, setMissingPortalProject] = useState<
    string | null
  >(null);

  /* Every row saves on its own against the version it read. */
  const save = async (body: {
    name?: string;
    settings?: Partial<ProjectSettings>;
  }) => {
    if (!project.client || !project.current || !d) return false;
    setSaving(true);
    setError('');
    try {
      await project.client.updateProject(project.current.id, {
        ...body,
        expected_version: d.version,
      });
      await invalidate('detail');
      if (body.name !== undefined) await project.refreshProjects();
      receipt(t('projects.settings.savedReceipt'));
      return true;
    } catch (e) {
      const message =
        isLazuApiError(e) && e.code === 'version_conflict'
          ? errorMessage(e)
          : t('projects.settings.saveFailed');
      setError(message);
      if (!editor) errorReceipt(e);
      return false;
    } finally {
      setSaving(false);
    }
  };

  const openEditor = (next: Exclude<Editor, null>) => {
    if (!d) return;
    setError('');
    if (next === 'name') setNameDraft(d.name);
    if (next === 'budget')
      setBudgetDraft(draftFromMicro(d.settings.default_member_budget_microusd));
    if (next === 'cap')
      setBudgetDraft(draftFromMicro(d.settings.project_cap_microusd));
    setEditor(next);
  };

  const budgetValidation = validateBudget(budgetDraft);
  const submitEditor = async () => {
    if (!editor) return;
    let ok = false;
    if (editor === 'name') {
      if (!nameDraft.trim()) return;
      ok = await save({ name: nameDraft.trim() });
    } else if (budgetValidation.ok) {
      ok = await save({
        settings:
          editor === 'budget'
            ? { default_member_budget_microusd: budgetValidation.micro }
            : { project_cap_microusd: budgetValidation.micro },
      });
    }
    if (ok) setEditor(null);
  };

  const dissolve = async () => {
    if (!project.client || !project.current || !d) return;
    setDissolving(true);
    try {
      await project.client.dissolveProject(project.current.id, d.name);
      setDissolveOpen(false);
      await project.refreshProjects();
      await invalidate('all');
      receipt(t('projects.settings.dissolvedReceipt', { name: d.name }));
      navigate('/console');
    } catch (e) {
      errorReceipt(e);
    } finally {
      setDissolving(false);
    }
  };

  const openPortal = async () => {
    if (!project.client || !project.current) return;
    setPortalBusy(true);
    try {
      const { url } = await project.client.getPortalUrl(project.current.id);
      window.open(url, '_blank', 'noopener');
    } catch (e) {
      if (isLazuApiError(e) && e.code === 'no_billing_customer') {
        setMissingPortalProject(project.current.id);
      } else {
        errorReceipt(e);
      }
    } finally {
      setPortalBusy(false);
    }
  };

  const billing = wallet.data?.billing_details;
  const billingLine = billing?.name
    ? [billing.name, billing.address?.city, billing.tax_ids[0]?.value]
        .filter(Boolean)
        .join(' · ')
    : t('projects.settings.billingNone');
  const memberCount = (members.data ?? []).filter(
    (m) => m.role !== 'owner',
  ).length;
  // Dissolving stops every Key that is not the owner's own.
  const otherKeyCount = (tokens.data ?? []).filter(
    (k) => k.owner?.id !== project.viewerId,
  ).length;
  const topupLabel = (value: ProjectSettings['who_can_topup']) =>
    value === 'owner'
      ? t('projects.settings.ownerOnly')
      : t('projects.settings.ownerAndAdmin');
  const pending = <PendingValue chars={10} />;
  const allocated = d?.allocation.member_caps_microusd ?? 0;

  return (
    <ConsolePage
      appearance='review'
      className='pj-page pj-settings-page'
      title={t('projects.settings.title')}
      description={t('projects.settings.lede', {
        project: project.currentName,
      })}
    >
      {detail.isError || wallet.isError || members.isError || tokens.isError ? (
        <ErrLine>
          {t('projects.key.form.unavailable')}{' '}
          <Button
            size='sm'
            onClick={() => {
              void detail.refetch();
              void wallet.refetch();
              void members.refetch();
              void tokens.refetch();
            }}
          >
            {t('projects.key.form.retry')}
          </Button>
        </ErrLine>
      ) : null}

      <div className='pj-set-stack'>
        <SettingsCard
          description={t('projects.settings.group.projectSub')}
          title={t('projects.settings.group.project')}
        >
          <SettingsRow
            actions={
              <Button
                disabled={!d}
                onClick={() => openEditor('name')}
                size='sm'
                variant='secondary'
              >
                {t('projects.settings.edit')}
              </Button>
            }
            hint={t('projects.settings.nameHint')}
            label={t('projects.settings.name')}
            value={d ? d.name : pending}
          />
          <SettingsRow
            hint={t('projects.settings.ownerHint')}
            label={t('projects.settings.owner')}
            value={d ? d.owner.username : pending}
          />
          <SettingsRow
            actions={
              <Popover
                open={menu === 'topup'}
                onOpenChange={(open) => setMenu(open ? 'topup' : null)}
              >
                <PopoverTrigger>
                  <Button disabled={!d || saving} size='sm' variant='secondary'>
                    {t('projects.settings.switch')}
                  </Button>
                </PopoverTrigger>
                <PopoverContent align='start' className='pj-menu'>
                  {(['owner_admin', 'owner'] as const).map((value) => (
                    <button
                      className='pj-menu__it'
                      data-on={value === d?.settings.who_can_topup || undefined}
                      data-owns-chrome=''
                      key={value}
                      onClick={() => {
                        setMenu(null);
                        if (value !== d?.settings.who_can_topup)
                          void save({ settings: { who_can_topup: value } });
                      }}
                      type='button'
                    >
                      {topupLabel(value)}
                    </button>
                  ))}
                </PopoverContent>
              </Popover>
            }
            hint={t('projects.settings.whoCanTopupHint')}
            label={t('projects.settings.whoCanTopup')}
            value={d ? topupLabel(d.settings.who_can_topup) : pending}
          />
          <SettingsRow
            actions={
              missingPortalProject !== project.current?.id ? (
                <Button
                  disabled={portalBusy || !wallet.data}
                  onClick={() => void openPortal()}
                  size='sm'
                  variant='secondary'
                >
                  {t('projects.settings.manage')}
                </Button>
              ) : (
                <span className='set-row__ro'>
                  {t('projects.settings.portalAfterFirstPayment')}
                </span>
              )
            }
            hint={t('projects.settings.billingHint')}
            label={t('projects.settings.billing')}
            value={wallet.data ? billingLine : <PendingValue chars={15} />}
          />
        </SettingsCard>

        <SettingsCard
          description={t('projects.settings.defaultsSub')}
          title={t('projects.settings.group.defaults')}
        >
          <SettingsRow
            actions={
              <Button
                disabled={!d}
                onClick={() => openEditor('budget')}
                size='sm'
                variant='secondary'
              >
                {t('projects.settings.editValue')}
              </Button>
            }
            hint={t('projects.settings.newMemberBudgetHint')}
            label={t('projects.memberEdit.budget')}
            value={
              d
                ? d.settings.default_member_budget_microusd === null
                  ? t('projects.budget.unlimited')
                  : t('projects.settings.perMonth', {
                      amount: formatUSD(
                        d.settings.default_member_budget_microusd,
                      ),
                    })
                : pending
            }
          />
          <SettingsRow
            actions={
              <Popover
                open={menu === 'warning'}
                onOpenChange={(open) => setMenu(open ? 'warning' : null)}
              >
                <PopoverTrigger>
                  <Button disabled={!d || saving} size='sm' variant='secondary'>
                    {t('projects.settings.editValue')}
                  </Button>
                </PopoverTrigger>
                <PopoverContent align='start' className='pj-menu'>
                  {WARNING_OPTIONS.map((value) => (
                    <button
                      className='pj-menu__it'
                      data-on={value === d?.settings.warning_pct || undefined}
                      data-owns-chrome=''
                      key={value}
                      onClick={() => {
                        setMenu(null);
                        if (value !== d?.settings.warning_pct)
                          void save({ settings: { warning_pct: value } });
                      }}
                      type='button'
                    >
                      {value}%
                    </button>
                  ))}
                </PopoverContent>
              </Popover>
            }
            hint={t('projects.settings.budgetReminderHint')}
            label={t('projects.settings.budgetReminder')}
            value={d ? `${d.settings.warning_pct}%` : pending}
          />
          <SettingsRow
            hint={t('projects.settings.effortHint')}
            label={t('projects.settings.effort')}
            value={
              <div className='lazu-effort' data-testid='project-effort-setting'>
                {d ? (
                  <EffortCapControl
                    aria-label={t('projects.settings.effort')}
                    disabled={!d || saving}
                    onChange={(next) =>
                      void save({
                        settings: { default_reasoning_effort_cap: next },
                      })
                    }
                    value={d.settings.default_reasoning_effort_cap ?? ''}
                  />
                ) : (
                  <div
                    style={{ visibility: revealDetail ? 'visible' : 'hidden' }}
                  >
                    <PendingBlock height={30} />
                  </div>
                )}
              </div>
            }
          />
        </SettingsCard>

        <SettingsCard
          description={t('projects.settings.group.capSub')}
          title={t('projects.settings.group.cap')}
        >
          <SettingsRow
            actions={
              <>
                {d && d.settings.project_cap_microusd !== null ? (
                  <PjBadge>
                    {t('projects.settings.allocated', {
                      amount: formatUSDShort(allocated),
                    })}
                  </PjBadge>
                ) : null}
                <Button
                  disabled={!d}
                  onClick={() => openEditor('cap')}
                  size='sm'
                  variant='secondary'
                >
                  {t('projects.settings.editValue')}
                </Button>
              </>
            }
            hint={t('projects.settings.projectCapHint')}
            label={t('projects.settings.projectCap')}
            value={
              d
                ? d.settings.project_cap_microusd === null
                  ? t('projects.budget.notSet')
                  : formatUSD(d.settings.project_cap_microusd)
                : pending
            }
          />
          <SettingsRow
            hint={t('projects.settings.resetHint')}
            label={t('projects.settings.resetTime')}
            value={t('projects.settings.resetValue')}
          />
        </SettingsCard>

        <SettingsCard
          className='pj-set-danger'
          description={t('projects.settings.dangerSub')}
          title={t('projects.detail.danger')}
        >
          <SettingsRow
            actions={
              <Button
                className='pj-settings-dissolve'
                disabled={
                  !d ||
                  !members.data ||
                  !tokens.data ||
                  members.isError ||
                  tokens.isError
                }
                onClick={() => setDissolveOpen(true)}
                size='sm'
                variant='danger'
              >
                {t('projects.settings.dissolveEllipsis')}
              </Button>
            }
            hint={t('projects.settings.dissolveNote')}
            label={t('projects.settings.dissolve')}
          />
        </SettingsCard>
      </div>

      <Modal
        busy={saving}
        className='pj-modal pj-modal--member'
        closeLabel={t('common.close', { defaultValue: '关闭' })}
        onOpenChange={(open) => {
          if (!open) setEditor(null);
        }}
        open={editor !== null}
        title={
          editor === 'name'
            ? t('projects.settings.name')
            : editor === 'budget'
              ? t('projects.settings.newMemberBudget')
              : t('projects.settings.projectCap')
        }
        body={
          <div className='pj-medit'>
            {editor === 'name' ? (
              <div className='pj-medit__f'>
                <Input
                  aria-label={t('projects.settings.name')}
                  onChange={(e) => setNameDraft(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') void submitEditor();
                  }}
                  value={nameDraft}
                />
                <p className='pj-medit__hint'>
                  {t('projects.settings.nameHint')}
                </p>
              </div>
            ) : (
              <div className='pj-medit__f'>
                <BudgetPicker
                  draft={budgetDraft}
                  label={
                    editor === 'budget'
                      ? t('projects.settings.newMemberBudget')
                      : t('projects.settings.projectCap')
                  }
                  onChange={setBudgetDraft}
                  quick={editor === 'budget' ? [50, 100, 500] : []}
                  size='sm'
                />
                <p className='pj-medit__hint'>
                  {budgetErrorText(budgetDraft, t) ||
                    (editor === 'budget'
                      ? t('projects.settings.newMemberBudgetHint')
                      : t('projects.settings.projectCapHint'))}
                </p>
              </div>
            )}
            {error ? <ErrLine>{error}</ErrLine> : null}
          </div>
        }
        footer={
          <div className='pj-modal__foot'>
            <Button
              disabled={saving}
              onClick={() => setEditor(null)}
              variant='secondary'
            >
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              disabled={
                editor === 'name' ? !nameDraft.trim() : !budgetValidation.ok
              }
              loading={saving}
              onClick={() => void submitEditor()}
              variant='primary'
            >
              {t('common.save', { defaultValue: '保存' })}
            </Button>
          </div>
        }
      />

      <GateModal
        busy={dissolving}
        busyLabel={t('projects.settings.dissolving')}
        confirmLabel={t('projects.settings.dissolveConfirm')}
        description={t('projects.settings.dissolveLede')}
        gate={{
          type: 'typed',
          expected: d?.name ?? '',
          label: t('projects.settings.typeName'),
        }}
        onConfirm={dissolve}
        onOpenChange={setDissolveOpen}
        open={dissolveOpen}
        title={t('projects.settings.dissolveTitle', { name: d?.name ?? '' })}
        bullets={[
          t('projects.settings.dissolve1', {
            members: memberCount,
            keys: otherKeyCount,
          }),
          t('projects.settings.dissolve2', {
            balance: formatUSD(d?.balance_microusd),
            name: d?.name ?? project.currentName,
          }),
          t('projects.settings.dissolve3'),
        ]}
      />
    </ConsolePage>
  );
}
