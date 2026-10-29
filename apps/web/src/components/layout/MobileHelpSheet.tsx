import { Activity, CircleHelp, Flag, Mail, Send } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { preloadConsoleRoute } from './routePreload';
import {
  getCommunityPlatform,
  getCommunityURL,
  getSupportEmail,
} from '../../helpers/brandUrls';
import { useSidebar } from '../../hooks/common/useSidebar';
import { openReportProblem } from '../support/reportChannel';
import { Modal } from '../ui/modal';

/* The phone's Help. The desktop top bar has a Help menu and a community
 * entry; the phone bar had neither, so a reader on a phone could not report
 * a problem, write to support or find the community at all. One button in
 * the bar opens a sheet with those entries, community first. Docs, theme and
 * language stay in the drawer, where they already are. */
const MobileHelpSheet = () => {
  const { t } = useTranslation();
  const { isModuleVisible } = useSidebar();
  const [open, setOpen] = useState(false);

  const communityHref = getCommunityURL();
  const communityName = getCommunityPlatform(communityHref);
  const supportEmail = getSupportEmail();
  const showModelStatus = isModuleVisible('workspace', 'channel_health');

  const reportProblem = () => {
    setOpen(false);
    openReportProblem();
  };

  return (
    <>
      <button
        aria-label={t('console.topbar.helpSheet', {
          defaultValue: '帮助与支持',
        })}
        aria-expanded={open}
        aria-haspopup='dialog'
        className='mobile-console-help-trigger'
        onClick={() => setOpen(true)}
        type='button'
      >
        <CircleHelp aria-hidden='true' size={18} />
      </button>
      <Modal
        body={
          <nav
            aria-label={t('console.topbar.helpSheet', {
              defaultValue: '帮助与支持',
            })}
            className='mobile-help-sheet'
          >
            {communityHref ? (
              <a
                className='mobile-help-sheet__item'
                href={communityHref}
                onClick={() => setOpen(false)}
                rel='noopener noreferrer'
                target='_blank'
              >
                <Send aria-hidden='true' size={18} />
                <span className='mobile-help-sheet__text'>
                  <span>
                    {communityName
                      ? t('console.topbar.communityNamed', {
                          defaultValue: '{{name}} 交流群',
                          name: communityName,
                        })
                      : t('console.topbar.community', {
                          defaultValue: '加入社区',
                        })}
                  </span>
                </span>
                <span aria-hidden='true' className='mobile-help-sheet__out'>
                  ↗
                </span>
              </a>
            ) : null}
            <button
              className='mobile-help-sheet__item'
              onClick={reportProblem}
              type='button'
            >
              <Flag aria-hidden='true' size={18} />
              <span className='mobile-help-sheet__text'>
                <span>
                  {t('console.topbar.reportProblem', {
                    defaultValue: '报告问题',
                  })}
                </span>
              </span>
            </button>
            <a
              className='mobile-help-sheet__item'
              href={`mailto:${supportEmail}`}
              onClick={() => setOpen(false)}
            >
              <Mail aria-hidden='true' size={18} />
              <span className='mobile-help-sheet__text'>
                <span>
                  {t('console.topbar.contactSupport', {
                    defaultValue: '联系支持',
                  })}
                </span>
                <small>{supportEmail}</small>
              </span>
            </a>
            {showModelStatus ? (
              <Link
                className='mobile-help-sheet__item'
                onClick={() => setOpen(false)}
                onFocus={() => preloadConsoleRoute('/console/health')}
                onPointerEnter={() => preloadConsoleRoute('/console/health')}
                to='/console/health'
              >
                <Activity aria-hidden='true' size={18} />
                <span className='mobile-help-sheet__text'>
                  <span>
                    {t('nav.modelStatus', { defaultValue: '模型状态' })}
                  </span>
                </span>
              </Link>
            ) : null}
          </nav>
        }
        onOpenChange={setOpen}
        open={open}
        sheet
        title={t('console.topbar.helpSheet', { defaultValue: '帮助与支持' })}
      />
    </>
  );
};

export default MobileHelpSheet;
