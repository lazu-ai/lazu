/* Auto-reload · one row under the wallet's top-up bar.
 *
 * Off by default and one line tall (A): a switch, the state, one sentence.
 * Turning the switch on opens the three numbers and the authorization line;
 * the card itself is saved on a Stripe page that charges nothing. On (B):
 * the three numbers, the card with its change link, a summary of the last
 * reload and this month's total, and one line of rules. A declined card
 * pauses reloads and the wallet shows the notice (C) above everything.
 *
 * The component states nothing it does not know: while the setting is still
 * loading it holds the row's height and draws no "off".
 */
import { CreditCard } from 'lucide-react';
import { useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from './button';
import { InlineNotice } from './inline-notice';
import { PendingBlock } from './pending';
import { Switch } from './switch';
import { cn } from '../../lib/cn';
import type { ReactNode } from 'react';

export type AutoReloadPauseReason = '' | 'card_declined' | 'cap_reached';

export type AutoReloadSettings = {
  thresholdUSD: number;
  amountUSD: number;
  monthlyCapUSD: number;
};

export type AutoReloadState = AutoReloadSettings & {
  /* False until the owner has ever touched auto-reload. */
  configured: boolean;
  enabled: boolean;
  /* "Visa ···· 4242", or null while no card is on file. */
  cardLabel: string | null;
  /* A Stripe card-save page was opened and has not reported back. */
  setupPending: boolean;
  pausedReason: AutoReloadPauseReason;
  lastReload: { amountUSD: number; completedAt: number } | null;
  monthSpentUSD: number;
  minAmountUSD: number;
  maxAmountUSD: number;
};

type AutoReloadCardProps = {
  /* True while the setting is still loading: hold the space, state nothing. */
  pending: boolean;
  /* Null once loading ended without an answer: the row draws nothing. */
  state: AutoReloadState | null;
  busy?: boolean;
  /* Turn on without a card on file, or change the card: opens Stripe. */
  onSaveCard: (settings: AutoReloadSettings) => void;
  /* Turn on with the card already on file. */
  onTurnOn: (settings: AutoReloadSettings) => void;
  /* Save changed numbers while on. */
  onSave: (settings: AutoReloadSettings) => void;
  onTurnOff: () => void;
  /* Opens the three numbers at once (the wallet does this when it lands from
     the notice's "change card" button). */
  openEditor?: boolean;
  className?: string;
};

const whole = (value: number) => `$${value.toLocaleString('en-US')}`;
const usd = (value: number) =>
  `$${value.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })}`;
const monthDay = (unix: number) => {
  const date = new Date(unix * 1000);
  return `${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
};

/* The closed row: 14px padding, two lines of 20px and 18px. */
const ROW_HEIGHT = 72;

type Draft = { threshold: string; amount: string; cap: string };

const draftOf = (state: AutoReloadState | null): Draft => ({
  threshold: state?.configured ? String(state.thresholdUSD) : '10',
  amount: state?.configured ? String(state.amountUSD) : '50',
  cap: state?.configured ? String(state.monthlyCapUSD) : '300',
});

export function AutoReloadCard({
  pending,
  state,
  busy = false,
  onSaveCard,
  onTurnOn,
  onSave,
  onTurnOff,
  openEditor = false,
  className,
}: AutoReloadCardProps) {
  const { t } = useTranslation();
  const ns = 'page.console.wallet.autoReload';
  const id = useId();
  const [editing, setEditing] = useState(openEditor);
  const [draft, setDraft] = useState<Draft>(() => draftOf(state));
  /* A new answer from the server resets the numbers the reader has not
     saved: the saved ones are the fact. */
  const stateKey = state
    ? `${state.configured}:${state.enabled}:${state.thresholdUSD}:${state.amountUSD}:${state.monthlyCapUSD}:${state.cardLabel ?? ''}`
    : 'none';
  useEffect(() => {
    setDraft(draftOf(state));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [stateKey]);
  useEffect(() => {
    if (openEditor) setEditing(true);
  }, [openEditor]);

  if (pending) {
    return (
      <PendingBlock
        className={cn('lazu-auto-reload lazu-auto-reload--pending', className)}
        height={ROW_HEIGHT}
      />
    );
  }
  /* No answer (the query failed): say nothing rather than "off". */
  if (!state) return null;

  const min = state.minAmountUSD;
  const max = state.maxAmountUSD;
  const parse = (value: string) =>
    /^\d+$/.test(value) ? Number(value) : Number.NaN;
  const threshold = parse(draft.threshold);
  const amount = parse(draft.amount);
  const cap = parse(draft.cap);
  const error = [threshold, amount, cap].some(
    (value) => !Number.isSafeInteger(value),
  )
    ? t(`${ns}.errorWhole`)
    : threshold < 1
      ? t(`${ns}.errorThreshold`)
      : amount < min
        ? t(`${ns}.errorMin`, { min: whole(min) })
        : amount > max
          ? t(`${ns}.errorMax`, { max: whole(max) })
          : cap < amount
            ? t(`${ns}.errorCap`)
            : '';
  const settings: AutoReloadSettings | null = error
    ? null
    : { thresholdUSD: threshold, amountUSD: amount, monthlyCapUSD: cap };
  const dirty =
    !state.configured ||
    threshold !== state.thresholdUSD ||
    amount !== state.amountUSD ||
    cap !== state.monthlyCapUSD;

  const on = state.enabled;
  const stateLabel = on
    ? state.pausedReason
      ? t(`${ns}.statePaused`)
      : t(`${ns}.stateOn`)
    : state.setupPending
      ? t(`${ns}.statePending`)
      : t(`${ns}.stateOff`);

  const fields: Array<{
    key: keyof Draft;
    label: string;
    hint?: string;
  }> = [
    { key: 'threshold', label: t(`${ns}.threshold`) },
    {
      key: 'amount',
      label: t(`${ns}.amount`),
      hint: t(`${ns}.amountHint`, { min: whole(min) }),
    },
    { key: 'cap', label: t(`${ns}.cap`) },
  ];

  const numbers = (
    <div className='lazu-auto-reload__grid'>
      {fields.map((field) => (
        <div className='lazu-auto-reload__cell' key={field.key}>
          <label htmlFor={`${id}-${field.key}`}>{field.label}</label>
          <span
            className='lazu-auto-reload__field'
            data-invalid={
              error && !Number.isSafeInteger(parse(draft[field.key]))
                ? ''
                : undefined
            }
          >
            <span aria-hidden='true'>$</span>
            <input
              aria-describedby={error ? `${id}-error` : undefined}
              autoComplete='off'
              disabled={busy}
              id={`${id}-${field.key}`}
              inputMode='numeric'
              onChange={(event) =>
                setDraft((prev) => ({
                  ...prev,
                  [field.key]: event.target.value.trim(),
                }))
              }
              value={draft[field.key]}
            />
          </span>
        </div>
      ))}
      <div className='lazu-auto-reload__cell'>
        <span className='lazu-auto-reload__cell-label'>{t(`${ns}.card`)}</span>
        <span className='lazu-auto-reload__card'>
          <CreditCard aria-hidden='true' size={14} />
          <span className='lazu-auto-reload__card-label'>
            {state.cardLabel ?? t(`${ns}.noCard`)}
          </span>
          {state.cardLabel ? (
            <button
              className='lazu-auto-reload__link'
              disabled={busy}
              onClick={() => {
                if (settings) onSaveCard(settings);
              }}
              type='button'
            >
              {t(`${ns}.changeCard`)}
            </button>
          ) : null}
        </span>
      </div>
    </div>
  );

  const errorLine = error ? (
    <p className='lazu-auto-reload__error' id={`${id}-error`} role='alert'>
      {error}
    </p>
  ) : null;

  let summary: ReactNode = null;
  if (on) {
    summary = (
      <span className='lazu-auto-reload__summary'>
        {state.lastReload
          ? t(`${ns}.summaryLast`, {
              date: monthDay(state.lastReload.completedAt),
              amount: usd(state.lastReload.amountUSD),
            })
          : t(`${ns}.summaryNone`)}
        {' · '}
        {t(`${ns}.summaryMonth`, {
          spent: whole(state.monthSpentUSD),
          cap: whole(state.monthlyCapUSD),
        })}
      </span>
    );
  }

  return (
    <section
      className={cn('lazu-auto-reload', className)}
      data-state={on ? (state.pausedReason ? 'paused' : 'on') : 'off'}
    >
      <div className='lazu-auto-reload__row'>
        <Switch
          aria-label={t(`${ns}.title`)}
          checked={on}
          disabled={busy}
          onCheckedChange={(checked) => {
            if (checked) {
              setEditing(true);
              return;
            }
            setEditing(false);
            onTurnOff();
          }}
        />
        <div className='lazu-auto-reload__copy'>
          <strong>
            {t(`${ns}.title`)} · {stateLabel}
          </strong>
          {!on && !editing ? (
            <span>
              {state.setupPending ? t(`${ns}.pendingSub`) : t(`${ns}.offSub`)}
            </span>
          ) : null}
          {on && state.pausedReason === 'cap_reached' ? (
            <span>
              {t(`${ns}.pausedCap`, { cap: whole(state.monthlyCapUSD) })}
            </span>
          ) : null}
          {on && state.pausedReason === 'card_declined' ? (
            <span>{t(`${ns}.pausedCard`)}</span>
          ) : null}
        </div>
        {summary}
      </div>

      {on ? (
        <>
          {numbers}
          {errorLine}
          <div className='lazu-auto-reload__foot'>
            <p className='lazu-auto-reload__note'>
              {t(`${ns}.note`, { min: whole(min) })}
            </p>
            {dirty ? (
              <span className='lazu-auto-reload__actions'>
                <Button
                  disabled={busy}
                  onClick={() => setDraft(draftOf(state))}
                  size='sm'
                  variant='ghost'
                >
                  {t(`${ns}.cancel`)}
                </Button>
                <Button
                  disabled={busy || !settings}
                  loading={busy}
                  onClick={() => {
                    if (settings) onSave(settings);
                  }}
                  size='sm'
                  variant='primary'
                >
                  {t(`${ns}.save`)}
                </Button>
              </span>
            ) : null}
          </div>
        </>
      ) : editing ? (
        <>
          {numbers}
          {errorLine}
          <div className='lazu-auto-reload__foot'>
            <p className='lazu-auto-reload__note'>
              {t(`${ns}.consent`)}{' '}
              {state.cardLabel ? null : t(`${ns}.saveCardHint`)}
            </p>
            <span className='lazu-auto-reload__actions'>
              <Button
                disabled={busy}
                onClick={() => {
                  setEditing(false);
                  setDraft(draftOf(state));
                }}
                size='sm'
                variant='ghost'
              >
                {t(`${ns}.cancel`)}
              </Button>
              <Button
                disabled={busy || !settings}
                loading={busy}
                onClick={() => {
                  if (!settings) return;
                  if (state.cardLabel) onTurnOn(settings);
                  else onSaveCard(settings);
                }}
                size='sm'
                variant='primary'
              >
                {state.cardLabel
                  ? t(`${ns}.agreeTurnOn`)
                  : t(`${ns}.agreeSaveCard`)}
              </Button>
            </span>
          </div>
        </>
      ) : null}
    </section>
  );
}

type AutoReloadFailureNoticeProps = {
  cardLabel: string | null;
  failedAt: number;
  thresholdUSD: number;
  amountUSD: number;
  /* The wallet balance when known; the sentence leaves it out otherwise. */
  balanceUSD: number | null;
  busy?: boolean;
  onChangeCard: () => void;
  onTopUp: (amountUSD: number) => void;
  className?: string;
};

/* C · the wallet-top notice after a declined reload: paper ground, ink edge,
   the two things the owner can do. */
export function AutoReloadFailureNotice({
  cardLabel,
  failedAt,
  thresholdUSD,
  amountUSD,
  balanceUSD,
  busy = false,
  onChangeCard,
  onTopUp,
  className,
}: AutoReloadFailureNoticeProps) {
  const { t } = useTranslation();
  const ns = 'page.console.wallet.autoReload';
  const when = failedAt
    ? new Date(failedAt * 1000).toLocaleString(undefined, {
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
      })
    : '';
  const card = cardLabel ?? t(`${ns}.savedCard`);
  const description =
    balanceUSD === null
      ? t(`${ns}.failBodyShort`, { card, date: when })
      : t(`${ns}.failBody`, {
          card,
          date: when,
          balance: usd(balanceUSD),
          threshold: whole(thresholdUSD),
        });
  return (
    <InlineNotice
      actions={
        <>
          <Button
            disabled={busy}
            onClick={onChangeCard}
            size='sm'
            variant='secondary'
          >
            {t(`${ns}.failChangeCard`)}
          </Button>
          <Button
            disabled={busy}
            onClick={() => onTopUp(amountUSD)}
            size='sm'
            variant='primary'
          >
            {t(`${ns}.failTopUp`, { amount: whole(amountUSD) })}
          </Button>
        </>
      }
      className={cn('lazu-auto-reload-notice', className)}
      description={description}
      title={t(`${ns}.failTitle`)}
      tone='danger'
    />
  );
}
