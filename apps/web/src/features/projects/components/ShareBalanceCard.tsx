/* The wallet's own way in. A team is how one balance gets spent by several
 * people, so the offer belongs next to the balance rather than on a page
 * about something else. Shown only while the project is still solo. */
import { UsersRound } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { InviteModal } from './InviteModal';
import { Button } from '../../../components/ui';
import { useProject } from '../ProjectContext';
import { useProjectDetail } from '../queries';

export function ShareBalanceCard() {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const detail = useProjectDetail();
  const [open, setOpen] = useState(false);
  if (!project.isSoloOwner) return null;
  return (
    <>
      <section className='pj-share'>
        <span aria-hidden='true' className='pj-share__mark'>
          <UsersRound size={22} />
        </span>
        <div className='pj-share__body'>
          <h2 className='pj-share__title'>{t('projects.share.title')}</h2>
          <p className='pj-share__sub'>{t('projects.share.sub')}</p>
        </div>
        <Button onClick={() => setOpen(true)} size='sm' variant='secondary'>
          {t('projects.solo.inviteButton')}
        </Button>
      </section>
      <InviteModal
        balanceMicro={
          detail.data?.balance_microusd ?? project.current?.balanceMicro
        }
        detail={detail.data}
        first
        onOpenChange={setOpen}
        open={open}
      />
    </>
  );
}
