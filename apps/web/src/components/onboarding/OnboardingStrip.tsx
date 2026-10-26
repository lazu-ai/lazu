import { Check } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import './onboarding.css';
import type { OnboardingState } from './useOnboarding';

type Props = {
  onDismiss: () => void;
  state: OnboardingState;
};

type Cell = {
  done: boolean;
  hint: string;
  key: string;
  label: string;
  link?: { label: string; to: string };
};

/**
 * The takeover's downgraded form. Once the account is past its empty state the
 * checklist collapses into one hairline row that follows the KPI grammar, so it
 * sits above the real data without competing with it.
 */
const OnboardingStrip = ({ onDismiss, state }: Props) => {
  const { t } = useTranslation();

  const cells: Cell[] = [
    {
      done: state.has_key,
      hint: state.has_key
        ? t('page.console.onboarding.step.keyDone')
        : t('page.console.onboarding.step.keyTodo'),
      key: 'key',
      label: t('page.console.onboarding.step.key'),
      link: state.has_key
        ? undefined
        : {
            label: t('page.console.onboarding.goCreate'),
            to: '/console/token',
          },
    },
    {
      done: state.has_request,
      hint: t('page.console.onboarding.step.requestHint'),
      key: 'request',
      label: t('page.console.onboarding.step.request'),
    },
    {
      done: state.has_alert,
      hint: t('page.console.onboarding.step.alertHint'),
      key: 'alert',
      label: t('page.console.onboarding.step.alert'),
      link: state.has_alert
        ? undefined
        : {
            label: t('page.console.onboarding.goSettings'),
            to: '/console/personal',
          },
    },
  ];

  if (state.needs_password) {
    cells.push({
      done: state.password_set,
      hint: t('page.console.onboarding.step.passwordHint'),
      key: 'password',
      label: t('page.console.onboarding.step.password'),
      link: state.password_set
        ? undefined
        : {
            label: t('page.console.onboarding.goSettings'),
            to: '/console/personal',
          },
    });
  }

  const doneCount = cells.filter((cell) => cell.done).length;

  return (
    <section aria-labelledby='onboarding-strip-title' className='onb-strip'>
      <div className='onb-strip__head'>
        <h2 className='onb-strip__title' id='onboarding-strip-title'>
          {t('page.console.onboarding.stripTitle')}
        </h2>
        <span className='onb-strip__meta'>
          <span className='onb-strip__count'>
            {doneCount}/{cells.length}
          </span>
          <button className='onb-dismiss' onClick={onDismiss} type='button'>
            {t('page.console.onboarding.collapse')}
          </button>
        </span>
      </div>
      <div className='onb-strip__rows' data-count={cells.length}>
        {cells.map((cell) => (
          <div className='onb-strip__cell' key={cell.key}>
            <span className='onb-strip__state'>
              <span className='onb-check' data-done={cell.done || undefined}>
                <Check aria-hidden='true' size={9} />
              </span>
              {cell.label}{' '}
              <span className='sr-only'>
                (
                {t(
                  cell.done
                    ? 'page.console.onboarding.step.statusDone'
                    : 'page.console.onboarding.step.statusPending',
                )}
                )
              </span>
            </span>
            <small className='onb-strip__hint'>{cell.hint}</small>
            {cell.link ? (
              <Link className='onb-link' to={cell.link.to}>
                {cell.link.label}
              </Link>
            ) : null}
          </div>
        ))}
      </div>
    </section>
  );
};

export default OnboardingStrip;
