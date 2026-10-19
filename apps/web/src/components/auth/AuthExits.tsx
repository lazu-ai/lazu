import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';

/* The way out of the door.
 *
 * Its own module on purpose: the loading outline draws the real element so the
 * column does not grow when the door arrives, and the outline is reached from
 * the eagerly-loaded AppLoading. Importing it from AuthLayout would pull the
 * whole authentication implementation — brand assets, theme, chrome controls,
 * the Yunmu ground — into the first download for every route. */
export function AuthExits() {
  const { t } = useTranslation();
  return (
    <nav className='auth-column__exits' aria-label='Lazu'>
      <Link to='/'>lazu.ai</Link>
      <Link to='/privacy'>{t('page.public.privacyPolicy.title')}</Link>
      <Link to='/terms'>{t('page.public.userAgreement.title')}</Link>
    </nav>
  );
}

export default AuthExits;
