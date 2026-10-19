/* The invitation, as one draft shared by two surfaces.
 *
 * A solo owner's first invitation is a form on the 成员与预算 page itself —
 * it is the only thing that page asks of them, so it does not hide behind a
 * button. Every later invitation is the slim dialog. Both edit the same
 * draft: address validated as typed against members and pending invitations
 * (S3.1), role limited to what the inviter may hand out, budget as a cap. */
import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Input } from '../../../components/ui';
import { isLazuApiError } from '../../../lib/api-error';
import { canInviteRole } from '../access';
import {
  INVITE_VALID_DAYS,
  draftFromMicro,
  validateBudget,
  type BudgetDraft,
} from '../budget';
import { useProject } from '../ProjectContext';
import {
  useInvalidateProject,
  useProjectInvites,
  useProjectMembers,
} from '../queries';
import { describeError, errorReceipt, receipt } from '../receipt';
import { type ProjectDetail, type ProjectRole } from '../types';
import { BudgetPicker, ErrLine, SegControl, budgetErrorText } from './atoms';

export type InviteRole = Exclude<ProjectRole, 'owner'>;

export function useInviteDraft({
  active,
  first = false,
  namesProject = false,
  ready = true,
  detail,
  onInvited,
}: {
  /** The draft resets each time this turns true (a dialog opening). */
  active: boolean;
  /** Invitation into a project that is still unshared (R2): its member and
   *  invitation lists do not exist yet, so they are not asked for. */
  first?: boolean;
  /** Whether this surface also names the project as it upgrades it. The
   *  dialog does; the members page does not, because the name is edited on
   *  its own head — and sending a stale default from here would silently
   *  overwrite a rename the owner just made. */
  namesProject?: boolean;
  /** False while the project's settings are still in flight. The draft holds
   *  off until then rather than seeding a guessed cap, and a late answer
   *  never wipes an address already being typed. */
  ready?: boolean;
  detail?: ProjectDetail | null;
  onInvited?: () => void;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const invalidate = useInvalidateProject();
  const members = useProjectMembers({
    enabled: active && !first && project.isManager,
  });
  const invites = useProjectInvites({
    enabled: active && !first && project.isManager,
  });
  const projectId = project.current?.id;
  const defaultBudget = detail?.settings.default_member_budget_microusd;
  // One seed per project and opening. Names and defaults can refresh while
  // the owner is editing; neither is the identity of this draft.
  const seededFor = useRef<string | null>(null);
  const [seededProjectId, setSeededProjectId] = useState<string | null>(null);
  const [initialBudget, setInitialBudget] = useState<number | null>(null);
  const seeded = Boolean(
    active && ready && projectId && seededProjectId === projectId,
  );
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [role, setRole] = useState<InviteRole>('member');
  const [budget, setBudget] = useState<BudgetDraft>({
    mode: 'amount',
    input: '',
  });
  const [busy, setBusy] = useState(false);
  const [serverError, setServerError] = useState('');

  useEffect(() => {
    if (!active || !ready) {
      if (!active) {
        seededFor.current = null;
        setSeededProjectId(null);
      }
      return;
    }
    if (!projectId || defaultBudget === undefined) return;
    if (seededFor.current === projectId) return;
    seededFor.current = projectId;
    setSeededProjectId(projectId);
    setInitialBudget(defaultBudget);
    setName(
      namesProject
        ? `${project.viewerName} ${t('projects.invite.defaultNameSuffix')}`
        : project.currentName,
    );
    setEmail('');
    setRole('member');
    setBudget(draftFromMicro(defaultBudget));
    setBusy(false);
    setServerError('');
  }, [
    active,
    ready,
    namesProject,
    project.viewerName,
    projectId,
    project.currentName,
    defaultBudget,
    t,
  ]);

  const normalized = email.trim().toLowerCase();
  const emailError = useMemo(() => {
    if (!normalized) return '';
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(normalized))
      return t('projects.invite.emailInvalid');
    const existing = members.data?.find(
      (m) => m.email.toLowerCase() === normalized,
    );
    if (existing)
      return t('projects.invite.alreadyMember', {
        name: existing.username,
        project: project.currentName,
      });
    const pending = invites.data?.find(
      (i) => i.status === 'pending' && i.email.toLowerCase() === normalized,
    );
    if (pending) return t('projects.invite.alreadyInvited');
    return '';
  }, [invites.data, members.data, normalized, project.currentName, t]);

  const budgetValidation = validateBudget(budget);
  const budgetError = seeded ? budgetErrorText(budget, t) : '';
  const canSend =
    seeded &&
    Boolean(normalized) &&
    !emailError &&
    budgetValidation.ok &&
    (!namesProject || name.trim().length > 0) &&
    !busy;

  const send = async (): Promise<boolean> => {
    if (!canSend || !project.client || !project.current || !budgetValidation.ok)
      return false;
    setBusy(true);
    setServerError('');
    try {
      const invite = await project.client.createInvite(project.current.id, {
        email: normalized,
        role,
        budget_microusd: budgetValidation.micro,
        // Omitted, the server keeps the project's current name — which is
        // what a caller that does not offer the field intends.
        project_name: namesProject ? name.trim() : undefined,
      });
      receipt(
        t('projects.invite.sent', {
          email: invite.email,
          days: INVITE_VALID_DAYS,
        }),
      );
      if (first) await project.refreshProjects();
      await invalidate('invites');
      onInvited?.();
      return true;
    } catch (error) {
      if (
        isLazuApiError(error) &&
        (error.code === 'already_member' || error.code === 'already_invited')
      ) {
        /* Translate by code; the backend `message` is English and is only a
           fallback for codes we have no copy for. */
        setServerError(describeError(error));
      } else {
        errorReceipt(error);
      }
      return false;
    } finally {
      setBusy(false);
    }
  };

  const roleOptions = (['member', 'admin'] as InviteRole[]).map((value) => ({
    value,
    label: t(`projects.role.${value}`),
    disabled: !canInviteRole(project.role, value),
  }));

  return {
    budget,
    budgetError,
    initialBudget,
    busy,
    canSend,
    /* False until the draft holds the project's real settings. A surface that
       renders its own controls disables them until then, so nothing typed is
       thrown away and no cap is offered that the project did not configure. */
    seeded,
    email,
    emailError,
    name,
    role,
    roleOptions,
    send,
    serverError,
    setBudget,
    setEmail: (value: string) => {
      setEmail(value);
      setServerError('');
    },
    setName,
    setRole,
  };
}

