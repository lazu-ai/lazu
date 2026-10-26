/* Key dialogs: create a Key in a project (S10.1–2), rotate (S10.5), delete
 * (S10.6). There is only one kind of Key: it belongs to the project and to
 * one member. A freshly created or rotated secret shows inline for 30 s with
 * a copy button; it can always be shown again later by whoever is allowed
 * to. */
import { useQuery } from '@tanstack/react-query';
import { Check } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '../../../components/ui/button';
import { PendingValue } from '../../../components/ui/pending';
import {
  normalizeTokenPayload,
  normalizeTokenDetail,
  type SaveTokenVariables,
} from '../../../hooks/tokens/useTokensQuery';
import {
  TokenFormDrawer,
  type TokenFormServices,
} from '../../../pages/Token/token-support';
import '../../../pages/Token/token-page.css';
import {
  MAX_PERSONAL_KEYS,
  REVEAL_REMASK_SECONDS,
  validateBudget,
} from '../budget';
import { fetchProjectTokenVendorOptions } from '../client';
import { formatUSDShort } from '../money';
import { useProject } from '../ProjectContext';
import {
  useInvalidateProject,
  useProjectModels,
  useMyBudget,
} from '../queries';
import { errorReceipt, receipt } from '../receipt';
import { useOperationId } from '../useOperationId';
import {
  ActionModal,
  ErrLine,
  FormRow,
  FormRows,
  GateModal,
  SegControl,
} from './atoms';
import type { ProjectToken } from '../types';

/* --------------------------------------------------------- created secret */

export function CreatedSecret({
  name,
  secret,
  note,
  expiresAt,
}: {
  name: string;
  secret: string;
  note: string;
  expiresAt?: number;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const deadline = useMemo(
    () => expiresAt ?? (secret ? Date.now() + REVEAL_REMASK_SECONDS * 1000 : 0),
    [expiresAt, secret],
  );
  const [now, setNow] = useState(Date.now);
  const left = Math.max(0, Math.ceil((deadline - now) / 1000));
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    setNow(Date.now());
    setCopied(false);
  }, [deadline]);
  useEffect(() => {
    const refresh = () => {
      const currentTime = Date.now();
      setNow(currentTime);
      if (currentTime >= deadline) window.clearInterval(timer);
    };
    const timer = window.setInterval(refresh, 1000);
    const expiry = window.setTimeout(
      refresh,
      Math.max(0, deadline - Date.now()),
    );
    window.addEventListener('focus', refresh);
    document.addEventListener('visibilitychange', refresh);
    return () => {
      window.clearInterval(timer);
      window.clearTimeout(expiry);
      window.removeEventListener('focus', refresh);
      document.removeEventListener('visibilitychange', refresh);
    };
  }, [deadline]);
  const copy = async () => {
    if (Date.now() >= deadline) {
      setNow(Date.now());
      return;
    }
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(true);
      receipt(t('projects.key.copied', { name }));
    } catch {
      errorReceipt(
        new Error(
          t('secret.copyFallback', { defaultValue: '请在此字段全选并复制' }),
        ),
      );
    }
  };
  if (left <= 0) {
    return (
      <span className='pj-okt'>
        <Check aria-hidden='true' size={12} />{' '}
        {t('projects.key.createdMasked', { name })}
      </span>
    );
  }
  return (
    <div className='pj-created'>
      <div className='pj-created__row'>
        <span className='pj-nm'>{name}</span>
        <span className='pj-k__plain'>{secret}</span>
        <Button onClick={() => void copy()} size='sm' variant='primary'>
          {copied
            ? t('secret.copied', { defaultValue: '已复制' })
            : t('secret.copy', { defaultValue: '复制' })}
        </Button>
        <span className='pj-cd'>{left}s</span>
      </div>
      <span className='pj-okt'>{note}</span>
    </div>
  );
}

/* --------------------------------------------------------- create (S10.1) */

