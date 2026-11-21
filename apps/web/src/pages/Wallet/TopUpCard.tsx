/* The wallet's 充值 tab: two cards side by side. The account card holds the
 * balance (the view's one large figure) and the account's own low-balance
 * mail switch; the amount card holds five preset tiles and 自定义, and the
 * pay button that names the amount. The method is chosen on the checkout
 * page, so a single-method deployment shows no method control here. On a
 * phone the cards stack. */
import { Fragment, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import { PendingValue } from '../../components/ui/pending';
import { Switch } from '../../components/ui/switch';
import { topupPresetsWithin } from '../../components/ui/topup-bar';
import type { ReactNode } from 'react';

const WALLET_PRESETS = [10, 20, 50, 100, 500] as const;
const WALLET_DEFAULT_AMOUNT = 20;

const ns = 'page.console.wallet.topup';

export type PayMethod = {
  name: string;
  status?: string;
  type: string;
};

export type LowBalanceMail = {
  available: boolean;
  on: boolean;
  thresholdUSD: number;
};

type TopUpCardProps = {
  balanceUSD: number | null;
  balancePending: boolean;
  bonusLabel?: (bonusUSD: number) => ReactNode;
  bonusUSD?: (amountUSD: number) => number;
  /* The amount the reader last took to checkout, when one is remembered. */
  rememberedAmount?: number;
  lowBalance: LowBalanceMail | null;
  lowBalanceSaving: boolean;
  max: number;
  method: string;
  methods: PayMethod[];
  min: number;
  note?: ReactNode;
  onLowBalanceChange: (on: boolean) => void;
  onMethodChange: (method: string) => void;
  onPay: (amountUSD: number) => void;
  paying: boolean;
};

const formatUSD = (value: number) =>
  `$${value.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })}`;

const whole = (value: number) => `$${value.toLocaleString('en-US')}`;

/* A threshold reads as $5, or $2.50 when it has cents. */
const threshold = (value: number) =>
  Number.isInteger(value) ? whole(value) : formatUSD(value);

export function TopUpCard({
  balanceUSD,
  balancePending,
  bonusLabel,
  bonusUSD,
  rememberedAmount,
  lowBalance,
  lowBalanceSaving,
  max,
  method,
  methods,
  min,
  note,
  onLowBalanceChange,
  onMethodChange,
  onPay,
  paying,
}: TopUpCardProps) {
  const { t } = useTranslation();
  const presets = topupPresetsWithin(WALLET_PRESETS, min, max);
  const fallback = presets.includes(WALLET_DEFAULT_AMOUNT)
    ? WALLET_DEFAULT_AMOUNT
    : presets[0];
  /* A remembered amount off the tiles comes back typed into 自定义. */
  const opensTyped =
    rememberedAmount !== undefined && !presets.includes(rememberedAmount);
  /* null = the 自定义 tile is picked and the field below it is in charge. */
  const [preset, setPreset] = useState<number | null>(
    opensTyped ? null : (rememberedAmount ?? fallback),
  );
  const [typed, setTyped] = useState(
    opensTyped ? String(rememberedAmount) : '',
  );
  const customRef = useRef<HTMLInputElement>(null);
  const custom = preset === null;
  const chosen = custom ? Number(typed) : preset;
  const error = !custom
    ? ''
    : typed === ''
      ? ''
      : !/^\d+$/.test(typed) || !Number.isSafeInteger(chosen)
        ? t(`${ns}.errorWhole`)
        : chosen < min
          ? t(`${ns}.errorMin`, { min: whole(min) })
          : chosen > max
            ? t(`${ns}.errorMax`, { max: whole(max) })
            : '';
  const amount = (custom && (typed === '' || error)) || !chosen ? null : chosen;
  const bonus = amount !== null && bonusUSD ? Math.max(0, bonusUSD(amount)) : 0;
  const low =
    balanceUSD !== null &&
    lowBalance !== null &&
    lowBalance.thresholdUSD > 0 &&
    balanceUSD < lowBalance.thresholdUSD;

  return (
    <div className='wal-topup'>
      <section
        aria-labelledby='wal-topup-balance'
        className='wal-card wal-topup__account'
      >
        <div className='wal-topup__account-head'>
          <span className='wal-label' id='wal-topup-balance'>
            {t(`${ns}.balance`)}
          </span>
          {low ? <Badge variant='warning'>{t(`${ns}.low`)}</Badge> : null}
        </div>
        {balancePending ? (
          <PendingValue chars={6} />
        ) : (
          <p className='wal-hero wal-topup__balance-value'>
            {balanceUSD === null ? '—' : formatUSD(balanceUSD)}
          </p>
        )}
        {lowBalance?.available ? (
          <div className='wal-topup__remind'>
            <span id='wal-remind-label'>
              {/* The threshold is set in figures inside the sentence; the
                  sentence's word order is the locale's. */}
              {t(`${ns}.remind`, { amount: '\u0000' })
                .split('\u0000')
                .map((part, index) =>
                  index === 0 ? (
                    part
                  ) : (
                    <Fragment key={part}>
                      <span className='wal-figure'>
                        {threshold(lowBalance.thresholdUSD)}
                      </span>
                      {part}
                    </Fragment>
                  ),
                )}
            </span>
            <Switch
              aria-labelledby='wal-remind-label'
              checked={lowBalance.on}
              className='wal-switch'
              disabled={lowBalanceSaving}
              onCheckedChange={(checked) => onLowBalanceChange(checked)}
            />
          </div>
        ) : null}
      </section>

      <section
        aria-labelledby='wal-topup-title'
        className='wal-card wal-topup__pick'
      >
        <h2 className='wal-h2' id='wal-topup-title'>
          {t(`${ns}.amount`)}
        </h2>
        <div
          aria-labelledby='wal-topup-title'
          className='wal-topup__tiles'
          role='group'
        >
          {presets.map((value) => (
            <button
              aria-pressed={preset === value}
              className='wal-tile'
              data-owns-chrome=''
              key={value}
              onClick={() => setPreset(value)}
              type='button'
            >
              {whole(value)}
            </button>
          ))}
          <button
            aria-pressed={custom}
            className='wal-tile wal-tile--custom'
            data-owns-chrome=''
            onClick={() => {
              setPreset(null);
              requestAnimationFrame(() => customRef.current?.focus());
            }}
            type='button'
          >
            {t(`${ns}.custom`)}
          </button>
        </div>
        {custom ? (
          <div className='wal-topup__custom-field'>
            <label className='wal-label' htmlFor='wallet-custom-amount'>
              {t(`${ns}.customField`)}
            </label>
            <div className='wal-topup__custom-wrap'>
              <span aria-hidden='true' className='wal-topup__custom-unit'>
                $
              </span>
              <Input
                aria-describedby={error ? 'wal-topup-error' : undefined}
                aria-invalid={error ? true : undefined}
                autoComplete='off'
                className='wal-topup__custom'
                ref={customRef}
                id='wallet-custom-amount'
                inputMode='numeric'
                onChange={(event) => setTyped(event.target.value.trim())}
                placeholder={`${min}–${max}`}
                value={typed}
              />
            </div>
            {error ? (
              <p className='wal-topup__error' id='wal-topup-error' role='alert'>
                {error}
              </p>
            ) : null}
          </div>
        ) : null}
        {bonus > 0 && bonusLabel ? (
          <p className='wal-topup__bonus'>{bonusLabel(bonus)}</p>
        ) : null}

        {methods.length > 1 ? (
          <div
            aria-label={t(`${ns}.method`)}
            className='wal-seg wal-topup__methods'
            role='group'
          >
            {methods.map((item) => (
              <button
                aria-pressed={method === item.type}
                className='wal-seg__item'
                key={item.type}
                onClick={() => onMethodChange(item.type)}
                type='button'
              >
                {item.name || item.type}
              </button>
            ))}
          </div>
        ) : null}

        <div className='wal-topup__foot'>
          {note ? <p className='wal-topup__note'>{note}</p> : <span />}
          <Button
            className='wal-topup__pay'
            disabled={amount === null}
            loading={paying}
            onClick={() => {
              if (amount !== null) onPay(amount);
            }}
            variant='primary'
          >
            {amount === null
              ? t(`${ns}.payEmpty`)
              : t(`${ns}.pay`, { amount: formatUSD(amount) })}
          </Button>
        </div>
      </section>
    </div>
  );
}
