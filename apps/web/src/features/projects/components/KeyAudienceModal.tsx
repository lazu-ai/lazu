/* The fork in front of "创建密钥", for a project that is still solo.
 *
 * The behaviour this intercepts is real and invisible to us: someone who
 * wants a colleague to have access copies their own key into a chat window.
 * Asking who the key is for, at the moment they are about to do it, is the
 * only place in the console where that choice is still open.
 *
 * It asks once. After the first answer the drawer opens straight away, so
 * the people who only ever make keys for themselves pay for this exactly
 * one time. */
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { InviteModal } from './InviteModal';
import { Button, Modal } from '../../../components/ui';
import { useProject } from '../ProjectContext';
import { useProjectDetail } from '../queries';

const STORE_PREFIX = 'lazu:key-audience-asked:';

const storeKey = (viewerId: string) => `${STORE_PREFIX}${viewerId || 'anon'}`;

/** True when this viewer has never answered the fork. */
export const shouldAskKeyAudience = (viewerId: string) => {
  try {
    return localStorage.getItem(storeKey(viewerId)) !== '1';
  } catch {
    return false;
  }
};

export const rememberKeyAudienceAsked = (viewerId: string) => {
  try {
    localStorage.setItem(storeKey(viewerId), '1');
  } catch {
    /* storage unavailable — the fork simply asks again next time */
  }
};

export function KeyAudienceModal({
  open,
  onOpenChange,
  onSelf,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Chosen "for me": carry on to the ordinary create-key drawer. */
  onSelf: () => void;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const detail = useProjectDetail();
  const [inviting, setInviting] = useState(false);

  const answer = (self: boolean) => {
    rememberKeyAudienceAsked(project.viewerId);
    onOpenChange(false);
    if (self) onSelf();
    else setInviting(true);
  };

  return (
    <>
      <Modal
        body={
          <div className='pj-fork'>
            <button
              className='pj-fork__opt'
              data-on=''
              data-owns-chrome=''
              onClick={() => answer(true)}
              type='button'
            >
              <b>{t('projects.fork.self')}</b>
              <span>{t('projects.fork.selfBody')}</span>
            </button>
            <button
              className='pj-fork__opt'
              data-owns-chrome=''
              onClick={() => answer(false)}
              type='button'
            >
              <b>
                {t('projects.fork.mate')}
                <em className='pj-fork__tag'>{t('projects.fork.tag')}</em>
              </b>
              <span>{t('projects.fork.mateBody')}</span>
            </button>
          </div>
        }
        className='pj-modal'
        closeLabel={t('common.close', { defaultValue: '关闭' })}
        footer={
          <Button
            onClick={() => {
              rememberKeyAudienceAsked(project.viewerId);
              onOpenChange(false);
            }}
            size='sm'
            variant='secondary'
          >
            {t('projects.common.never_mind')}
          </Button>
        }
        onOpenChange={(next) => {
          if (!next) rememberKeyAudienceAsked(project.viewerId);
          onOpenChange(next);
        }}
        open={open}
        title={
          <span className='pj-modal__titlewrap'>
            <span className='pj-eyebrow'>{t('projects.fork.eyebrow')}</span>
            <span>{t('projects.fork.title')}</span>
          </span>
        }
      />
      <InviteModal
        balanceMicro={
          detail.data?.balance_microusd ?? project.current?.balanceMicro
        }
        detail={detail.data}
        first
        onOpenChange={setInviting}
        open={inviting}
      />
    </>
  );
}
