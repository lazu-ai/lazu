/* Dialogs that act on one member: role change (S4), removal (S5) — which
 * always stops that member's Keys in this project immediately, with a
 * 10-minute undo — and the budget-request handling shared with the Members
 * page and the attention list (S8). */
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Textarea } from '../../../components/ui';
import { isLazuApiError } from '../../../lib/api-error';
import {
  BUDGET_REQUEST_NOTE_MIN,
  REMOVAL_UNDO_MINUTES,
  draftFromMicro,
  validateBudget,
  validateBudgetRequestNote,
  type BudgetDraft,
} from '../budget';
import { formatUSD, formatUSDShort } from '../money';
import { useProject } from '../ProjectContext';
import { useInvalidateProject } from '../queries';
import { errorReceipt, receipt } from '../receipt';
import {
  type BudgetRequest,
  type ProjectMember,
  type ProjectRole,
  type ProjectToken,
  type RemovalReceipt,
} from '../types';
import {
  ActionModal,
  BudgetPicker,
  ErrLine,
  FormRow,
  FormRows,
  GateModal,
  budgetErrorText,
} from './atoms';

/* ------------------------------------------------------------ role change */

export function RoleChangeModal({
  member,
  nextRole,
  open,
  onOpenChange,
  onDone,
}: {
  member: ProjectMember;
  nextRole: Exclude<ProjectRole, 'owner'>;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onDone?: (member: ProjectMember) => void;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const invalidate = useInvalidateProject();
  const [busy, setBusy] = useState(false);
  const promote = nextRole === 'admin';

  const confirm = async () => {
    if (!project.client || !project.current) return;
    setBusy(true);
    try {
      const updated = await project.client.setMemberRole(
        project.current.id,
        member.user_id,
        nextRole,
      );
      onOpenChange(false);
      receipt(
        promote
          ? t('projects.role.promoted', { name: member.username })
          : t('projects.role.demoted', { name: member.username }),
      );
      await invalidate('members');
      onDone?.(updated);
    } catch (error) {
      errorReceipt(error);
    } finally {
      setBusy(false);
    }
  };

  return (
    <ActionModal
      busy={busy}
      busyLabel={t('projects.common.applying')}
      confirmLabel={
        promote
          ? t('projects.role.promoteConfirm')
          : t('projects.role.demoteConfirm')
      }
      description={
        promote
          ? t('projects.role.immediateNoSave')
          : t('projects.role.immediate')
      }
      onConfirm={confirm}
      onOpenChange={onOpenChange}
      open={open}
      title={
        promote
          ? t('projects.role.promoteTitle', { name: member.username })
          : t('projects.role.demoteTitle', { name: member.username })
      }
    >
      <ul className='pj-modal__list'>
        {promote ? (
          <>
            <li>{t('projects.role.promote1')}</li>
            <li>{t('projects.role.promote2')}</li>
            <li>{t('projects.role.promote3')}</li>
            <li>{t('projects.role.promote4')}</li>
          </>
        ) : (
          <>
            <li>{t('projects.role.demote1')}</li>
            <li>{t('projects.role.demote2')}</li>
          </>
        )}
      </ul>
    </ActionModal>
  );
}

/* ---------------------------------------------------------------- removal */

export function RemoveMemberModal({
  member,
  keys,
  open,
  onOpenChange,
  onRemoved,
}: {
  member: ProjectMember;
  keys: ProjectToken[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRemoved: (receipt: RemovalReceipt) => void;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const invalidate = useInvalidateProject();
  const [busy, setBusy] = useState(false);
  // Removal always stops the member's Keys in this project immediately —
  // there is no grace period and no policy to branch on.
  const enabledKeys = keys.filter((k) => k.status === 'enabled');

  const remove = async () => {
    if (!project.client || !project.current) return;
    setBusy(true);
    try {
      const result = await project.client.removeMember(
        project.current.id,
        member.user_id,
      );
      onOpenChange(false);
      await invalidate('members');
      await invalidate('tokens');
      onRemoved(result);
    } catch (error) {
      errorReceipt(error);
    } finally {
      setBusy(false);
    }
  };

  return (
    <GateModal
      busy={busy}
      busyLabel={t('projects.remove.removing')}
      confirmLabel={t('projects.remove.confirm', { name: member.username })}
      description={t('projects.remove.lede', { name: member.username })}
      gate={{
        type: 'checkbox',
        label: t('projects.remove.gate', {
          count: enabledKeys.length,
          name: member.username,
        }),
      }}
      onConfirm={remove}
      onOpenChange={onOpenChange}
      open={open}
      title={t('projects.remove.title', {
        name: member.username,
        project: project.currentName,
      })}
      bullets={[
        <span key='keys'>
          <b>{t('projects.remove.stopNow')}</b>{' '}
          {t('projects.remove.keys', {
            count: enabledKeys.length,
            name: member.username,
            names: enabledKeys.map((k) => k.name).join('、') || '—',
          })}
        </span>,
        t('projects.remove.budget', {
          budget: formatUSDShort(member.budget_microusd),
          name: member.username,
          spent: formatUSD(member.month_spend_microusd),
        }),
        t('projects.remove.history', {
          name: member.username,
          project: project.currentName,
        }),
        t('projects.remove.ownProject', { name: member.username }),
      ]}
    />
  );
}

export function UndoRemoveLine({
  member,
  receiptData,
  onUndone,
}: {
  member: ProjectMember;
  receiptData: RemovalReceipt;
  onUndone: () => void;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const invalidate = useInvalidateProject();
  const [busy, setBusy] = useState(false);
  const [left, setLeft] = useState(
    Math.max(0, receiptData.undo_until - Math.floor(Date.now() / 1000)),
  );
  useEffect(() => {
    if (left <= 0) return;
    const timer = window.setTimeout(() => setLeft((v) => v - 1), 1000);
    return () => window.clearTimeout(timer);
  }, [left]);
  const undo = async () => {
    if (!project.client || !project.current) return;
    setBusy(true);
    try {
      const result = await project.client.undoRemove(
        project.current.id,
        member.user_id,
        receiptData.removal_id,
      );
      await invalidate('members');
      await invalidate('tokens');
      receipt(
        result.skipped_key_ids.length
          ? t('projects.remove.undonePartial', {
              name: member.username,
              restored: result.restored_key_ids.length,
              skipped: result.skipped_key_ids.length,
            })
          : t('projects.remove.undone', {
              name: member.username,
              restored: result.restored_key_ids.length,
            }),
      );
      onUndone();
    } catch (error) {
      errorReceipt(error);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className='pj-undo'>
      <span className='pj-okt'>
        {t('projects.remove.done', {
          name: member.username,
          project: project.currentName,
          count: member.key_count,
        })}
      </span>
      {left > 0 ? (
        <Button
          loading={busy}
          onClick={() => void undo()}
          size='sm'
          variant='ghost'
        >
          {t('projects.remove.undo', {
            minutes: REMOVAL_UNDO_MINUTES,
            left: `${Math.floor(left / 60)}:${String(left % 60).padStart(2, '0')}`,
          })}
        </Button>
      ) : null}
    </div>
  );
}

/* --------------------------------------------------------- budget request */

export function HandleBudgetRequestModal({
  request,
  member,
  open,
  onOpenChange,
  allocationMicro,
}: {
  request: BudgetRequest;
  member?: ProjectMember | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  allocationMicro?: number;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const invalidate = useInvalidateProject();
  const [draft, setDraft] = useState<BudgetDraft>(
    draftFromMicro(request.requested_microusd),
  );
  const [mode, setMode] = useState<'approve' | 'reject'>('approve');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (open) {
      setDraft(draftFromMicro(request.requested_microusd));
      setMode('approve');
      setNote('');
    }
  }, [open, request.requested_microusd]);
  const validation = validateBudget(draft);
  const rangeError = budgetErrorText(draft, t);
  const current = member?.budget_microusd ?? null;
  const nextAllocation =
    allocationMicro !== undefined && validation.ok && validation.micro !== null
      ? allocationMicro - (current ?? 0) + validation.micro
      : null;

  const approve = async () => {
    if (!project.client || !project.current || !validation.ok) return;
    setBusy(true);
    try {
      await project.client.approveBudgetRequest(
        project.current.id,
        request.id,
        validation.micro,
      );
      onOpenChange(false);
      receipt(
        t('projects.request.approvedReceipt', {
          name: request.user.username,
          budget: formatUSDShort(validation.micro),
        }),
      );
      await invalidate('budget');
    } catch (error) {
      if (isLazuApiError(error) && error.code === 'state_conflict') {
        onOpenChange(false);
        await invalidate('budget');
      }
      errorReceipt(error);
    } finally {
      setBusy(false);
    }
  };
  const reject = async () => {
    if (!project.client || !project.current || !validateBudgetRequestNote(note))
      return;
    setBusy(true);
    try {
      await project.client.rejectBudgetRequest(
        project.current.id,
        request.id,
        note.trim(),
      );
      onOpenChange(false);
      receipt(
        t('projects.request.rejectedReceipt', { name: request.user.username }),
      );
      await invalidate('budget');
    } catch (error) {
      if (isLazuApiError(error) && error.code === 'state_conflict') {
        onOpenChange(false);
        await invalidate('budget');
      }
      errorReceipt(error);
    } finally {
      setBusy(false);
    }
  };

  if (mode === 'reject') {
    return (
      <ActionModal
        busy={busy}
        confirmLabel={t('projects.request.reject')}
        danger
        description={t('projects.request.rejectLede', {
          name: request.user.username,
        })}
        disabled={!validateBudgetRequestNote(note)}
        onConfirm={reject}
        onOpenChange={onOpenChange}
        open={open}
        title={t('projects.request.rejectTitle', {
          name: request.user.username,
        })}
        extraFooter={
          <Button
            disabled={busy}
            onClick={() => setMode('approve')}
            variant='ghost'
          >
            {t('projects.common.back')}
          </Button>
        }
      >
        <Textarea
          disabled={busy}
          onChange={(e) => setNote(e.target.value)}
          placeholder={t('projects.request.rejectPlaceholder')}
          rows={3}
          value={note}
        />
        {note.trim() && !validateBudgetRequestNote(note) ? (
          <ErrLine>
            {t('projects.request.noteTooShort', {
              min: BUDGET_REQUEST_NOTE_MIN,
            })}
          </ErrLine>
        ) : null}
      </ActionModal>
    );
  }

  return (
    <ActionModal
      busy={busy}
      confirmLabel={t('projects.request.approveAndSet')}
      description={t('projects.request.handleLede', {
        current: formatUSDShort(current),
        spent: formatUSD(member?.month_spend_microusd ?? 0),
      })}
      disabled={!validation.ok}
      extraFooter={
        <Button
          disabled={busy}
          onClick={() => setMode('reject')}
          variant='ghost'
        >
          {t('projects.request.rejectEllipsis')}
        </Button>
      }
      onConfirm={approve}
      onOpenChange={onOpenChange}
      open={open}
      title={t('projects.request.handleTitle', {
        name: request.user.username,
        amount: formatUSDShort(request.requested_microusd),
      })}
    >
      <FormRows labelWidth={90}>
        <FormRow
          label={t('projects.request.changeTo')}
          hint={
            rangeError ||
            (nextAllocation !== null
              ? t('projects.request.allocationHint', {
                  from: formatUSDShort(allocationMicro ?? 0),
                  to: formatUSDShort(nextAllocation),
                })
              : t('projects.budget.monthlyFromNow'))
          }
        >
          <BudgetPicker
            allowUnlimited={false}
            disabled={busy}
            draft={draft}
            onChange={setDraft}
            quick={[80, 150]}
            size='sm'
          />
        </FormRow>
      </FormRows>
      <p className='pj-muted pj-modal__quote'>「{request.reason}」</p>
    </ActionModal>
  );
}
