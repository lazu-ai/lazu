import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '../../components/ui/button';

type SaveGroup = {
  title: string;
  count: number;
  busy: boolean;
  validate: () => Promise<boolean>;
  save: () => Promise<unknown>;
  reset: () => void;
};
type Registry = {
  register: (id: string, group: SaveGroup | null) => void;
  navigationBlocked: boolean;
  /** Runs the same save the bar's button runs. Resolves true when every dirty
   *  group persisted, so a caller can continue the action it interrupted. */
  saveAll: () => Promise<boolean>;
};
const SettingsSaveContext = createContext<Registry | null>(null);

/** One save/revert pair for all mounted settings groups. Validation completes
 * before any write; a failed group keeps its draft and stops subsequent writes. */
export function SettingsHeaderActionsProvider({
  children,
}: {
  children: (actions: ReactNode) => ReactNode;
}) {
  const { t } = useTranslation();
  const [groups, setGroups] = useState<Record<string, SaveGroup>>({});
  const [saving, setSaving] = useState(false);
  const register = useCallback(
    (id: string, group: SaveGroup | null) =>
      setGroups((current) => {
        const next = { ...current };
        if (group) next[id] = group;
        else delete next[id];
        return next;
      }),
    [],
  );
  const dirty = Object.values(groups).filter((group) => group.count > 0);
  const count = dirty.reduce((sum, group) => sum + group.count, 0);
  const busy = saving || Object.values(groups).some((group) => group.busy);
  const saveAll = async () => {
    setSaving(true);
    try {
      const validity = await Promise.all(
        dirty.map((group) => group.validate()),
      );
      if (validity.some((valid) => !valid)) return false;
      for (const group of dirty) await group.save();
      return true;
    } catch {
      // Each mutation owns its actionable error message; leave all drafts intact.
      return false;
    } finally {
      setSaving(false);
    }
  };
  /* Unsaved work follows the reader instead of waiting at the bottom of the
     page. The previous bar was rendered after the last group with no styles of
     its own, so on a long tab the save button sat below everything and was
     reachable only by scrolling past every setting. */
  const actions = count ? (
    <div className='set-unsaved' aria-busy={busy} role='status'>
      <span className='set-unsaved__count'>
        {t('page.console.settingV2.runtime.unsavedCount', {
          count,
          defaultValue: '{{count}} 项未保存',
        })}
      </span>
      <Button
        disabled={busy}
        size='sm'
        variant='ghost'
        onClick={() => dirty.forEach((group) => group.reset())}
      >
        {t('page.console.settingV2.runtime.reset')}
      </Button>
      <Button loading={busy} size='sm' onClick={() => void saveAll()}>
        {t('page.console.settingV2.runtime.save')}
      </Button>
    </div>
  ) : null;
  return (
    <SettingsSaveContext.Provider
      value={{ register, navigationBlocked: count > 0 || busy, saveAll }}
    >
      {children(actions)}
    </SettingsSaveContext.Provider>
  );
}

export function useSettingsSaveGroup(group: SaveGroup) {
  const context = useContext(SettingsSaveContext);
  const register = context?.register;
  const id = useId();
  const latest = useRef(group);
  latest.current = group;
  useEffect(() => {
    if (!register) return;
    register(id, {
      ...group,
      validate: () => latest.current.validate(),
      save: () => latest.current.save(),
      reset: () => latest.current.reset(),
    });
    return () => register(id, null);
  }, [register, id, group.count, group.busy, group.title]);
  return Boolean(context);
}

/** Tier navigation unmounts the registry; do not silently discard pending work. */
export function useSettingsNavigationBlocked() {
  return useContext(SettingsSaveContext)?.navigationBlocked ?? false;
}

/** Save from outside the bar — the tab guard offers "save and switch". */
export function useSettingsSaveAll() {
  return useContext(SettingsSaveContext)?.saveAll;
}
