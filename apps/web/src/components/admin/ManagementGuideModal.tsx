import { useTranslation } from 'react-i18next';
import { Modal, DataCard } from '../ui';

type ManagementKind = 'channel' | 'user' | 'model';

const KIND = {
  channel: { titleKey: 'admin.guide.channel.title', apiBase: '/api/channel' },
  user: { titleKey: 'admin.guide.user.title', apiBase: '/api/user' },
  model: { titleKey: 'admin.guide.model.title', apiBase: '/api/models' },
} satisfies Record<ManagementKind, { titleKey: string; apiBase: string }>;

type ManagementGuideModalProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  kind: ManagementKind;
  mode?: 'create' | 'edit';
  recordLabel?: string;
};

export function ManagementGuideModal({
  open,
  onOpenChange,
  kind,
  mode = 'create',
  recordLabel,
}: ManagementGuideModalProps) {
  const { t } = useTranslation();
  const meta = KIND[kind] || KIND.channel;
  const titleBase = t(meta.titleKey);
  const headline =
    mode === 'edit'
      ? `${titleBase} · ${recordLabel || ''}`
      : `${titleBase} · ${t('admin.guide.modeCreate')}`;

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      className='lazu-modal-content--wide'
      title={headline}
      description={t(`admin.guide.${kind}.description`)}
      body={
        <div className='grid gap-4'>
          <DataCard
            title={t('admin.guide.actions.title')}
            description={t('admin.guide.actions.description')}
          >
            <ul className='lazu-guide-list'>
              <li>{t('admin.guide.actions.row')}</li>
              <li>{t('admin.guide.actions.bulk')}</li>
              <li>{t('admin.guide.actions.refresh')}</li>
            </ul>
          </DataCard>

          <DataCard
            title={t('admin.guide.api.title')}
            description={t('admin.guide.api.description')}
          >
            <ol className='lazu-guide-list lazu-guide-list--ordered'>
              <li>
                {t('admin.guide.api.create')}{' '}
                <code className='lazu-inline-code'>POST {meta.apiBase}/</code>
              </li>
              <li>
                {t('admin.guide.api.update')}{' '}
                <code className='lazu-inline-code'>PUT {meta.apiBase}/</code>
              </li>
              <li>{t('admin.guide.api.docs')}</li>
            </ol>
          </DataCard>

          <DataCard
            title={t('admin.guide.roadmap.title')}
            description={t('admin.guide.roadmap.description')}
          />
        </div>
      }
    />
  );
}

export default ManagementGuideModal;
