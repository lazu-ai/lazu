/* The `projects` entry switch. It comes from `/api/user/self`
 * (`features.projects`, missing = false). Local development can run the
 * console against the in-memory mock by setting `VITE_PROJECTS_MOCK=1`; the
 * switch then reads true without a backend. This only governs which entries
 * exist — every action is still checked by the backend matrix. */

export const MOCK_SCENARIO_STORAGE_KEY = 'lazu:projects-mock-scenario';

export type MockScenario = 'solo' | 'owner' | 'member' | 'admin';

export const isProjectsMockEnabled = (): boolean => {
  try {
    return import.meta.env.DEV && import.meta.env.VITE_PROJECTS_MOCK === '1';
  } catch {
    return false;
  }
};

export const readMockScenario = (): MockScenario => {
  try {
    const stored = localStorage.getItem(MOCK_SCENARIO_STORAGE_KEY);
    if (
      stored === 'solo' ||
      stored === 'owner' ||
      stored === 'member' ||
      stored === 'admin'
    ) {
      return stored;
    }
  } catch {
    /* storage unavailable */
  }
  return 'owner';
};

export const writeMockScenario = (scenario: MockScenario) => {
  try {
    localStorage.setItem(MOCK_SCENARIO_STORAGE_KEY, scenario);
  } catch {
    /* storage unavailable */
  }
};

/** `features.projects` from a self payload; anything but `true` is off. */
export const readProjectsFlag = (user: unknown): boolean => {
  if (isProjectsMockEnabled()) return true;
  if (!user || typeof user !== 'object') return false;
  const features = (user as { features?: unknown }).features;
  if (!features || typeof features !== 'object') return false;
  return (features as { projects?: unknown }).projects === true;
};
