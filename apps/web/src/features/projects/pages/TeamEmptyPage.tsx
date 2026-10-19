/* 成员与预算 before there is anyone else.
 *
 * The page a solo owner lands on is the page they will keep: the same members
 * table the shared project shows, holding the one row that exists. Under it,
 * the row that adds the next one — address, role, cap, invite — and one greyed
 * row showing where they land. That replaces a card of three explanatory
 * tiles, a before/after diagram and a 620px two-column dialog, because none of
 * that answered "how do I add someone"; it was an essay about what would
 * happen if I did.
 *
 * The table is the shared `Card` + `.pj-table` pair the members page itself
 * composes, so the two states of this route share one row geometry and this
 * page inherits every later fix to it.
 */
import { Check, Pencil, X } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Input, Pending } from '../../../components/ui';
import ConsolePage from '../../../components/ui/console-page';
import { canInviteRole } from '../access';
import { INVITE_VALID_DAYS, draftFromMicro, validateBudget } from '../budget';
import { Card, SegControl } from '../components/atoms';
import { useInviteDraft, type InviteRole } from '../components/InviteForm';
import { MICRO, formatUSD, formatUSDShort } from '../money';
import { useProject } from '../ProjectContext';
import {
  useInvalidateProject,
  useProjectDetail,
  useProjectMembers,
} from '../queries';
import { errorReceipt, receipt } from '../receipt';

const initial = (name: string) => (name.trim()[0] || '?').toUpperCase();

const CAPS = [50, 100, 500];

/* The project's name used to be a field inside the first-invite dialog, which
 * is a strange place to meet it: you came to invite someone, not to name
 * something. It lives on the head instead, editable in place — and only once
 * the detail query has handed over the version a write needs, so an early
 * Save cannot quietly do nothing. */
function ProjectName({ version }: { version?: number }) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const invalidate = useInvalidateProject();
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(project.currentName);
  const [busy, setBusy] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const ready = version !== undefined;

  useEffect(() => {
    if (editing) inputRef.current?.select();
  }, [editing]);

  const save = async () => {
    const next = value.trim();
    if (!next || next === project.currentName) {
      setEditing(false);
      setValue(project.currentName);
      return;
    }
    if (!project.client || !project.current || version === undefined) return;
    setBusy(true);
    try {
      await project.client.updateProject(project.current.id, {
        expected_version: version,
        name: next,
      });
      await invalidate('detail');
      await project.refreshProjects();
      receipt(t('projects.settings.renamedReceipt', { name: next }));
      setEditing(false);
    } catch (error) {
      errorReceipt(error);
    } finally {
      setBusy(false);
    }
  };

  if (!editing) {
    return (
      <button
        className='pj-rename'
        data-owns-chrome=''
        disabled={!ready}
        onClick={() => {
          setValue(project.currentName);
          setEditing(true);
        }}
        type='button'
      >
        {project.currentName}
        {ready ? <Pencil aria-hidden='true' size={11} /> : null}
      </button>
    );
  }

  return (
    <span className='pj-rename__edit'>
      <Input
        aria-label={t('projects.invite.projectName')}
        disabled={busy}
        onChange={(event) => setValue(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Enter') void save();
          if (event.key === 'Escape') {
            setEditing(false);
            setValue(project.currentName);
          }
        }}
        ref={inputRef}
        value={value}
      />
      <button
        aria-label={t('common.save', { defaultValue: '保存' })}
        className='pj-rename__act'
        data-owns-chrome=''
        disabled={busy}
        onClick={() => void save()}
        type='button'
      >
        <Check aria-hidden='true' size={14} />
      </button>
      <button
        aria-label={t('common.cancel', { defaultValue: '取消' })}
        className='pj-rename__act'
        data-owns-chrome=''
        disabled={busy}
        onClick={() => {
          setEditing(false);
          setValue(project.currentName);
        }}
        type='button'
      >
        <X aria-hidden='true' size={14} />
      </button>
    </span>
  );
}

