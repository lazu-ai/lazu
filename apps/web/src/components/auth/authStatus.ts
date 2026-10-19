/* Which ways in are open — asked once, shared by everyone who asks.
 *
 * This used to be a plain `useEffect` inside the door, and the door rendered a
 * guess while the answer was outstanding: a null status read as "no email
 * code, password only", so the first thing a visitor saw was a username and
 * password form, and one round trip later it was replaced wholesale by the
 * real door — SSO buttons, the credential switch, an email field. Measured on
 * production: the wrong door painted at 80ms, the right one at 361ms, with a
 * 249px → 366px height jump animated in between. That is the "a screen appears
 * and then another screen appears" everyone felt.
 *
 * Two things follow from putting the answer outside React. The request can
 * start before the door mounts — a hovered sign-in link, a page that boots on
 * /login — so the answer is usually already here when the column first paints.
 * And callers can tell "not asked yet" from "asked and told", which is what
 * the door needs in order to wait instead of guess.
 */
import { API } from '../../helpers/api';

export type AuthProvider = {
  id: string;
  provider?: string;
};

export type AuthStatus = {
  break_glass: boolean;
  email_code_auth: boolean;
  enabled: boolean;
  password_login: boolean;
  provider?: string;
  providers: AuthProvider[];
};

export type AuthStatusSnapshot = {
  /** The last answer we have: this page's, the last visit's, or none yet. */
  status: AuthStatus | null;
  /** Whether the door may paint. False only until the first answer arrives. */
  ready: boolean;
};

/* The last answer, remembered across visits.
 *
 * A deployment's sign-in configuration changes about never, and the visitor
 * most likely to arrive cold on /login is one who has signed in before — so
 * the previous answer is a far better opening bid than a guess, and it paints
 * the true door on the first frame. It is still only an opening bid: the real
 * request goes out on every load and replaces it. The cost of being wrong is
 * bounded by how rarely the configuration changes; the cost of guessing was
 * paid by every visitor on every visit. */
const SEED_KEY = 'lazu.authStatus.v1';

/* If the status endpoint itself is unreachable there is no answer to wait
 * for, and a door that never paints is worse than one that offers the
 * credential every deployment has. This is the old guess, kept for the one
 * case it was ever right about. */
const UNREACHABLE: AuthStatus = {
  break_glass: false,
  email_code_auth: false,
  enabled: false,
  password_login: true,
  providers: [],
};

const normalizeProviders = (data: Record<string, unknown>): AuthProvider[] => {
  if (Array.isArray(data.providers)) {
    return data.providers.flatMap((candidate) => {
      if (candidate === null || typeof candidate !== 'object') return [];
      const provider = candidate as Record<string, unknown>;
      if (typeof provider.id !== 'string' || provider.id.length === 0)
        return [];
      return [
        {
          id: provider.id,
          provider:
            typeof provider.provider === 'string'
              ? provider.provider
              : undefined,
        },
      ];
    });
  }

  // Compatibility with API versions that exposed one OIDC provider only.
  if (data.enabled) {
    return [
      {
        id: 'oidc',
        provider: typeof data.provider === 'string' ? data.provider : undefined,
      },
    ];
  }
  return [];
};

const parseStatus = (value: unknown): AuthStatus | null => {
  if (value === null || typeof value !== 'object') return null;
  const raw = value as Record<string, unknown>;
  const providers = normalizeProviders(raw);
  return {
    break_glass: Boolean(raw.break_glass),
    email_code_auth: Boolean(raw.email_code_auth),
    enabled: providers.length > 0,
    password_login: Boolean(raw.password_login),
    provider: typeof raw.provider === 'string' ? raw.provider : undefined,
    providers,
  };
};

/* Read through `globalThis` so the prerender build, where there is no storage
 * at all, takes the same path as a browser that refuses it. */
const seedStore = (): Storage | null =>
  typeof globalThis.localStorage === 'undefined'
    ? null
    : globalThis.localStorage;

const readSeed = (): AuthStatus | null => {
  try {
    const raw = seedStore()?.getItem(SEED_KEY);
    return raw ? parseStatus(JSON.parse(raw)) : null;
  } catch {
    // Unreadable storage, or junk left by an older build: no seed, no noise.
    return null;
  }
};

const writeSeed = (status: AuthStatus) => {
  try {
    seedStore()?.setItem(SEED_KEY, JSON.stringify(status));
  } catch {
    // Private mode or a full quota: the seed is an optimization, not a need.
  }
};

const sameStatus = (a: AuthStatus | null, b: AuthStatus) =>
  a !== null &&
  a.break_glass === b.break_glass &&
  a.email_code_auth === b.email_code_auth &&
  a.enabled === b.enabled &&
  a.password_login === b.password_login &&
  a.provider === b.provider &&
  a.providers.length === b.providers.length &&
  a.providers.every(
    (provider, index) =>
      provider.id === b.providers[index]?.id &&
      provider.provider === b.providers[index]?.provider,
  );

let snapshot: AuthStatusSnapshot = { ready: false, status: null };
let seeded = false;
let inFlight: Promise<AuthStatus> | null = null;
const listeners = new Set<() => void>();

const publish = (status: AuthStatus) => {
  if (sameStatus(snapshot.status, status) && snapshot.ready) return;
  snapshot = { ready: true, status };
  listeners.forEach((listener) => listener());
};

/* The seed is read on first use rather than at module load, so importing this
 * file — which the marketing bundle does, to be able to prefetch — touches no
 * storage until something actually wants the answer. */
const ensureSeed = () => {
  if (seeded) return;
  seeded = true;
  const seed = readSeed();
  if (seed) snapshot = { ready: true, status: seed };
};

/** Start the request if it has not been started, and answer when it lands. */
export const fetchAuthStatus = (): Promise<AuthStatus> => {
  ensureSeed();
  if (inFlight) return inFlight;
  inFlight = API.get('/api/auth/oidc/status')
    .then((res) => {
      const status = parseStatus(res.data?.data) ?? UNREACHABLE;
      publish(status);
      writeSeed(status);
      return status;
    })
    .catch(() => {
      // Keep whatever the last visit told us over the blanket fallback: it is
      // the more likely truth, and it is what is already on screen.
      const status = snapshot.status ?? UNREACHABLE;
      publish(status);
      return status;
    });
  return inFlight;
};

/** Warm the answer from somewhere that is not the door itself. */
export const prefetchAuthStatus = () => {
  void fetchAuthStatus();
};

export const subscribeAuthStatus = (listener: () => void) => {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
};

export const getAuthStatusSnapshot = (): AuthStatusSnapshot => {
  ensureSeed();
  return snapshot;
};

/** Test seam: forget the process-wide answer and the remembered one. */
export const resetAuthStatusForTests = () => {
  snapshot = { ready: false, status: null };
  seeded = false;
  inFlight = null;
  listeners.clear();
  try {
    seedStore()?.removeItem(SEED_KEY);
  } catch {
    // Nothing to forget.
  }
};
