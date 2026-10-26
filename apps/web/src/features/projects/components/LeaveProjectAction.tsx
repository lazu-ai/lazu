/* Leave a shared project (S5). Leaving stops every Key you own in that
 * project immediately — there is no policy and no grace period — so the gate
 * always says so and counts the Keys that are still enabled. */
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { Button } from '../../../components/ui/button';
import { useProject } from '../ProjectContext';
import { useMyProjectTokens } from '../queries';
import { errorReceipt, receipt } from '../receipt';
import { GateModal } from './atoms';

export function LeaveProjectAction() {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const tokens = useMyProjectTokens({ enabled: open });
  if (
    !project.isShared ||
    project.role === 'owner' ||
    project.current?.status !== 'active'
  )
    return null;
  const close = () => setOpen(false);
  const leave = async () => {
    if (!project.client || !project.current) return;
    setBusy(true);
    try {
      await project.client.leaveProject(project.current.id);
      await project.refreshProjects();
      receipt(t('projects.leave.done'));
      close();
      navigate('/console');
    } catch (error) {
      errorReceipt(error);
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <Button variant='ghost-danger' size='sm' onClick={() => setOpen(true)}>
        {t('projects.leave.action')}
      </Button>
      <GateModal
        open={open}
        onOpenChange={(next) => {
          if (!next) close();
        }}
        busy={busy}
        disabled={!tokens.data}
        title={t('projects.leave.title', { name: project.currentName })}
        description={t('projects.leave.description')}
        gate={{
          type: 'checkbox',
          label: t('projects.leave.gate', {
            count:
              tokens.data?.filter((key) => key.status === 'enabled').length ??
              0,
          }),
        }}
        confirmLabel={t('projects.leave.action')}
        onConfirm={leave}
      />
    </>
  );
}
