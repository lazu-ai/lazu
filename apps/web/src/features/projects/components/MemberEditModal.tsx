/* 编辑成员 — the small centered dialog the members list opens from a row's
 * 「编辑」. Three things only: monthly budget, allowed models and the
 * reasoning-effort cap, saved together. Role, suspension and removal stay in
 * the row's ··· menu, each with its own confirmation. Locked 2026-09-28 in
 * brand/foundations/team-projects-2026-09.md § Member edit dialog. */
import { Search } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Checkbox,
  EffortCapControl,
  Input,
  Modal,
  PendingValue,
  PendingBlock,
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '../../../components/ui';
import { useDelayedReveal } from '../../../hooks/common/useDelayedReveal';
import { errorMessage, isLazuApiError } from '../../../lib/api-error';
import {
  draftEquals,
  draftFromMicro,
  validateBudget,
  type BudgetDraft,
} from '../budget';
import { formatUSD, percentOf } from '../money';
import { useProject } from '../ProjectContext';
import { useInvalidateProject, useProjectModels } from '../queries';
import { receipt } from '../receipt';
import { type ProjectMember } from '../types';
import { BudgetPicker, ErrLine, SelectLike, budgetErrorText } from './atoms';

const sameModels = (a: string[], b: string[]) =>
  a.length === b.length &&
  [...a].sort().join('\n') === [...b].sort().join('\n');

