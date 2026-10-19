import { useTranslation } from 'react-i18next';
import { type Account } from './account-model';
import { PendingBlock, Switch } from '../../components/ui';
import { getExternalAPIBaseURL } from '../../helpers/apiUrl';
import {
  type AccessAddressKind,
  useAccessAddressesAdmin,
  useSetAccessAddressGrant,
} from '../../hooks/access-addresses/useAccessAddressesAdmin';

/* Which API addresses this account's Keys may call. The default address is
   open to everyone; every other one is switched on per account, and the
   gateway refuses it to the rest. */
export default function AccountAccessAddresses({
  account,
}: {
  account: Account;
}) {
  const { t } = useTranslation();
  const query = useAccessAddressesAdmin();
  const grant = useSetAccessAddressGrant();
  const internalId = Number(account.internal_id ?? account.id);
  const kindLabel = (kind: AccessAddressKind) =>
    kind === 'cn_direct'
      ? t('page.console.token.address.kind.cnDirect', {
          defaultValue: '国内直连',
        })
      : t('page.console.token.address.kind.global', {
          defaultValue: '全球加速',
        });
  const defaultURL = getExternalAPIBaseURL(
    window.location.origin,
    window.location.hostname,
  );

  if (query.isPending) return <PendingBlock height={92} />;

  const rows = [
    { kind: 'global' as const, url: defaultURL, on: true, fixed: true },
    ...(query.data || []).map((item) => ({
      kind: item.kind,
      url: item.url,
      on: item.user_ids.includes(internalId),
      fixed: false,
    })),
  ];

  return (
    <div className='acc-addr'>
      {rows.map((row) => (
        <div className='acc-addr__row' key={row.url}>
          <span className='acc-addr__kind'>{kindLabel(row.kind)}</span>
          <code className='acc-addr__url'>{row.url}</code>
          <Switch
            aria-label={`${kindLabel(row.kind)} ${row.url}`}
            checked={row.on}
            disabled={row.fixed || grant.isPending}
            onCheckedChange={(checked) =>
              grant.mutate({
                enabled: checked,
                url: row.url,
                userId: account.id,
              })
            }
          />
        </div>
      ))}
      <p className='acc-addr__note'>
        {(query.data || []).length
          ? t('page.console.orgs.drawer.access.note', {
              defaultValue: '没开的地址，这个账户的密钥调用会被拒绝。',
            })
          : t('page.console.orgs.drawer.access.empty', {
              defaultValue:
                '还没有登记其他地址，在平台设置 › 账号与接入里添加。',
            })}
      </p>
    </div>
  );
}
