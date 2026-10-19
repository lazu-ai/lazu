/* One tiny signal: "the set of projects this user belongs to changed". The
 * scope provider re-derives the switcher from it; pages re-read self. */
import { useEffect, useState } from 'react';
import { isProjectsMockEnabled } from './flag';
import type { ProjectSummary } from './types';

const target = new EventTarget();
const EVENT = 'lazu:projects-changed';

export const announceProjectsChanged = () => {
  target.dispatchEvent(new Event(EVENT));
};

export const useProjectsChangedTick = () => {
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const bump = () => setTick((value) => value + 1);
    target.addEventListener(EVENT, bump);
    return () => target.removeEventListener(EVENT, bump);
  }, []);
  return tick;
};

/** `projects[]` for the session. Real mode reads the self payload; mock mode
 *  asks the in-memory store (loaded lazily, so the first render may be empty
 *  until `announceProjectsChanged` fires). */
let mockProjectsReader: (() => ProjectSummary[]) | null = null;

export const registerMockProjectsReader = (reader: () => ProjectSummary[]) => {
  mockProjectsReader = reader;
  announceProjectsChanged();
};

export const readSessionProjects = (user: unknown): ProjectSummary[] => {
  if (isProjectsMockEnabled()) {
    return mockProjectsReader ? mockProjectsReader() : [];
  }
  if (!user || typeof user !== 'object') return [];
  const projects = (user as { projects?: unknown }).projects;
  return Array.isArray(projects) ? (projects as ProjectSummary[]) : [];
};
