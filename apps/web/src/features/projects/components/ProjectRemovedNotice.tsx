/* Full-page notice when the project the reader was looking at is gone —
 * removed, suspended out, or dissolved. Not a 403: one exit, back to the
 * reader's own project. */
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { Button, StatusPage } from '../../../components/ui';
import { useProject } from '../ProjectContext';

export function ProjectRemovedNotice() {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const navigate = useNavigate();
  const { removedNotice, dismissRemoved, projects, switchProject } =
    useProject();
  if (!removedNotice) return null;
  const own = projects.find((project) => project.is_default) ?? projects[0];

  const back = () => {
    if (own) switchProject(own.id);
    dismissRemoved();
    navigate('/console');
  };

  return (
    <StatusPage
      action={
        <Button variant='primary' onClick={back}>
          {t('projects.removed.back')}
        </Button>
      }
      description={t('projects.removed.body', {
        name: removedNotice.projectName,
      })}
      live
      mark='Removed'
      title={t('projects.removed.title', { name: removedNotice.projectName })}
    />
  );
}
