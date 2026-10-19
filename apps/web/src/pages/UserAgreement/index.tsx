import React from 'react';
import { useTranslation } from 'react-i18next';
import DocumentRenderer from '../../components/common/DocumentRenderer';

const UserAgreement = () => {
  const { t } = useTranslation();

  return (
    <DocumentRenderer
      apiEndpoint='/api/user-agreement'
      title={t('page.public.userAgreement.title')}
      cacheKey='user_agreement'
      siblings={[
        {
          description: t('page.public.privacyPolicy.siblingDescription', {
            defaultValue: '我们处理哪些信息、如何使用、保留多久。',
          }),
          title: t('page.public.privacyPolicy.title'),
          to: '/privacy',
        },
      ]}
      emptyMessage={t('page.public.userAgreement.loadError')}
    />
  );
};

export default UserAgreement;
