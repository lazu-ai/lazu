/* The project switcher (S9). It is shown whenever the switch is on, even with
 * a single project: this is the only place the console says out loud that you
 * are standing in a project at all, and a solo owner who never sees it has no
 * way to learn that the thing has a name, a balance and room for other people.
 * Switching only changes what the console shows; it never changes what a Key
 * charges.
 *
 * Two shapes. `sidebar` is the bordered card it began as. `topbar` is the
 * first crumb of the console's "<project> › <group>" line: the project's name
 * is said once, where the reader looks to learn where they are, instead of
 * twice — once in a card above the nav and once in the bar. The menu is the
 * same in both, and gains the two things the card could not hold: a way to
 * start sharing, and the project's settings. */
import { Check, ChevronDown, Plus, Settings2 } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation, useNavigate } from 'react-router-dom';
import {
  Button,
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '../../../components/ui';
import { formatUSD } from '../money';
import { useProject } from '../ProjectContext';
import { useMyBudget } from '../queries';
import { receipt } from '../receipt';
import type { ProjectSummary } from '../types';

const initial = (name: string) => (name.trim()[0] || '?').toUpperCase();

export function ProjectSwitcher({
  onNavigate,
  variant = 'sidebar',
}: {
  onNavigate?: () => void;
  variant?: 'sidebar' | 'topbar';
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const navigate = useNavigate();
  const location = useLocation();
  const {
    projects,
    current,
    currentName,
    role,
    dirtyEntries,
    switchProject,
    enabled,
    isOwner,
    isSoloOwner,
  } = useProject();
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState<ProjectSummary | null>(null);
  const [switching, setSwitching] = useState<ProjectSummary | null>(null);
  const [focusIndex, setFocusIndex] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);
  const myBudget = useMyBudget({
    enabled: enabled && Boolean(current?.isShared) && role === 'member',
  });

  useEffect(() => {
    if (!open) return;
    const index = Math.max(
      0,
      projects.findIndex((project) => project.id === current?.id),
    );
    setFocusIndex(index);
    requestAnimationFrame(() => {
      listRef.current
        ?.querySelectorAll<HTMLButtonElement>('[data-project-item]')
        [index]?.focus();
    });
  }, [current?.id, open, projects]);

  if (!enabled || !current) return null;

  const currentProject = projects.find((project) => project.id === current.id);

  const metaFor = (project: ProjectSummary, isCurrent: boolean) => {
    if (project.upgraded_at === null) {
      return t('projects.switcher.ownMeta', {
        balance: formatUSD(project.balance_microusd),
      });
    }
    if (project.role === 'owner') {
      return t('projects.switcher.ownerMeta', { count: project.member_count });
    }
    const remaining =
      isCurrent && role === 'member' && myBudget.data
        ? myBudget.data.remaining_microusd === null
          ? t('projects.switcher.budgetUnlimited')
          : t('projects.switcher.budgetLeft', {
              amount: formatUSD(myBudget.data.remaining_microusd),
            })
        : '';
    return [
      t('projects.switcher.payerMeta', { owner: project.owner.username }),
      remaining,
    ]
      .filter(Boolean)
      .join(' · ');
  };

  const finishSwitch = (project: ProjectSummary, discardDirty = false) => {
    setSwitching(project);
    const result = switchProject(project.id, discardDirty);
    if (result === 'switched') {
      if (location.pathname !== '/console') navigate('/console');
      onNavigate?.();
      receipt(
        t('projects.switcher.switched', {
          name: project.name,
          balance: formatUSD(project.balance_microusd),
        }),
      );
    }
    setSwitching(null);
    setOpen(false);
    setPending(null);
  };

  const choose = (project: ProjectSummary) => {
    if (project.id === current.id) {
      setOpen(false);
      return;
    }
    if (dirtyEntries.length) {
      setPending(project);
      return;
    }
    finishSwitch(project);
  };

  const saveThenSwitch = async (project: ProjectSummary) => {
    setSwitching(project);
    try {
      for (const entry of dirtyEntries) {
        if (!entry.save) throw new Error('Unsaved entry cannot be saved');
        await entry.save();
      }
    } catch {
      setSwitching(null);
      return;
    }
    finishSwitch(project, true);
  };

  const onKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
    event.preventDefault();
    const next =
      event.key === 'ArrowDown'
        ? (focusIndex + 1) % projects.length
        : (focusIndex - 1 + projects.length) % projects.length;
    setFocusIndex(next);
    listRef.current
      ?.querySelectorAll<HTMLButtonElement>('[data-project-item]')
      [next]?.focus();
  };

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setPending(null);
      }}
    >
      <PopoverTrigger>
        {variant === 'topbar' ? (
          <button
            aria-expanded={open}
            aria-haspopup='listbox'
            className='pj-crumb'
            data-owns-chrome=''
            type='button'
          >
            <span
              className='pj-crumb__sq'
              data-shared={current.isShared || undefined}
            >
              {initial(currentName)}
            </span>
            <b className='pj-crumb__name'>{currentName}</b>
            <ChevronDown
              aria-hidden='true'
              className='pj-crumb__chev'
              size={14}
            />
          </button>
        ) : (
          <button
            aria-haspopup='listbox'
            aria-expanded={open}
            className='pj-space'
            data-owns-chrome=''
            type='button'
          >
            <span
              className='pj-space__sq'
              data-shared={current.isShared || undefined}
            >
              {initial(currentName)}
            </span>
            <span className='pj-space__t'>
              <b>{currentName}</b>
              <small>
                {currentProject ? metaFor(currentProject, true) : ''}
              </small>
            </span>
            <ChevronDown
              aria-hidden='true'
              className='pj-space__chev'
              size={14}
            />
          </button>
        )}
      </PopoverTrigger>
      <PopoverContent
        align='start'
        className='pj-menu'
        side='bottom'
        sideOffset={6}
      >
        {pending ? (
          <div className='pj-pop'>
            <p>
              {t('projects.switcher.unsavedPrompt', {
                name: currentName,
                what: dirtyEntries.map((entry) => entry.label).join('、'),
              })}
            </p>
            <div className='pj-pop__acts'>
              <Button
                size='sm'
                variant='secondary'
                disabled={Boolean(switching)}
                onClick={() => finishSwitch(pending, true)}
              >
                {t('projects.switcher.discardAndSwitch')}
              </Button>
              <Button
                size='sm'
                variant='primary'
                loading={Boolean(switching)}
                disabled={dirtyEntries.some((entry) => !entry.save)}
                onClick={() => void saveThenSwitch(pending)}
              >
                {t('projects.switcher.saveFirst')}
              </Button>
            </div>
          </div>
        ) : (
          <div ref={listRef} role='listbox' onKeyDown={onKeyDown} tabIndex={-1}>
            <div className='pj-menu__eb pj-menu__eb--head'>
              {t('projects.switcher.heading')}
            </div>
            {projects.map((project, index) => {
              const isCurrent = project.id === current.id;
              return (
                <button
                  aria-selected={isCurrent}
                  className='pj-menu__it'
                  data-on={isCurrent || undefined}
                  data-project-item=''
                  data-owns-chrome=''
                  key={project.id}
                  onClick={() => choose(project)}
                  role='option'
                  tabIndex={index === focusIndex ? 0 : -1}
                  type='button'
                >
                  <span
                    className='pj-space__sq'
                    data-shared={project.upgraded_at !== null || undefined}
                  >
                    {initial(project.name)}
                  </span>
                  <span className='pj-space__t'>
                    <b>{project.name}</b>
                    <small>{metaFor(project, isCurrent)}</small>
                  </span>
                  {isCurrent ? <Check aria-hidden='true' size={14} /> : null}
                </button>
              );
            })}
            <div className='pj-menu__rule' />
            {isSoloOwner ? (
              <button
                className='pj-menu__it pj-menu__start'
                data-owns-chrome=''
                onClick={() => {
                  setOpen(false);
                  navigate('/console/project/members');
                  onNavigate?.();
                }}
                type='button'
              >
                <span aria-hidden='true' className='pj-menu__plus'>
                  <Plus size={14} />
                </span>
                <span className='pj-space__t'>
                  <b>{t('projects.switcher.start')}</b>
                </span>
              </button>
            ) : null}
            {/* Project settings only exist once a project is shared: the
                route sends every unshared project back to the overview, so
                offering it to a solo owner is a dead end. */}
            {isOwner && current.isShared ? (
              <button
                className='pj-menu__it pj-menu__quiet'
                data-owns-chrome=''
                onClick={() => {
                  setOpen(false);
                  navigate('/console/project/settings');
                  onNavigate?.();
                }}
                type='button'
              >
                <span aria-hidden='true' className='pj-menu__glyph'>
                  <Settings2 size={14} />
                </span>
                <span className='pj-space__t'>
                  <b>{t('projects.nav.settings')}</b>
                </span>
              </button>
            ) : null}
            {!isSoloOwner && projects.length < 2 ? (
              <div className='pj-menu__hint'>
                {t('projects.switcher.missingHint')}
              </div>
            ) : null}
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
