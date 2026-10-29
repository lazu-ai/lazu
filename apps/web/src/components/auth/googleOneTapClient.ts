/* Google One Tap, without React.
 *
 * The deployment says whether to offer it (`google_one_tap_client_id` in the
 * auth status); this file does the rest: load Google's script, take a nonce
 * from the server, show the prompt, and post the ID token Google hands back.
 * It is plain DOM and fetch so a server-rendered page can use it as it is,
 * not only the React app.
 *
 * Everything that can fail fails quietly. Google's script is unreachable for
 * part of the audience and a visitor signed out of Google never sees a
 * prompt; either way the page simply has no prompt, and nothing on it waits
 * for one.
 */
import { buildAPIURL } from '../../helpers/apiUrl';

const GOOGLE_IDENTITY_SCRIPT = 'https://accounts.google.com/gsi/client';
const ONE_TAP_PATH = '/api/auth/google/one-tap';
// The server keeps a nonce for ten minutes. Google is configured with the one
// it was given, so past this age a prompt would hand back a token the server
// refuses; a minute of margin covers the round trips and a slow click.
const NONCE_LIFETIME_MS = 9 * 60 * 1000;

type GoogleCredentialResponse = { credential?: string };

type GoogleIdentity = {
  initialize: (config: {
    auto_select: boolean;
    callback: (response: GoogleCredentialResponse) => void;
    cancel_on_tap_outside: boolean;
    client_id: string;
    context: 'signin' | 'signup' | 'use';
    itp_support: boolean;
    nonce: string;
  }) => void;
  prompt: () => void;
  cancel: () => void;
};

type ApiEnvelope = {
  data?: unknown;
  error?: { code?: unknown };
  success?: boolean;
};

export type SignedInUser = Record<string, unknown>;

export type GoogleOneTapOptions = {
  clientId: string;
  /** Builds the API address; defaults to the console's API base. */
  apiURL?: (path: string) => string;
  /** Sent with the sign-in, e.g. the interface language for the welcome mail. */
  headers?: Record<string, string>;
  /** Runs right before the token is posted. */
  beforeSubmit?: () => void;
  onSignedIn: (user: SignedInUser) => void;
  /** An error catalog code; not called for a prompt that never showed. */
  onError?: (code: string) => void;
};

const googleIdentity = (): GoogleIdentity | undefined =>
  (window as unknown as { google?: { accounts?: { id?: GoogleIdentity } } })
    .google?.accounts?.id;

let scriptLoad: Promise<GoogleIdentity> | null = null;

const loadGoogleIdentity = (): Promise<GoogleIdentity> => {
  const ready = googleIdentity();
  if (ready) return Promise.resolve(ready);
  if (scriptLoad) return scriptLoad;
  scriptLoad = new Promise<GoogleIdentity>((resolve, reject) => {
    const script = document.createElement('script');
    script.src = GOOGLE_IDENTITY_SCRIPT;
    script.async = true;
    script.defer = true;
    script.onload = () => {
      const loaded = googleIdentity();
      if (loaded) resolve(loaded);
      else reject(new Error('google identity script loaded without its API'));
    };
    script.onerror = () => {
      script.remove();
      reject(new Error('google identity script unreachable'));
    };
    document.head.appendChild(script);
  });
  // A failed load may be retried by a later page; a hung one stays pending,
  // which is harmless because nothing waits on it.
  scriptLoad.catch(() => {
    scriptLoad = null;
  });
  return scriptLoad;
};

const readEnvelope = async (response: Response): Promise<ApiEnvelope> => {
  try {
    const body: unknown = await response.json();
    return body && typeof body === 'object' ? (body as ApiEnvelope) : {};
  } catch {
    return {};
  }
};

const errorCode = (body: ApiEnvelope): string =>
  typeof body.error?.code === 'string' ? body.error.code : 'oauth_login_failed';

