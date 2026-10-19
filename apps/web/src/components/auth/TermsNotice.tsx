/* Consent as a notice, not a gate.
 *
 * The checkbox this replaces had to sit above the provider buttons so it could
 * disable them, which is how it ended up directly under the page heading. What
 * actually makes an agreement hold up is a conspicuous link immediately beside
 * the control the person presses, on an uncluttered background — plus the
 * version and timestamp the server writes onto the account. Both are here.
 */
import { useContext } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { StatusContext } from '../../context/Status';

const TermsNotice = () => {
  const { t } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const hasUserAgreement = Boolean(statusState.status?.user_agreement_enabled);
  const hasPrivacyPolicy = Boolean(statusState.status?.privacy_policy_enabled);

  if (!hasUserAgreement && !hasPrivacyPolicy) return null;

  return (
    <p className='auth-terms-notice'>
      {t('page.public.auth.termsNoticePrefix')}
      {hasUserAgreement ? (
        <>
          {' '}
          <Link
            to='/terms'
            target='_blank'
            rel='noopener noreferrer'
            className='auth-link'
          >
            {t('page.public.userAgreement.title')}
          </Link>
        </>
      ) : null}
      {hasUserAgreement && hasPrivacyPolicy
        ? ` ${t('page.public.auth.and')}`
        : null}
      {hasPrivacyPolicy ? (
        <>
          {' '}
          <Link
            to='/privacy'
            target='_blank'
            rel='noopener noreferrer'
            className='auth-link'
          >
            {t('page.public.privacyPolicy.title')}
          </Link>
        </>
      ) : null}
    </p>
  );
};

export default TermsNotice;
