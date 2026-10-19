import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import Loading from '../../components/layout/AppLoading';
import { getDocsQuickstartURL } from '../../helpers/brandUrls';

/** The in-app quickstart retired in favour of the docs site — one guide,
 *  one place to keep it current. This route only forwards old links. */
const Quickstart = () => {
  const { i18n } = useTranslation();

  useEffect(() => {
    window.location.replace(getDocsQuickstartURL(i18n.language));
  }, [i18n.language]);

  return <Loading />;
};

export default Quickstart;
