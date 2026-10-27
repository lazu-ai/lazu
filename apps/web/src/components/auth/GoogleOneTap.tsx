/* Offers Google One Tap to a signed-out visitor when the deployment has it on.
 *
 * Renders nothing: the prompt is Google's own, drawn over the page, and its
 * account chooser carries the consent notice (the privacy and terms links on
 * the deployment's Google consent screen). A sign-in finishes exactly as the
 * sign-in door finishes one: the session, the stored copy, the API client's
 * identity, then `next`.
 */
import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { startGoogleOneTap, stopGoogleOneTap } from './googleOneTapClient';
import { readStoredSessionHint } from './loginSession';
import useAuthStatus from './useAuthStatus';
import { useSession } from '../../context/User';
import { updateAPI } from '../../helpers/api';
import { postLoginDestination } from '../../helpers/authNavigation';
import { setUserData } from '../../helpers/data';
import { showError } from '../../helpers/utils';
import { notify } from '../ui/toast';
import type { GoogleOneTapOptions } from './googleOneTapClient';

type GoogleOneTapProps = {
  /** Where a sign-in lands; checked like every other post-login path. */
  next: string;
};

export default function GoogleOneTap({ next }: GoogleOneTapProps) {
  const { t, i18n } = useTranslation();
  const { status } = useAuthStatus();
  const { user, signIn } = useSession();
  const navigate = useNavigate();
  const clientId = status?.google_one_tap_client_id;
  const language = i18n.resolvedLanguage || i18n.language;
  const signedIn = Boolean(user);

  useEffect(() => {
    if (!clientId || signedIn || readStoredSessionHint()) return undefined;
    const options: GoogleOneTapOptions = {
      clientId,
      // The welcome mail for a new account is written in this language.
      headers: language ? { 'Accept-Language': language } : undefined,
      onError: (code) => showError({ error: { code } }),
      onSignedIn: (data) => {
        signIn(data);
        setUserData(data);
        updateAPI();
        notify.success(t('page.public.auth.loginSuccess'));
        navigate(postLoginDestination(next, data.role), { replace: true });
      },
    };
    startGoogleOneTap(options);
    return () => stopGoogleOneTap(options);
  }, [clientId, language, navigate, next, signIn, signedIn, t]);

  return null;
}
