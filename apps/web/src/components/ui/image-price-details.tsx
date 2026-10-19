import { useTranslation } from 'react-i18next';
import type { ImagePricing } from '../../lib/image-pricing';

export function ImagePriceDetails({ price }: { price: ImagePricing }) {
  const { t } = useTranslation();
  const money = (n: number) => `$${Number(n.toFixed(6))}`;
  const threshold = price.pixel_threshold?.toLocaleString();
  return (
    <dl className='text-xs leading-relaxed text-muted-foreground'>
      <div>
        <dt className='inline'>
          {t('pricing.images.successfulOutput')}
          {threshold ? ` ≤ ${threshold}px` : ''}:{' '}
        </dt>
        <dd className='inline'>{money(price.output_usd)}</dd>
      </div>
      {price.high_resolution_output_usd != null ? (
        <div>
          <dt className='inline'>{`> ${threshold}px: `}</dt>
          <dd className='inline'>{money(price.high_resolution_output_usd)}</dd>
        </div>
      ) : null}
      {price.layer_usd != null ? (
        <div>
          <dt className='inline'>{t('pricing.images.decomposition')}: </dt>
          <dd className='inline'>
            {money(price.layer_usd)}
            {price.high_resolution_layer_usd != null
              ? ` / ${money(price.high_resolution_layer_usd)} (> ${threshold}px)`
              : ''}
          </dd>
        </div>
      ) : null}
      <div>
        <dt className='inline'>{t('pricing.images.references')}: </dt>
        <dd className='inline'>
          {price.reference_usd
            ? `${t('pricing.images.freeImages')} ${price.free_references ?? 0}; +${money(price.reference_usd)} / ${t('pricing.images.additionalImage')}`
            : t('pricing.images.free')}
        </dd>
      </div>
    </dl>
  );
}
