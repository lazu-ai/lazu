import React from 'react';
import { useTranslation } from 'react-i18next';
import DocumentRenderer from '../../components/common/DocumentRenderer';

const PrivacyPolicy = () => {
  const { t } = useTranslation();

  return (
    <DocumentRenderer
      apiEndpoint='/api/privacy-policy'
      title={t('page.public.privacyPolicy.title')}
      cacheKey='privacy_policy'
      siblings={[
        {
          description: t('page.public.userAgreement.siblingDescription', {
            defaultValue: '使用 Lazu 的资格、计费、API Key 与责任边界。',
          }),
          title: t('page.public.userAgreement.title'),
          to: '/terms',
        },
      ]}
      emptyMessage={t('page.public.privacyPolicy.loadError')}
    />
  );
};

export default PrivacyPolicy;
