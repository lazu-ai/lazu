/* Provider sign-in button.
 *
 * The backend reports every configured identity provider. Recognised providers
 * carry their own mark and name; a generic enterprise OIDC provider falls back
 * to neutral SSO. The parent renders only entries returned by the backend.
 */
import { KeyRound } from 'lucide-react';
import { useTranslation } from 'react-i18next';

export type SsoProvider = '' | 'github' | 'gitlab' | 'google' | 'microsoft';

type SsoButtonProps = {
  disabled?: boolean;
  onClick: () => void;
  provider?: SsoProvider | string;
};

const GithubMark = () => (
  <svg className='auth-sso-mark' viewBox='0 0 16 16' aria-hidden='true'>
    <path
      fill='currentColor'
      d='M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82a7.4 7.4 0 0 1 2-.27c.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z'
    />
  </svg>
);

const GoogleMark = () => (
  <svg className='auth-sso-mark' viewBox='0 0 18 18' aria-hidden='true'>
    <path
      fill='#4285F4'
      d='M17.64 9.2c0-.64-.06-1.25-.16-1.84H9v3.48h4.84a4.14 4.14 0 0 1-1.8 2.72v2.26h2.92c1.7-1.57 2.68-3.88 2.68-6.62Z'
    />
    <path
      fill='#34A853'
      d='M9 18c2.43 0 4.47-.81 5.96-2.18l-2.92-2.26c-.81.54-1.84.86-3.04.86-2.34 0-4.32-1.58-5.03-3.7H.96v2.33A9 9 0 0 0 9 18Z'
    />
    <path
      fill='#FBBC05'
      d='M3.97 10.71a5.4 5.4 0 0 1 0-3.42V4.96H.96a9 9 0 0 0 0 8.08l3.01-2.33Z'
    />
    <path
      fill='#EA4335'
      d='M9 3.58c1.32 0 2.5.46 3.44 1.35l2.58-2.58C13.46.9 11.43 0 9 0A9 9 0 0 0 .96 4.96l3.01 2.33C4.68 5.17 6.66 3.58 9 3.58Z'
    />
  </svg>
);

const GitlabMark = () => (
  <svg className='auth-sso-mark' viewBox='0 0 16 16' aria-hidden='true'>
    <path fill='#E24329' d='m8 15.2-2.95-9.07h5.9L8 15.2Z' />
    <path fill='#FC6D26' d='m8 15.2-2.95-9.07H.92L8 15.2Z' />
    <path
      fill='#FCA326'
      d='M.92 6.13 .03 8.9a.61.61 0 0 0 .22.68L8 15.2.92 6.13Z'
    />
    <path
      fill='#E24329'
      d='M.92 6.13h4.13L3.27.66a.31.31 0 0 0-.59 0L.92 6.13Z'
    />
    <path fill='#FC6D26' d='m8 15.2 2.95-9.07h4.13L8 15.2Z' />
    <path
      fill='#FCA326'
      d='m15.08 6.13.89 2.77a.61.61 0 0 1-.22.68L8 15.2l7.08-9.07Z'
    />
    <path
      fill='#E24329'
      d='M15.08 6.13h-4.13L12.73.66a.31.31 0 0 1 .59 0l1.76 5.47Z'
    />
  </svg>
);

const MicrosoftMark = () => (
  <svg className='auth-sso-mark' viewBox='0 0 16 16' aria-hidden='true'>
    <path fill='#F25022' d='M1 1h6.6v6.6H1z' />
    <path fill='#7FBA00' d='M8.4 1H15v6.6H8.4z' />
    <path fill='#00A4EF' d='M1 8.4h6.6V15H1z' />
    <path fill='#FFB900' d='M8.4 8.4H15V15H8.4z' />
  </svg>
);

const MARKS: Record<string, () => JSX.Element> = {
  github: GithubMark,
  gitlab: GitlabMark,
  google: GoogleMark,
  microsoft: MicrosoftMark,
};

const NAMES: Record<string, string> = {
  github: 'GitHub',
  gitlab: 'GitLab',
  google: 'Google',
  microsoft: 'Microsoft',
};

export default function SsoButton({
  disabled,
  onClick,
  provider,
}: SsoButtonProps) {
  const { t } = useTranslation();
  const key = provider && MARKS[provider] ? provider : '';
  const Mark = key ? MARKS[key] : null;

  return (
    <button
      className='auth-sso-button'
      // Paints its own surface, so it opts out of the global native-button
      // reset in index.css the way that reset asks bespoke controls to.
      data-owns-chrome
      disabled={disabled}
      onClick={onClick}
      type='button'
    >
      {Mark ? <Mark /> : <KeyRound aria-hidden='true' size={16} />}
      {key
        ? t('page.public.auth.ssoWithProvider', {
            provider: NAMES[key],
          })
        : t('page.public.auth.oidcLogin')}
    </button>
  );
}