export function CreateKeyModal({
  open,
  onOpenChange,
  ownKeyCount,
  onCreated,
  editingToken,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  ownKeyCount: number;
  editingToken?: ProjectToken;
  onCreated: (created: { token: ProjectToken; key: string }) => void;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const invalidate = useInvalidateProject();
  const budget = useMyBudget({ enabled: open && !editingToken });
  const detail = useQuery({
    queryKey: [
      'project-token-edit',
      project.current?.id,
      editingToken?.id,
      editingToken?.version,
    ],
    queryFn: () =>
      project.client!.getToken(editingToken!.id, project.current!.id),
    enabled: open && !!editingToken && !!project.client && !!project.current,
    retry: false,
  });
  const detailValues = useMemo(
    () =>
      detail.data
        ? normalizeTokenDetail({
            ...detail.data,
            unlimited_quota: detail.data.cap_microusd === null,
            remain_quota: Number(detail.data.cap_microusd ?? 0),
          })
        : undefined,
    [detail.data],
  );
  const models = useProjectModels({ enabled: open });
  const catalog = useQuery({
    queryKey: ['project-token-vendors', project.current?.id],
    queryFn: fetchProjectTokenVendorOptions,
    enabled: open,
  });
  const [busy, setBusy] = useState(false);
  const atLimit = !editingToken && ownKeyCount >= MAX_PERSONAL_KEYS;
  const ready =
    (editingToken ? !!detail.data : !!budget.data) &&
    !!models.data &&
    !!catalog.data;
  const unavailable =
    (!editingToken && budget.isError) ||
    detail.isError ||
    models.isError ||
    catalog.isError;
  const allowed = new Set(models.data);
  const facts =
    catalog.data?.models.filter((fact) => allowed.has(fact.model)) ?? [];
  const vendorIds = new Set(facts.map((fact) => fact.vendorID));
  const save = async ({ values }: SaveTokenVariables) => {
    if (!project.client || !project.current || !ready || atLimit)
      throw new Error(t('projects.key.form.unavailable'));
    setBusy(true);
    try {
      const monthlyCap = validateBudget(
        values.unlimited_quota
          ? { mode: 'unlimited' }
          : { mode: 'amount', input: String(values.remain_quota) },
      );
      if (!monthlyCap.ok) throw new Error(t('projects.key.form.invalidCap'));
      const payload = normalizeTokenPayload(values);
      const body = {
        ...payload,
        name: String(values.name || '').trim(),
        model_limits:
          values.model_limits_enabled && Array.isArray(values.model_limits)
            ? values.model_limits
            : [],
        cap_microusd: monthlyCap.micro,
        // The shared amount input is a monthly cap in teams, not lifetime quota.
        unlimited_quota: true,
        remain_quota: 0,
      };
      if (editingToken) {
        await project.client.updateTokenFull(
          editingToken.id,
          project.current.id,
          { ...body, expected_version: detail.data?.version },
        );
        await invalidate('tokens');
        onOpenChange(false);
        return { data: [] };
      }
      const created = await project.client.createToken(
        project.current.id,
        body,
      );
      await invalidate('tokens');
      onOpenChange(false);
      onCreated(created);
      return { data: [] };
    } finally {
      setBusy(false);
    }
  };
  const services: TokenFormServices = {
    t,
    modelOptions: (models.data ?? []).map((model) => ({
      label: model,
      value: model,
    })),
    vendorOptions: {
      models: facts,
      vendors:
        catalog.data?.vendors.filter((vendor) =>
          vendorIds.has(String(vendor.id)),
        ) ?? [],
    },
    saveTokenMutation: { isPending: busy, mutateAsync: save },
    copyText: async () => false,
    revealKey: async () => {
      throw new Error('Create form cannot reveal existing keys');
    },
  };
  const money = (value: number | null) =>
    value === null ? t('projects.budget.unlimited') : formatUSDShort(value);
  return (
    <TokenFormDrawer
      editingToken={
        editingToken ? { ...editingToken, can_reveal_key: false } : null
      }
      open={open}
      onOpenChange={onOpenChange}
      onSecretsCreated={() => {}}
      tokens={services}
      team={{
        detailValues,
        disabled: !ready || unavailable || busy || atLimit,
        memberEffortCap: editingToken
          ? String(
              detail.data?.member_effort_cap ??
                editingToken.member_effort_cap ??
                '',
            )
          : (budget.data?.reasoning_effort_cap ?? ''),
        memberName: editingToken?.owner?.username ?? project.viewerName,
        countLabel: t('projects.key.form.count', {
          count: ownKeyCount,
          max: MAX_PERSONAL_KEYS,
        }),
        content: (
          <>
            <div className='tok-create__team-context'>
              <div>
                <b>{project.currentName}</b> ·{' '}
                {editingToken?.owner?.username ?? project.viewerName}
                <small>{t('projects.key.form.ownerPays')}</small>
              </div>
              {!editingToken && (
                <div>
                  {t('projects.key.form.monthBudget')}{' '}
                  {budget.data ? (
                    money(budget.data.budget_microusd)
                  ) : (
                    <PendingValue chars={7} />
                  )}
                  <small>
                    {t('projects.key.form.remaining')}{' '}
                    {budget.data ? (
                      money(budget.data.remaining_microusd)
                    ) : (
                      <PendingValue chars={7} />
                    )}
                  </small>
                </div>
              )}
            </div>
            {atLimit ? (
              <ErrLine>
                {t('projects.key.limitReached', { max: MAX_PERSONAL_KEYS })}
              </ErrLine>
            ) : null}
            {unavailable ? (
              <ErrLine>
                {t('projects.key.form.unavailable')}{' '}
                <Button
                  size='sm'
                  onClick={() => {
                    if (editingToken) void detail.refetch();
                    else void budget.refetch();
                    void models.refetch();
                    void catalog.refetch();
                  }}
                >
                  {t('projects.key.form.retry')}
                </Button>
              </ErrLine>
            ) : null}
          </>
        ),
      }}
    />
  );
}

/* ------------------------------------------------------------ rotate (S6.5) */

export function RotateKeyModal({
  token,
  open,
  onOpenChange,
  onRotated,
}: {
  token: ProjectToken;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRotated: (result: { key: string; old_expires_at: number | null }) => void;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const invalidate = useInvalidateProject();
  const rotationOperation = useOperationId();
  const [oldExpires, setOldExpires] = useState<'now' | '24h'>('now');
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (open) setOldExpires('now');
  }, [open]);
  const rotate = async () => {
    if (!project.client || !project.current) return;
    setBusy(true);
    try {
      const result = await project.client.rotateToken(
        token.id,
        project.current.id,
        oldExpires,
        rotationOperation.forInput(
          JSON.stringify([token.id, project.current.id, oldExpires]),
        ),
      );
      rotationOperation.completed();
      onOpenChange(false);
      await invalidate('tokens');
      onRotated(result);
    } catch (e) {
      errorReceipt(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <ActionModal
      busy={busy}
      confirmLabel={t('projects.key.rotate')}
      description={t('projects.key.rotateLede')}
      onConfirm={rotate}
      onOpenChange={onOpenChange}
      open={open}
      title={t('projects.key.rotateTitle', { name: token.name })}
    >
      <FormRows labelWidth={110}>
        <FormRow
          label={t('projects.key.oldSecret')}
          hint={t('projects.key.rotateHint')}
        >
          <SegControl<'now' | '24h'>
            disabled={busy}
            onChange={setOldExpires}
            options={[
              { value: 'now', label: t('projects.key.expireNow') },
              { value: '24h', label: t('projects.key.expire24h') },
            ]}
            value={oldExpires}
          />
        </FormRow>
      </FormRows>
    </ActionModal>
  );
}

/* ------------------------------------------------------------ delete (S10.5) */

export function DeleteKeyModal({
  token,
  open,
  onOpenChange,
  onDeleted,
}: {
  token: ProjectToken;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onDeleted: () => void;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const invalidate = useInvalidateProject();
  const [busy, setBusy] = useState(false);
  const remove = async () => {
    if (!project.client || !project.current) return;
    setBusy(true);
    try {
      await project.client.deleteToken(token.id, project.current.id);
      onOpenChange(false);
      await invalidate('tokens');
      receipt(t('projects.key.deletedReceipt', { name: token.name }));
      onDeleted();
    } catch (e) {
      errorReceipt(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <GateModal
      busy={busy}
      confirmLabel={t('page.console.tables.delete', { defaultValue: '删除' })}
      description={t('projects.key.deleteLede', {
        project: project.currentName,
      })}
      gate={{ type: 'checkbox', label: t('projects.key.deleteGate') }}
      onConfirm={remove}
      onOpenChange={onOpenChange}
      open={open}
      title={t('projects.key.deleteTitle', { name: token.name })}
    />
  );
}

export function EditKeyModal({
  token,
  onClose,
}: {
  token: ProjectToken;
  onClose: () => void;
}) {
  return (
    <CreateKeyModal
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      ownKeyCount={0}
      onCreated={() => {}}
      editingToken={token}
    />
  );
}
