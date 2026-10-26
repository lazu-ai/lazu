import { Trans, useTranslation } from 'react-i18next';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { Button, StatusPage } from '../../components/ui';
import { suggestRoute } from '../../lib/route-suggest';

/* React Router numbers the entries it pushed; idx 0 is where this tab came
 * in, so there is no "back" to offer to someone who opened the link fresh. */
const hasInAppHistory = () =>
  typeof window !== 'undefined' &&
  Number((window.history.state as { idx?: number } | null)?.idx) > 0;

const NotFound = () => {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const signedIn =
    typeof window !== 'undefined' && Boolean(localStorage.getItem('user'));
  const suggestion = suggestRoute(location.pathname);
  const suggestionName = suggestion
    ? t(suggestion.label, { defaultValue: suggestion.fallback })
    : '';

  const secondary = hasInAppHistory() ? (
    <Button onClick={() => navigate(-1)} variant='ghost'>
      {t('page.notFound.backAction')}
    </Button>
  ) : signedIn ? null : (
    <Button
      render={<a aria-label={t('page.notFound.modelsAction')} href='/models' />}
      variant='ghost'
    >
      {t('page.notFound.modelsAction')}
    </Button>
  );

  return (
    <StatusPage
      action={
        signedIn ? (
          <Button render={<Link to='/console' />} variant='primary'>
            {t('page.notFound.consoleAction')}
          </Button>
        ) : (
          <Button render={<Link to='/' />} variant='primary'>
            {t('page.notFound.homeAction')}
          </Button>
        )
      }
      description={t('page.notFound.description')}
      ground
      hint={
        suggestion ? (
          <Trans
            components={{
              page: <a aria-label={suggestionName} href={suggestion.path} />,
            }}
            i18nKey='page.notFound.suggestion'
            values={{ name: suggestionName }}
          />
        ) : undefined
      }
      mark='404'
      pill={`GET ${location.pathname}${location.search}`}
      secondaryAction={secondary}
      title={t('page.notFound.title')}
    />
  );
};

export default NotFound;