export default function TeamEmptyPage() {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const detail = useProjectDetail();
  const members = useProjectMembers();
  const draft = useInviteDraft({
    active: true,
    detail: detail.data,
    first: true,
    // The name is edited on the head, so this form does not send one: passing
    // the draft's default would overwrite a rename made a moment earlier.
    namesProject: false,
    // Not "the request finished" but "we hold the project's settings": a read
    // that failed leaves us guessing at the cap this invitation would set, and
    // a guess is not something to persist on someone's behalf.
    ready: Boolean(detail.data),
  });
  const balanceMicro =
    detail.data?.balance_microusd ?? project.current?.balanceMicro ?? null;
  const balance = formatUSD(balanceMicro);
  const you = project.viewerName || 'You';

  /* The owner's own row is real data: a default project can already hold Keys
     and a month of spend, and printing $0.00 over them would be a lie the
     reader has no way to spot. */
  const owner = members.data?.find((m) => m.role === 'owner');

  const capValidation = validateBudget(draft.budget);
  // `micro` is null for "no cap", which is a state, not an amount.
  const capMicro =
    capValidation.ok && capValidation.micro !== null
      ? capValidation.micro
      : undefined;
  /* The project's configured default, remembered from the moment the draft was
     seeded. A project dissolved back to solo keeps its settings, so that value
     can be a custom amount or no cap at all — and it has to stay on offer
     after another preset is pressed, or the reader cannot put it back. */
  const extraCap = draft.seeded ? draft.initialBudget : undefined;
  const caps =
    extraCap == null || CAPS.some((usd) => usd * MICRO === extraCap)
      ? CAPS
      : [...CAPS, extraCap / MICRO].sort((a, b) => a - b);
  const offerUnlimited = draft.seeded && draft.initialBudget === null;

  const roleOptions = (['member', 'admin'] as InviteRole[]).map((value) => ({
    disabled: !canInviteRole(project.role, value),
    label: t(`projects.role.${value}`),
    value,
  }));

  const controlsDisabled = draft.busy || !draft.seeded;

  return (
    <ConsolePage
      appearance='review'
      className='pj-page pj-team-empty'
      description={
        <>
          <ProjectName version={detail.data?.version} /> ·{' '}
          {t('projects.members.ledeSolo', { balance })}
        </>
      }
      stats={
        <>
          <span className='console-stat'>
            <span className='console-stat__label'>
              {t('projects.members.statMembers')}
            </span>
            <span className='console-stat__value'>1</span>
          </span>
          <span className='console-stat'>
            <span className='console-stat__label'>
              {t('projects.members.statBalance')}
            </span>
            <span className='console-stat__value'>
              <Pending chars={6} pending={detail.isPending}>
                {balance}
              </Pending>
            </span>
          </span>
        </>
      }
      title={t('projects.members.title')}
    >
      <Card className='pj-table-card' pad={false}>
        <div className='pj-roster__scroll'>
          <table className='pj-table'>
            <thead>
              <tr>
                <th style={{ width: '32%' }}>
                  {t('projects.members.col.member')}
                </th>
                <th>{t('projects.members.col.role')}</th>
                <th className='pj-r'>{t('projects.members.col.budget')}</th>
                <th className='pj-r'>{t('projects.members.col.spent')}</th>
                <th className='pj-r'>{t('projects.members.col.keys')}</th>
              </tr>
            </thead>
            <tbody>
              <tr>
                <td>
                  <span className='pj-who'>
                    <span className='pj-av'>{initial(you)}</span>
                    <span className='pj-who__t'>
                      <strong>{you}</strong>
                      <span className='pj-sub'>
                        {t('projects.members.youTag')}
                      </span>
                    </span>
                  </span>
                </td>
                <td className='pj-muted'>{t('projects.role.owner')}</td>
                <td className='pj-r pj-muted'>
                  {t('projects.budget.unlimited')}
                </td>
                <td className='pj-r'>
                  <Pending chars={6} pending={detail.isPending}>
                    {detail.data
                      ? formatUSD(detail.data.month_spend_microusd)
                      : ''}
                  </Pending>
                </td>
                <td className='pj-r'>
                  <Pending chars={2} pending={members.isPending}>
                    {owner ? String(owner.key_count) : ''}
                  </Pending>
                </td>
              </tr>

              {/* The next member is added on the row where they will appear. */}
              <tr className='pj-row--invite'>
                <td colSpan={5}>
                  <form
                    className='pj-roster__invite'
                    onSubmit={async (event) => {
                      event.preventDefault();
                      if (!draft.canSend) return;
                      // On success the project becomes shared and the route
                      // swaps this page for the real members list.
                      await draft.send();
                    }}
                  >
                    <span aria-hidden='true' className='pj-roster__av-ghost' />
                    <Input
                      aria-label={t('projects.invite.who')}
                      className='pj-roster__email'
                      disabled={controlsDisabled}
                      error={Boolean(draft.emailError || draft.serverError)}
                      inputMode='email'
                      onChange={(event) => draft.setEmail(event.target.value)}
                      placeholder={t('projects.invite.inlinePlaceholder')}
                      type='email'
                      value={draft.email}
                    />
                    <SegControl
                      ariaLabel={t('projects.invite.role')}
                      disabled={controlsDisabled}
                      onChange={draft.setRole}
                      options={roleOptions}
                      value={draft.role}
                    />
                    {/* Three caps, no free-text field: the first invitation is
                        a decision about someone joining, not about a number,
                        and the cap is editable on this same table afterwards.
                        Whatever the project already configured joins them, so
                        nothing is sent that the reader cannot see. */}
                    <span className='pj-roster__caps'>
                      {caps.map((usd) => (
                        <button
                          className='pj-chip'
                          data-on={
                            draft.seeded && capMicro === usd * MICRO
                              ? ''
                              : undefined
                          }
                          data-owns-chrome=''
                          disabled={controlsDisabled}
                          key={usd}
                          onClick={() =>
                            draft.setBudget(draftFromMicro(usd * MICRO))
                          }
                          type='button'
                        >
                          {formatUSDShort(usd * MICRO)}
                        </button>
                      ))}
                      {offerUnlimited ? (
                        <button
                          className='pj-chip'
                          data-on={
                            draft.budget.mode === 'unlimited' ? '' : undefined
                          }
                          data-owns-chrome=''
                          disabled={controlsDisabled}
                          onClick={() => draft.setBudget({ mode: 'unlimited' })}
                          type='button'
                        >
                          {t('projects.budget.unlimited')}
                        </button>
                      ) : null}
                    </span>
                    <Button
                      disabled={!draft.canSend}
                      loading={draft.busy}
                      size='sm'
                      type='submit'
                      variant='primary'
                    >
                      {t('projects.invite.sendShort')}
                    </Button>
                  </form>
                  {draft.emailError ||
                  draft.serverError ||
                  draft.budgetError ? (
                    <p className='pj-roster__err'>
                      {draft.emailError ||
                        draft.serverError ||
                        draft.budgetError}
                    </p>
                  ) : detail.isError ? (
                    <p className='pj-roster__err'>
                      {t('projects.empty.settingsUnavailable')}
                    </p>
                  ) : null}
                </td>
              </tr>

              {/* Where the next one lands. Not a control, not a loading state —
                  the shape of the row, drawn quietly. */}
              <tr aria-hidden='true' className='pj-roster__ghost'>
                <td>
                  <span className='pj-who'>
                    <span className='pj-roster__av-ghost' />
                    <span className='pj-roster__bar' style={{ width: 96 }} />
                  </span>
                </td>
                <td>
                  <span className='pj-roster__bar' style={{ width: 40 }} />
                </td>
                <td className='pj-r'>
                  <span className='pj-roster__bar' style={{ width: 44 }} />
                </td>
                <td className='pj-r'>
                  <span className='pj-roster__bar' style={{ width: 44 }} />
                </td>
                <td className='pj-r'>
                  <span className='pj-roster__bar' style={{ width: 14 }} />
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </Card>

      <p className='pj-roster__foot'>
        {t('projects.empty.footNote', { balance, days: INVITE_VALID_DAYS })}
      </p>
    </ConsolePage>
  );
}
