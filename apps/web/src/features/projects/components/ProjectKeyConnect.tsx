import { useQuery } from '@tanstack/react-query';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '../../../components/ui/button';
import { InlineNotice } from '../../../components/ui/inline-notice';
import { Modal } from '../../../components/ui/modal';
import {
  fetchTokenVendorOptions,
  type TokenRecord,
} from '../../../hooks/tokens/useTokensQuery';
import { isLazuApiError } from '../../../lib/api-error';
import {
  ConnectModal,
  type ConnectClient,
} from '../../../pages/Token/connect/ConnectModal';
import '../../../pages/Token/token-page.css';
import { REVEAL_REMASK_SECONDS } from '../budget';
import { useProject } from '../ProjectContext';
import { useInvalidateProject, useProjectModels } from '../queries';
import { projectsQueryRoot } from '../queryKeys';
import { receipt } from '../receipt';
import { CreatedSecret } from './KeyDialogs';
import { type ProjectToken } from '../types';

const connectRecord = (token: ProjectToken): TokenRecord => ({
  ...token,
  model_limits_enabled:
    token.model_limits_enabled ?? token.model_limits.length > 0,
  can_reveal_key: token.capabilities.can_reveal && !token.stopped_by_removal,
});

/** Reuse the personal Key connection UI, with project-scoped reads and writes. */
export function ProjectKeyConnect({
  token,
  secret,
  onClose,
}: {
  token: ProjectToken;
  secret?: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const project = useProject();
  const invalidate = useInvalidateProject();
  const models = useProjectModels();
  const vendors = useQuery({
    queryKey: [...projectsQueryRoot(project.current?.id), 'connect-catalog'],
    queryFn: fetchTokenVendorOptions,
    retry: false,
  });
  const record = useMemo(() => connectRecord(token), [token]);
  const [secretExpiresAt, setSecretExpiresAt] = useState(() =>
    secret ? Date.now() + REVEAL_REMASK_SECONDS * 1000 : undefined,
  );
  const recovery =
    vendors.isError || models.isError ? (
      <InlineNotice
        tone='danger'
        title={t('projects.common.loadFailed', { ns: 'projects' })}
        actions={
          <Button
            onClick={() =>
              void Promise.all([vendors.refetch(), models.refetch()])
            }
          >
            {t('projects.common.retry', { ns: 'projects' })}
          </Button>
        }
      />
    ) : null;
  if (!vendors.data || !models.data) {
    return (
      <Modal
        open
        onOpenChange={(open) => {
          if (!open) onClose();
        }}
        title={t('page.console.token.connect.title')}
        body={
          <>
            {secret ? (
              <CreatedSecret
                name={token.name}
                secret={secret}
                note=''
                expiresAt={secretExpiresAt}
              />
            ) : null}
            {recovery ?? (
              <span>{t('projects.common.loading', { ns: 'projects' })}</span>
            )}
          </>
        }
      />
    );
  }
  const client: ConnectClient = {
    t,
    modelOptions: models.data.map((model) => ({ label: model, value: model })),
    vendorOptions: vendors.data,
    revealKey: async (id) => {
      if (!project.client || !project.current)
        throw new Error('Project unavailable');
      const result = await project.client.revealToken(
        String(id),
        project.current.id,
      );
      if (result.notification_status === 'queued')
        receipt(
          t('projects.key.notified', {
            ns: 'projects',
            name: token.owner?.username,
          }),
        );
      setSecretExpiresAt(Date.now() + REVEAL_REMASK_SECONDS * 1000);
      return result.key;
    },
    updateTokenConnect: async (id, fields) => {
      if (!project.client || !project.current)
        throw new Error('Project unavailable');
      try {
        const updated = await project.client.updateTokenConnect(
          String(id),
          project.current.id,
          fields,
        );
        await invalidate('tokens');
        return { success: true, data: connectRecord(updated) };
      } catch (error) {
        if (isLazuApiError(error) && error.code === 'version_conflict') {
          try {
            await invalidate('tokens');
          } finally {
            onClose();
          }
        }
        throw error;
      }
    },
  };
  return (
    <ConnectModal
      open
      record={record}
      tokens={client}
      initialSecret={secret}
      secretExpiresAt={secretExpiresAt}
      notice={recovery}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    />
  );
}
