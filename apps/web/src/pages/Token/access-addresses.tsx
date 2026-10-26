import { Copy } from 'lucide-react';
import { useContext, useMemo } from 'react';
import { Button } from '../../components/ui';
import { notify } from '../../components/ui/toast';
import { UserContext } from '../../context/User';
import { copy } from '../../helpers';
import { getExternalAPIBaseURL } from '../../helpers/apiUrl';
import type { TFunction } from 'i18next';

export type AccessAddressKind = 'global' | 'cn_direct';

type AccessAddress = { kind: AccessAddressKind; url: string };

const isAccessAddress = (value: unknown): value is AccessAddress => {
  if (typeof value !== 'object' || value === null) return false;
  const record = value as Record<string, unknown>;
  return (
    typeof record.url === 'string' &&
    (record.kind === 'global' || record.kind === 'cn_direct')
  );
};

/** The extra API addresses opened to this account. They come with the
 *  session's own /api/user/self answer, so the strip never waits apart from
 *  the page. */
const readAccessAddresses = (user: unknown): AccessAddress[] => {
  if (!user || typeof user !== 'object') return [];
  const list = (user as { access_addresses?: unknown }).access_addresses;
  return Array.isArray(list) ? list.filter(isAccessAddress) : [];
};

export const accessAddressKindLabel = (
  kind: AccessAddressKind,
  t: TFunction,
) =>
  kind === 'cn_direct'
    ? t('page.console.token.address.kind.cnDirect', {
        defaultValue: '国内直连',
      })
    : t('page.console.token.address.kind.global', {
        defaultValue: '全球加速',
      });

const kindNote = (kind: AccessAddressKind, t: TFunction) =>
  kind === 'cn_direct'
    ? t('page.console.token.address.note.cnDirect', {
        defaultValue: '在中国大陆的网络里更稳，延迟更低。',
      })
    : t('page.console.token.address.note.global', {
        defaultValue: '海外访问快。不确定选哪个，就用它。',
      });

/**
 * Where to send requests, shown only to an account an administrator has
 * opened an extra address to. Everyone else keeps the page as it was: the
 * default address is already in every snippet the connect dialog writes.
 */
export function AccessAddressStrip({ t }: { t: TFunction }) {
  const [userState] = useContext(UserContext);
  const extras = useMemo(
    () => readAccessAddresses(userState.user),
    [userState.user],
  );
  const addresses = useMemo(() => {
    const fallback = getExternalAPIBaseURL(
      window.location.origin,
      window.location.hostname,
    );
    return [
      { kind: 'global' as const, url: fallback, isDefault: true },
      ...extras
        .filter((item) => item.url !== fallback)
        .map((item) => ({ ...item, isDefault: false })),
    ];
  }, [extras]);

  if (!extras.length) return null;

  return (
    <section
      aria-label={t('page.console.token.address.label', {
        defaultValue: '接入地址',
      })}
      className='tok-address'
      style={{ ['--tok-address-count' as string]: addresses.length }}
    >
      <div className='tok-address__label'>
        {t('page.console.token.address.label', { defaultValue: '接入地址' })}
      </div>
      {addresses.map((address) => (
        <div className='tok-address__cell' key={address.url}>
          <div className='tok-address__head'>
            <span className='tok-address__kind'>
              {accessAddressKindLabel(address.kind, t)}
            </span>
            {address.isDefault ? (
              <span className='tok-address__default'>
                {t('page.console.token.address.default', {
                  defaultValue: '默认',
                })}
              </span>
            ) : null}
          </div>
          <div className='tok-address__url-row'>
            <code className='tok-address__url'>{address.url}</code>
            <Button
              size='xs'
              variant='secondary'
              className='tok-address__copy'
              aria-label={t('page.console.token.address.copy', {
                defaultValue: '复制 {{url}}',
                url: address.url,
              })}
              onClick={async () => {
                if (await copy(address.url)) {
                  notify.success(
                    t('page.console.token.address.copied', {
                      defaultValue: '已复制 {{url}}',
                      url: address.url,
                    }),
                  );
                }
              }}
            >
              <Copy aria-hidden='true' size={14} />
            </Button>
          </div>
          <p className='tok-address__note'>{kindNote(address.kind, t)}</p>
        </div>
      ))}
    </section>
  );
}