export type InviteDraft = ReturnType<typeof useInviteDraft>;

/* One column: label, control, then the one hint that is worth a line. The
   two-column label grid this replaced gave every field a grey paragraph and
   made a four-field form read as fourteen lines. */
export function InviteFields({
  draft,
  idPrefix = 'pj-invite',
  namesProject = false,
}: {
  draft: InviteDraft;
  idPrefix?: string;
  /** Show the project-name field (the dialog that also upgrades a project). */
  namesProject?: boolean;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  return (
    <div className='pj-invite'>
      {namesProject ? (
        <div className='pj-invite__field'>
          <label className='pj-invite__label' htmlFor={`${idPrefix}-name`}>
            {t('projects.invite.projectName')}
          </label>
          <Input
            disabled={draft.busy || !draft.seeded}
            id={`${idPrefix}-name`}
            onChange={(e) => draft.setName(e.target.value)}
            value={draft.name}
          />
          <span className='pj-invite__hint'>
            {t('projects.invite.projectNameHint', {
              name: project.currentName,
            })}
          </span>
        </div>
      ) : null}

      <div className='pj-invite__field'>
        <label className='pj-invite__label' htmlFor={`${idPrefix}-email`}>
          {t('projects.invite.who')}
        </label>
        <Input
          disabled={draft.busy || !draft.seeded}
          error={Boolean(draft.emailError || draft.serverError)}
          id={`${idPrefix}-email`}
          inputMode='email'
          onChange={(e) => draft.setEmail(e.target.value)}
          placeholder='name@company.com'
          type='email'
          value={draft.email}
        />
        {draft.emailError || draft.serverError ? (
          <ErrLine>{draft.emailError || draft.serverError}</ErrLine>
        ) : null}
      </div>

      <div className='pj-invite__field'>
        <span className='pj-invite__label' id={`${idPrefix}-role`}>
          {t('projects.invite.role')}
        </span>
        <SegControl
          ariaLabel={t('projects.invite.role')}
          disabled={draft.busy || !draft.seeded}
          onChange={draft.setRole}
          options={draft.roleOptions}
          value={draft.role}
        />
        <span className='pj-invite__hint'>{t('projects.invite.roleHint')}</span>
      </div>

      <div className='pj-invite__field'>
        <span className='pj-invite__label'>{t('projects.invite.budget')}</span>
        <BudgetPicker
          disabled={draft.busy || !draft.seeded}
          draft={draft.budget}
          onChange={draft.setBudget}
          quick={[50, 100, 500]}
          size='sm'
        />
        {draft.budgetError ? (
          <ErrLine>{draft.budgetError}</ErrLine>
        ) : (
          <span className='pj-invite__hint'>
            {t('projects.invite.budgetHint')}
          </span>
        )}
      </div>
    </div>
  );
}
