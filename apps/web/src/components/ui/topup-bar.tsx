/* The wallet's top-up bar — one full-width row under the balance.
 *
 * Six preset amounts share the row with an amount field the reader can type
 * into directly; there is no separate "custom" button to open first. Typing
 * clears the preset highlight, clearing the field brings the preset back.
 * Amounts are whole dollars because checkout sells $1 units. The pay button,
 * the balance after payment and the days it lasts all follow the one amount
 * that is currently chosen; an invalid amount disables payment and says why
 * in one line under the row.
 */
import { CreditCard } from 'lucide-react';
import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from './button';
import { PendingValue } from './pending';
import { useDelayedReveal } from '../../hooks/common/useDelayedReveal';
import { cn } from '../../lib/cn';
import type { ReactNode } from 'react';

export const TOPUP_PRESETS = [10, 20, 50, 100, 200, 500] as const;
export const TOPUP_DEFAULT = 20;

type TopupBarProps = {
  title: ReactNode;
  /* One short line beside the title: who can pay, or a hint. */
  note?: ReactNode;
  /* Current balance and average daily spend in USD; null while unknown. */
  balanceUSD: number | null;
  balancePending: boolean;
  dailyAvgUSD?: number | null;
  min: number;
  max: number;
  presets?: readonly number[];
  /* The amount the bar opens on. Not one of the presets → it is typed into
     the field, so a remembered custom amount comes back as the reader left
     it. */
  initialAmount?: number;
  /* Payment method line, or method choices when a deployment offers several. */
  method: ReactNode;
  onPay: (amountUSD: number) => void;
  paying?: boolean;
  disabled?: boolean;
  inputId?: string;
  className?: string;
  /* Extra credit a payment of this amount earns (the first-top-up bonus),
     folded into the balance after payment; 0 or absent when none. */
  bonusUSD?: (amountUSD: number) => number;
  /* How that extra credit is named beside the balance after payment. */
  bonusLabel?: (bonusUSD: number) => ReactNode;
};

const usd = (value: number) =>
  `$${value.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })}`;

const whole = (value: number) => `$${value.toLocaleString('en-US')}`;

export function TopupBar({
  title,
  note,
  balanceUSD,
  balancePending,
  dailyAvgUSD,
  min,
  max,
  presets = TOPUP_PRESETS,
  initialAmount = TOPUP_DEFAULT,
  method,
  onPay,
  paying = false,
  disabled = false,
  inputId,
  className,
  bonusUSD,
  bonusLabel,
}: TopupBarProps) {
  const { t } = useTranslation();
  const ns = 'page.console.wallet.topupBar';
  const revealPending = useDelayedReveal(balancePending);
  const pending = (
    <PendingValue
      chars={20}
      className={!revealPending ? 'invisible' : undefined}
    />
  );
  const generatedId = useId();
  const fieldId = inputId ?? `topup-amount-${generatedId}`;
  const errorId = `${fieldId}-error`;
  const opensOnPreset = presets.includes(initialAmount);
  const [preset, setPreset] = useState<number>(
    opensOnPreset ? initialAmount : TOPUP_DEFAULT,
  );
  const [typed, setTyped] = useState(
    opensOnPreset ? '' : String(initialAmount),
  );

  const usingTyped = typed !== '';
  const selectedAmount = usingTyped ? Number(typed) : preset;
  const error =
    (usingTyped && !/^\d+$/.test(typed)) ||
    !Number.isSafeInteger(selectedAmount)
      ? t(`${ns}.errorWhole`)
      : selectedAmount < min
        ? t(`${ns}.errorMin`, { min: whole(min) })
        : selectedAmount > max
          ? t(`${ns}.errorMax`, { max: whole(max) })
          : '';
  const amount = error ? null : selectedAmount;
  const bonus = amount !== null && bonusUSD ? Math.max(0, bonusUSD(amount)) : 0;
  const after =
    amount !== null && balanceUSD !== null ? balanceUSD + amount + bonus : null;
  const days =
    after !== null && dailyAvgUSD && dailyAvgUSD > 0
      ? Math.round(after / dailyAvgUSD)
      : null;

  return (
    <section className={cn('lazu-topup-bar', className)}>
      <header className='lazu-topup-bar__head'>
        <div className='lazu-topup-bar__title'>
          <h2>{title}</h2>
          <span className='lazu-topup-bar__balance-slot'>
            {balancePending
              ? pending
              : balanceUSD === null
                ? t(`${ns}.balanceUnknown`)
                : dailyAvgUSD && dailyAvgUSD > 0
                  ? t(`${ns}.balanceDaily`, {
                      balance: usd(balanceUSD),
                      daily: usd(dailyAvgUSD),
                    })
                  : t(`${ns}.balance`, { balance: usd(balanceUSD) })}
          </span>
        </div>
        {note ? <span className='lazu-topup-bar__note'>{note}</span> : null}
      </header>
      <div
        aria-label={t(`${ns}.amounts`)}
        className='lazu-topup-bar__amounts'
        role='group'
      >
        {presets.map((value) => (
          <button
            aria-pressed={!usingTyped && preset === value}
            className='lazu-topup-bar__preset'
            data-owns-chrome=''
            key={value}
            onClick={() => {
              setPreset(value);
              setTyped('');
            }}
            type='button'
          >
            {whole(value)}
          </button>
        ))}
        <label
          className='lazu-topup-bar__field'
          data-active={usingTyped && !error ? '' : undefined}
          data-invalid={error ? '' : undefined}
          htmlFor={fieldId}
        >
          <span aria-hidden='true'>$</span>
          <input
            aria-describedby={error ? errorId : undefined}
            aria-invalid={error ? true : undefined}
            aria-label={t(`${ns}.otherLabel`)}
            autoComplete='off'
            id={fieldId}
            inputMode='numeric'
            onChange={(event) => setTyped(event.target.value)}
            placeholder={t(`${ns}.otherPlaceholder`, {
              min: whole(min),
              max: whole(max),
            })}
            value={typed}
          />
        </label>
      </div>
      {error ? (
        <p className='lazu-topup-bar__error' id={errorId} role='alert'>
          {error}
        </p>
      ) : null}
      <footer className='lazu-topup-bar__foot'>
        <div className='lazu-topup-bar__fact'>
          <span>{t(`${ns}.after`)}</span>
          <b className='lazu-topup-bar__balance-slot'>
            {balancePending
              ? pending
              : after === null
                ? '—'
                : days !== null
                  ? t(`${ns}.afterDays`, { balance: usd(after), days })
                  : usd(after)}
          </b>
          {bonus > 0 && bonusLabel ? (
            <span className='lazu-topup-bar__bonus'>{bonusLabel(bonus)}</span>
          ) : null}
        </div>
        <div className='lazu-topup-bar__fact'>
          <span>{t(`${ns}.method`)}</span>
          <div>{method}</div>
        </div>
        <Button
          className='lazu-topup-bar__pay'
          disabled={disabled || amount === null}
          loading={paying}
          onClick={() => {
            if (amount !== null) onPay(amount);
          }}
          variant='primary'
        >
          <CreditCard aria-hidden='true' size={15} />
          {amount === null
            ? t(`${ns}.payEmpty`)
            : t(`${ns}.pay`, { amount: whole(amount) })}
        </Button>
      </footer>
    </section>
  );
}
