import { Check } from 'lucide-react';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import './onboarding.css';
import { showSuccess } from '../../helpers/utils';
import { Button } from '../ui';
import type { OnboardingState } from './useOnboarding';

type Props = {
  accountLabel: string;
  apiBaseURL: string;
  onDismiss: () => void;
  state: OnboardingState;
};

type Step = {
  done: boolean;
  hint: string;
  key: string;
  label: string;
};

const API_KEY_PLACEHOLDER = 'YOUR_LAZU_API_KEY';
const MODEL_ID_PLACEHOLDER = 'YOUR_MODEL_ID';

/**
 * First-run takeover for the overview's empty state. A brand new account has no
 * usage to chart, so the space that would hold an empty chart carries the steps
 * that lead to the first request instead.
 */
const OnboardingTakeover = ({
  accountLabel,
  apiBaseURL,
  onDismiss,
  state,
}: Props) => {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);

  const command = useMemo(
    () =>
      `curl ${apiBaseURL}/chat/completions \\
  -H "Authorization: Bearer ${API_KEY_PLACEHOLDER}" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"${MODEL_ID_PLACEHOLDER}",
       "messages":[{"role":"user","content":"hello"}]}'`,
    [apiBaseURL],
  );

  const steps: Step[] = [
    {
      done: state.has_key,
      hint: state.has_key
        ? t('page.console.onboarding.step.keyDone')
        : t('page.console.onboarding.step.keyTodo'),
      key: 'key',
      label: t('page.console.onboarding.step.key'),
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
    },
  ];

  if (state.needs_password) {
    steps.push({
      done: state.password_set,
      hint: t('page.console.onboarding.step.passwordHint'),
      key: 'password',
      label: t('page.console.onboarding.step.password'),
    });
  }

  const needsSettings =
    !state.has_alert || (state.needs_password && !state.password_set);

  const copyCommand = async () => {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      showSuccess(t('page.console.onboarding.copied'));
    } catch {
      setCopied(false);
    }
  };

  return (
    <section
      aria-labelledby='onboarding-takeover-title'
      className='onb-takeover'
    >
      <h2 className='onb-takeover__title' id='onboarding-takeover-title'>
        {t('page.console.onboarding.takeoverTitle')}
      </h2>
      <p className='onb-takeover__sub'>{accountLabel}</p>

      <div className='onb-takeover__body'>
        <ol className='onb-steps'>
          {steps.map((step, index) => (
            <li className='onb-step' key={step.key}>
              <span
                className='onb-step__num'
                data-done={step.done || undefined}
              >
                {step.done ? <Check aria-hidden='true' size={11} /> : index + 1}
              </span>
              <div>
                <span className='onb-step__label'>
                  {step.label}{' '}
                  <span className='sr-only'>
                    (
                    {t(
                      step.done
                        ? 'page.console.onboarding.step.statusDone'
                        : 'page.console.onboarding.step.statusPending',
                    )}
                    )
                  </span>
                </span>
                <small className='onb-step__hint'>{step.hint}</small>
              </div>
            </li>
          ))}
        </ol>

        <div>
          <pre className='onb-code'>{command}</pre>
          <p className='onb-code__hint'>
            {t('page.console.onboarding.commandHint')}
          </p>
          <button
            aria-live='polite'
            className='onb-code__copy'
            onClick={copyCommand}
            type='button'
          >
            {copied
              ? t('page.console.onboarding.copied')
              : t('page.console.onboarding.copyCommand')}
          </button>
        </div>
      </div>

      <div className='onb-takeover__actions'>
        {state.has_key ? null : (
          <Button
            render={<Link to='/console/token' />}
            size='sm'
            variant='primary'
          >
            {t('page.console.onboarding.createKey')}
          </Button>
        )}
        {needsSettings ? (
          <Link className='onb-link onb-step__action' to='/console/personal'>
            {t('page.console.onboarding.goSettings')}
          </Link>
        ) : null}
        <button className='onb-dismiss' onClick={onDismiss} type='button'>
          {t('page.console.onboarding.skip')}
        </button>
      </div>
    </section>
  );
};

export default OnboardingTakeover;
