import { useTranslation } from 'react-i18next';
import { Link, useLocation } from 'react-router-dom';
import { Button, StatusPage } from '../../components/ui';

const Forbidden = () => {
  const { t } = useTranslation();
  const location = useLocation();
  // The route the guard turned away from, not /forbidden itself.
  const from = (
    location.state as {
      from?: { hash?: string; pathname?: string; search?: string };
    } | null
  )?.from;
  const attempted = from?.pathname
    ? `${from.pathname}${from.search || ''}`
    : undefined;

  return (
    <StatusPage
      action={
        <Button render={<Link to='/console' />} variant='primary'>
          {t('page.forbidden.consoleAction')}
        </Button>
      }
      description={t('page.forbidden.description')}
      ground
      mark='403'
      pill={attempted ? `GET ${attempted}` : undefined}
      secondaryAction={
        <Button render={<Link to='/' />} variant='ghost'>
          {t('page.forbidden.homeAction')}
        </Button>
      }
      title={t('page.forbidden.title')}
    />
  );
};

export default Forbidden;
