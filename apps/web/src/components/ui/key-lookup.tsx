import { useTranslation } from 'react-i18next';
import './key-lookup.css';

export function KeyLookupHeading() {
  const { t } = useTranslation();
  return (
    <div className='key-lookup__head'>
      <span className='key-lookup__eyebrow'>
        {t('page.keyInspector.eyebrow', {
          defaultValue: 'Key 查询 · 无需登录',
        })}
      </span>
      <h1 className='key-lookup__title'>
        {t('page.keyInspector.heroTitle', {
          defaultValue: '查询你的 API Key 用量',
        })}
      </h1>
      <p className='key-lookup__lede'>
        {t('page.keyInspector.heroSubtitle', {
          defaultValue:
            '粘贴 key，即可查看剩余额度、可调用模型与调用明细，无需登录账号。',
        })}
      </p>
    </div>
  );
}
