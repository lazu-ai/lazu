/* The ordinary "邀请成员" dialog: 440px, one column, the send button the
 * width of the form. It used to be a 620px two-column sheet with an eyebrow,
 * a two-line title, a three-line lede and a "what happens after" box — most
 * of which repeated the members page it opened from. The first invitation of
 * a default project (R2) is normally the inline form on 成员与预算; callers
 * that still open it as a dialog pass `first` and get the project-name field.
 * The dialog cannot close while a send is in flight. */
import { useTranslation } from 'react-i18next';
import { Button } from '../../../components/ui/button';
import { Modal } from '../../../components/ui/modal';
import { INVITE_VALID_DAYS } from '../budget';
import { useProject } from '../ProjectContext';
import { InviteFields, useInviteDraft } from './InviteForm';
import type { ProjectDetail } from '../types';

export function InviteModal({
  open,
  onOpenChange,
  first = false,
  detail,
  onInvited,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** First invitation of an unshared default project (R2). */
  first?: boolean;
  detail?: ProjectDetail | null;
  /** Kept for callers that still pass it; the dialog no longer prints it. */
  balanceMicro?: number | null;
  onInvited?: () => void;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  /* The dialog is the surface that names a project as it upgrades one, so it
     keeps the name field and sends it. */
  const draft = useInviteDraft({
    active: open,
    detail,
    first,
    namesProject: first,
    onInvited,
    ready: Boolean(detail),
  });

  return (
    <Modal
      busy={draft.busy}
      className='pj-modal pj-modal--invite'
      closeLabel={t('common.close', { defaultValue: '关闭' })}
      onOpenChange={onOpenChange}
      open={open}
      title={
        first
          ? t('projects.empty.cta')
          : t('projects.invite.title', { name: project.currentName })
      }
      description={
        first ? t('projects.invite.firstLede') : t('projects.invite.lede')
      }
      body={
        <form
          className='pj-modal__body'
          onSubmit={async (event) => {
            event.preventDefault();
            if (!draft.canSend) return;
            if (await draft.send()) onOpenChange(false);
          }}
        >
          <InviteFields draft={draft} namesProject={first} />
          <Button
            disabled={!draft.canSend}
            fullWidth
            loading={draft.busy}
            loadingLabel={t('projects.invite.sending')}
            type='submit'
            variant='primary'
          >
            {t('projects.invite.sendWithDays', { days: INVITE_VALID_DAYS })}
          </Button>
          {/* One line, under the button it qualifies. Esc and the ✕ close
              this; a Cancel button beside Send only competes with it. */}
          <p className='pj-invite__after'>
            {t('projects.invite.afterShortDialog')}
          </p>
        </form>
      }
    />
  );
}