export function MemberEditModal({
  member,
  open,
  onOpenChange,
}: {
  member: ProjectMember;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const invalidate = useInvalidateProject();
  const models = useProjectModels({ enabled: open });
  const [budget, setBudget] = useState<BudgetDraft>(() =>
    draftFromMicro(member.budget_microusd),
  );
  const [allowed, setAllowed] = useState<string[]>(member.allowed_models ?? []);
  const [cap, setCap] = useState(member.reasoning_effort_cap ?? '');
  const [picking, setPicking] = useState(false);
  const [query, setQuery] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!open) return;
    setBudget(draftFromMicro(member.budget_microusd));
    setAllowed(member.allowed_models ?? []);
    setCap(member.reasoning_effort_cap ?? '');
    setError('');
  }, [open, member.version]); // eslint-disable-line react-hooks/exhaustive-deps

  const validation = validateBudget(budget);
  const budgetError = budgetErrorText(budget, t);
  const dirty =
    !draftEquals(budget, draftFromMicro(member.budget_microusd)) ||
    !sameModels(allowed, member.allowed_models ?? []) ||
    cap !== (member.reasoning_effort_cap ?? '');

  const waiting = models.isPending;
  const reveal = useDelayedReveal(open && waiting);
  const catalog = models.data;
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return q
      ? (catalog ?? []).filter((m) => m.toLowerCase().includes(q))
      : (catalog ?? []);
  }, [catalog, query]);

  const remaining =
    member.budget_microusd === null
      ? null
      : Math.max(0, member.budget_microusd - member.month_spend_microusd);
  const used = percentOf(member.month_spend_microusd, member.budget_microusd);

  const save = async () => {
    if (!project.client || !project.current || !validation.ok) return;
    setBusy(true);
    setError('');
    try {
      await project.client.setMemberBudget(project.current.id, member.user_id, {
        budget_microusd: validation.micro,
        warning_pct: member.warning_pct,
        expected_version: member.version,
        allowed_models: allowed,
        reasoning_effort_cap: cap,
      });
      await invalidate('members');
      await invalidate('tokens');
      receipt(t('projects.memberEdit.saved', { name: member.username }));
      onOpenChange(false);
    } catch (e) {
      if (isLazuApiError(e) && e.code === 'version_conflict') {
        await invalidate('members');
        setError(errorMessage(e));
      } else {
        setError(t('projects.memberEdit.saveFailed'));
      }
    } finally {
      setBusy(false);
    }
  };

  const modelsLabel = allowed.length ? (
    t('projects.memberEdit.modelsLimited', { count: allowed.length })
  ) : waiting ? (
    <span style={{ visibility: reveal ? 'visible' : 'hidden' }}>
      <PendingValue chars={16} />
    </span>
  ) : models.isError ? (
    t('projects.common.loadFailed')
  ) : (
    t('projects.memberEdit.modelsAll', { count: catalog?.length })
  );

  return (
    <Modal
      busy={busy}
      className='pj-modal pj-modal--member'
      closeLabel={t('common.close', { defaultValue: '关闭' })}
      description={t('projects.memberEdit.lede', {
        email: member.email,
        role: t(`projects.role.${member.role}`),
      })}
      onOpenChange={onOpenChange}
      open={open}
      title={t('projects.memberEdit.title', { name: member.username })}
      body={
        <div className='pj-medit'>
          <div className='pj-medit__f'>
            <div className='pj-medit__lb'>
              <b>{t('projects.memberEdit.budget')}</b>
              {remaining !== null ? (
                <span>
                  {t('projects.memberEdit.budgetLeft', {
                    left: formatUSD(remaining),
                    pct: used ?? 0,
                  })}
                </span>
              ) : null}
            </div>
            <BudgetPicker
              draft={budget}
              label={t('projects.memberEdit.budget')}
              onChange={setBudget}
              quick={[30, 100]}
              size='sm'
            />
            <p className='pj-medit__hint'>
              {budgetError || t('projects.memberEdit.budgetHint')}
            </p>
          </div>

          <div className='pj-medit__f'>
            <div className='pj-medit__lb'>
              <b>{t('projects.memberEdit.models')}</b>
            </div>
            <Popover open={picking} onOpenChange={setPicking}>
              <PopoverTrigger>
                <SelectLike
                  aria-label={t('projects.memberEdit.models')}
                  style={{ width: '100%' }}
                >
                  {modelsLabel}
                </SelectLike>
              </PopoverTrigger>
              <PopoverContent align='start' className='pj-menu pj-medit__menu'>
                <div className='pj-medit__search'>
                  <Search aria-hidden='true' size={13} />
                  <Input
                    aria-label={t('projects.memberEdit.searchModels')}
                    onChange={(e) => setQuery(e.target.value)}
                    placeholder={t('projects.memberEdit.searchModels')}
                    value={query}
                  />
                </div>
                <button
                  className='pj-menu__it'
                  data-on={!allowed.length || undefined}
                  data-owns-chrome=''
                  onClick={() => setAllowed([])}
                  type='button'
                  disabled={!catalog}
                >
                  {waiting ? (
                    <span style={{ visibility: reveal ? 'visible' : 'hidden' }}>
                      <PendingValue chars={16} />
                    </span>
                  ) : models.isError ? (
                    t('projects.common.loadFailed')
                  ) : (
                    t('projects.memberEdit.modelsAll', {
                      count: catalog?.length,
                    })
                  )}
                </button>
                <div
                  className='pj-medit__list'
                  style={{ height: 224, overflowY: 'auto' }}
                >
                  {waiting ? (
                    <div style={{ visibility: reveal ? 'visible' : 'hidden' }}>
                      <PendingBlock height={224} />
                    </div>
                  ) : models.isError ? (
                    <ErrLine>{t('projects.common.loadFailed')}</ErrLine>
                  ) : null}
                  {filtered.map((name) => {
                    const on = allowed.includes(name);
                    return (
                      <label className='pj-medit__opt' key={name}>
                        <Checkbox
                          checked={on}
                          onCheckedChange={(next) =>
                            setAllowed((current) =>
                              next
                                ? [...current, name]
                                : current.filter((m) => m !== name),
                            )
                          }
                        />
                        <span className='pj-mono'>{name}</span>
                      </label>
                    );
                  })}
                </div>
              </PopoverContent>
            </Popover>
          </div>

          <div className='pj-medit__f'>
            <div className='pj-medit__lb'>
              <b>{t('projects.memberEdit.effort')}</b>
              {member.effort_downed_month ? (
                <span>
                  {t('projects.memberEdit.effortDowned', {
                    count: member.effort_downed_month,
                  })}
                </span>
              ) : null}
            </div>
            <EffortCapControl
              aria-label={t('projects.memberEdit.effort')}
              onChange={setCap}
              size='lg'
              value={cap}
            />
            <p className='pj-medit__hint'>
              {t('projects.memberEdit.effortHint', { name: member.username })}
            </p>
          </div>
          {error ? <ErrLine>{error}</ErrLine> : null}
        </div>
      }
      footer={
        <div className='pj-modal__foot pj-medit__foot'>
          <span className='pj-medit__note'>
            {t('projects.memberEdit.footNote')}
          </span>
          <Button
            disabled={busy}
            onClick={() => onOpenChange(false)}
            variant='secondary'
          >
            {t('common.cancel', { defaultValue: '取消' })}
          </Button>
          <Button
            disabled={!dirty || !validation.ok}
            loading={busy}
            onClick={() => void save()}
            variant='primary'
          >
            {t('common.save', { defaultValue: '保存' })}
          </Button>
        </div>
      }
    />
  );
}