/* Google asks for initialize() once per page. Later starts (another route of
 * the same app) only prompt again, and the callback reads the options of
 * whoever started last. */
let current: GoogleOneTapOptions | null = null;
let initialized: Promise<GoogleIdentity | null> | null = null;
// When the nonce behind `initialized` was asked for (it may be pending).
let initializedAt = 0;
let expiryTimer: ReturnType<typeof setTimeout> | null = null;

const forgetInitialization = () => {
  initialized = null;
  if (expiryTimer !== null) {
    clearTimeout(expiryTimer);
    expiryTimer = null;
  }
};

const submit = async (credential: string | undefined) => {
  const options = current;
  if (!options || !credential) return;
  const url = options.apiURL ?? buildAPIURL;
  // The server clears the nonce on this post, whatever the outcome; a later
  // start asks for a fresh one.
  forgetInitialization();
  options.beforeSubmit?.();
  try {
    const response = await fetch(url(ONE_TAP_PATH), {
      body: JSON.stringify({ credential }),
      credentials: 'include',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        ...options.headers,
      },
      method: 'POST',
    });
    const body = await readEnvelope(response);
    if (response.ok && body.success !== false && body.data) {
      options.onSignedIn(body.data as SignedInUser);
      return;
    }
    options.onError?.(errorCode(body));
  } catch {
    options.onError?.('oauth_login_failed');
  }
};

const initialize = async (
  options: GoogleOneTapOptions,
): Promise<GoogleIdentity | null> => {
  // Script first: a visitor who cannot reach Google costs no nonce and gets
  // no cookie.
  const identity = await loadGoogleIdentity();
  const url = options.apiURL ?? buildAPIURL;
  const response = await fetch(url(ONE_TAP_PATH), {
    credentials: 'include',
    headers: { Accept: 'application/json' },
  });
  const body = await readEnvelope(response);
  const data = body.data as { nonce?: unknown } | undefined;
  if (!response.ok || typeof data?.nonce !== 'string' || !data.nonce) {
    return null;
  }
  identity.initialize({
    auto_select: false,
    callback: (answer) => void submit(answer.credential),
    cancel_on_tap_outside: false,
    client_id: options.clientId,
    context: 'signin',
    itp_support: true,
    nonce: data.nonce,
  });
  return identity;
};

const pageVisible = () =>
  typeof document === 'undefined' || document.visibilityState !== 'hidden';

/** Offer One Tap on this page. Safe to call more than once. */
export const startGoogleOneTap = (options: GoogleOneTapOptions): void => {
  if (typeof window === 'undefined' || !options.clientId) return;
  current = options;
  // A later page of the same app reuses Google's configuration only while
  // its nonce is still one the server will accept.
  if (initialized && Date.now() - initializedAt >= NONCE_LIFETIME_MS) {
    forgetInitialization();
  }
  if (!initialized) {
    const attempt = initialize(options).catch(() => null);
    initialized = attempt;
    initializedAt = Date.now();
    void attempt.then((identity) => {
      if (initialized !== attempt) return;
      // A start that found nothing to show (no nonce, no script) may be retried.
      if (!identity) {
        forgetInitialization();
        return;
      }
      // A prompt still on screen when its nonce expires is renewed with a
      // fresh one, so choosing an account never meets a refused token. A
      // hidden page is left alone and renews on its next start.
      expiryTimer = setTimeout(() => {
        expiryTimer = null;
        if (initialized !== attempt) return;
        forgetInitialization();
        if (current && pageVisible()) startGoogleOneTap(current);
      }, NONCE_LIFETIME_MS);
    });
  }
  void initialized.then((identity) => {
    // Only the start that is still current gets a prompt.
    if (identity && current === options) identity.prompt();
  });
};

/** Withdraw the prompt, e.g. when the page that offered it goes away. */
export const stopGoogleOneTap = (options?: GoogleOneTapOptions): void => {
  if (options && current !== options) return;
  current = null;
  googleIdentity()?.cancel();
};
