import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { AuthPanel, AuthShell } from './AuthLayout';
import { useSession } from '../../context/User';
import { API, updateAPI } from '../../helpers/api';
import {
  safeInternalAuthPath,
  postLoginDestination,
} from '../../helpers/authNavigation';
import { setUserData } from '../../helpers/data';
import { showError } from '../../helpers/utils';
import { LoadingState } from '../ui';

export default function OIDCCallback() {
  const { t } = useTranslation();
  const userSession = useSession();
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();

  useEffect(() => {
    let cancelled = false;
    const next = safeInternalAuthPath(searchParams.get('next'));

    const bootstrap = async () => {
      try {
        const res = await API.get('/api/auth/oidc/bootstrap', {
          skipErrorHandler: true,
          withCredentials: true,
        } as unknown as Parameters<typeof API.get>[1]);
        if (cancelled) return;
        if (res.data?.success && res.data?.data) {
          const user = res.data.data;
          userSession.signIn(user);
          setUserData(user);
          updateAPI();
          navigate(postLoginDestination(next, user.role), { replace: true });
          return;
        }
        navigate('/login?expired=true', { replace: true });
      } catch (error) {
        if (!cancelled) {
          showError(error);
          navigate('/login?expired=true', { replace: true });
        }
      }
    };

    bootstrap();

    return () => {
      cancelled = true;
    };
  }, [navigate, searchParams, userSession.signIn]);

  return (
    <AuthShell>
      <AuthPanel
        title={t('page.public.auth.callback.title')}
        description={t('page.public.auth.callback.description')}
      >
        <LoadingState
          title={t('page.public.auth.callback.loadingTitle')}
          description={t('page.public.auth.callback.loadingDescription')}
        />
      </AuthPanel>
    </AuthShell>
  );
}
