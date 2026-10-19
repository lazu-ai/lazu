import { Plus, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Input, PendingBlock, Select } from '../../../components/ui';
import { showSuccess } from '../../../helpers';
import { getExternalAPIBaseURL } from '../../../helpers/apiUrl';
import {
  type AccessAddressKind,
  useAccessAddressesAdmin,
  useSaveAccessAddresses,
} from '../../../hooks/access-addresses/useAccessAddressesAdmin';
import './access-addresses.css';

type Draft = { kind: AccessAddressKind; url: string; userCount: number };

/* The extra API addresses this deployment answers on. A new address is open
   to nobody: accounts are opened one by one from 客户账户. The default
   address is the deployment's own and cannot be edited here. */
export default function AccessAddresses() {
  const { t } = useTranslation();
  const query = useAccessAddressesAdmin();
  const save = useSaveAccessAddresses();
  const [drafts, setDrafts] = useState<Draft[]>([]);

  useEffect(() => {
    if (!query.data) return;
    setDrafts(
      query.data.map((item) => ({
        kind: item.kind,
        url: item.url,
        userCount: item.user_count,
      })),
    );
  }, [query.data]);

  const kindOptions = [
    {
      label: t('page.console.token.address.kind.global', {
        defaultValue: '全球加速',
      }),
      value: 'global',
    },
    {
      label: t('page.console.token.address.kind.cnDirect', {
        defaultValue: '国内直连',
      }),
      value: 'cn_direct',
    },
  ];
  const defaultURL = getExternalAPIBaseURL(
    window.location.origin,
    window.location.hostname,
  );
  /* The list saves on its own button, so it says when it holds edits the
     server has not seen. */
  const savedShape = JSON.stringify(
    (query.data || []).map((item) => [item.kind, item.url]),
  );
  const draftShape = JSON.stringify(
    drafts
      .filter((draft) => draft.url.trim())
      .map((draft) => [draft.kind, draft.url.trim()]),
  );
  const dirty = Boolean(query.data) && savedShape !== draftShape;
  const update = (index: number, patch: Partial<Draft>) =>
    setDrafts((current) =>
      current.map((item, i) => (i === index ? { ...item, ...patch } : item)),
    );

  if (query.isPending) return <PendingBlock height={152} />;

  return (
    <div className='set-addr'>
      <div className='set-addr__row'>
        <Input
          aria-label={t('page.console.settingV2.access.addresses.url', {
            defaultValue: '地址',
          })}
          className='set-addr__url'
          disabled
          value={defaultURL}
        />
        <Select
          aria-label={t('page.console.settingV2.access.addresses.kind', {
            defaultValue: '类型',
          })}
          disabled
          options={kindOptions}
          value='global'
        />
        <span className='set-addr__tail'>
          {t('page.console.settingV2.access.addresses.defaultTail', {
            defaultValue: '默认 · 所有人',
          })}
        </span>
        <span />
      </div>
      {drafts.map((draft, index) => (
        <div className='set-addr__row' key={index}>
          <Input
            aria-label={t('page.console.settingV2.access.addresses.url', {
              defaultValue: '地址',
            })}
            className='set-addr__url'
            placeholder='https://'
            value={draft.url}
            onChange={(event) => update(index, { url: event.target.value })}
          />
          <Select
            aria-label={t('page.console.settingV2.access.addresses.kind', {
              defaultValue: '类型',
            })}
            options={kindOptions}
            value={draft.kind}
            onValueChange={(value) =>
              update(index, { kind: value as AccessAddressKind })
            }
          />
          <span className='set-addr__tail'>
            {t('page.console.settingV2.access.addresses.accounts', {
              count: draft.userCount,
              defaultValue: '{{count}} 个账户',
            })}
          </span>
          <Button
            size='xs'
            variant='ghost'
            aria-label={t('page.console.settingV2.access.addresses.remove', {
              defaultValue: '移除这个地址',
            })}
            onClick={() =>
              setDrafts((current) => current.filter((_, i) => i !== index))
            }
          >
            <X aria-hidden='true' size={14} />
          </Button>
        </div>
      ))}
      <div className='set-addr__actions'>
        <Button
          size='sm'
          variant='secondary'
          onClick={() =>
            setDrafts((current) => [
              ...current,
              { kind: 'cn_direct', url: '', userCount: 0 },
            ])
          }
        >
          <Plus aria-hidden='true' size={14} />
          {t('page.console.settingV2.access.addresses.add', {
            defaultValue: '添加地址',
          })}
        </Button>
        {dirty ? (
          <span
            className='set-card__dirty set-card__dirty--inline'
            role='status'
          >
            {t('page.console.settingV2.access.addresses.unsaved', {
              defaultValue: '有改动未保存',
            })}
          </span>
        ) : null}
        <Button
          size='sm'
          variant='primary'
          disabled={!dirty}
          loading={save.isPending}
          onClick={() =>
            save.mutate(
              drafts
                .filter((draft) => draft.url.trim())
                .map((draft) => ({ kind: draft.kind, url: draft.url.trim() })),
              {
                onSuccess: () =>
                  showSuccess(
                    t('page.console.settingV2.access.addresses.saved', {
                      defaultValue: '接入地址已保存',
                    }),
                  ),
              },
            )
          }
        >
          {t('common.save', { defaultValue: '保存' })}
        </Button>
      </div>
    </div>
  );
}
