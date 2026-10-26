import { Eye, EyeOff } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import './auth.css';
import { AuthExits } from './AuthExits';
import BrandLogo from '../../brand/BrandLogo';
import { useActualTheme } from '../../context/Theme';
import ChromeControls from '../layout/headerbar/ChromeControls';
import { Field, FieldMessage, Label } from '../ui/field';
import { Input } from '../ui/input';
import type { LucideIcon } from 'lucide-react';
import type { ChangeEvent, ReactNode } from 'react';
import type { UseFormRegisterReturn } from 'react-hook-form';

type AuthShellProps = {
  children: ReactNode;
};

/* How loudly the panel says what screen this is.
 *
 * `page` is the 29px heading every other auth route uses. `step` is the
 * smaller heading of a screen you arrived at from somewhere else — the code
 * screen already knows you pressed something to get there, so it does not
 * need to reintroduce the site. `hidden` keeps the heading for screen readers
 * only: on the door the wordmark names the site and the segmented control
 * names both ways in, so a 29px "Sign in to Lazu" above them is a caption on
 * a picture that already speaks. */
type AuthHeading = 'page' | 'step' | 'hidden';

type AuthPanelProps = {
  children: ReactNode;
  description?: ReactNode;
  footer?: ReactNode;
  heading?: AuthHeading;
  /** Changing this animates the panel body from its old height to its new one. */
  morphToken?: string;
  title: ReactNode;
};

type AuthTextFieldProps = {
  /** Tertiary action rendered on the label row, e.g. "forgot password". */
  aside?: ReactNode;
  autoComplete?: string;
  disabled?: boolean;
  error?: ReactNode;
  icon?: LucideIcon;
  id: string;
  label: ReactNode;
  name?: string;
  /** Controlled callers pass onChange/value; form-driven callers pass register. */
  onChange?: (value: string) => void;
  placeholder?: string;
  readOnly?: boolean;
  register?: UseFormRegisterReturn;
  type?: string;
  value?: string;
};

type AuthDividerProps = {
  children: ReactNode;
};

type AuthSegmentedOption = {
  label: ReactNode;
  value: string;
};

type AuthSegmentedProps = {
  ariaLabel: string;
  onChange: (value: string) => void;
  options: AuthSegmentedOption[];
  value: string;
};

const prefersReducedMotion = () =>
  typeof window !== 'undefined' &&
  window.matchMedia('(prefers-reduced-motion: reduce)').matches;

/* Auth shell: the flat page ground, the chrome controls, and whatever the
 * route puts in the middle. The split layout with a provider orbit is gone —
 * the ring carried no information and was identical on sign-in and sign-up —
 * and so is the card that replaced it, which read as a pop-up dialog floating
 * over an animated ground. The wordmark now lives inside the column so the
 * loading skeleton can occupy the same place without anything moving. */
export function AuthShell({ children }: AuthShellProps) {
  return (
    <div className='auth-page-shell'>
      <div className='auth-page-inner'>
        <div className='auth-page-topbar'>
          <ChromeControls className='auth-page-controls' />
        </div>
        <div className='auth-page-card-wrap'>{children}</div>
      </div>
    </div>
  );
}

/* The mark that anchors the column. Kept separate from AuthPanel so the route
 * skeleton can render it in exactly the same spot. */
export function AuthMark() {
  const actualTheme = useActualTheme();
  return (
    <div className='auth-column__mark'>
      <Link to='/' aria-label='Lazu'>
        {/* The horizontal lockup, not the bare tile: with the door's heading
            gone this is the only thing on the screen that names the site. */}
        <BrandLogo
          variant='primary'
          tone={actualTheme === 'dark' ? 'paper' : 'graphite'}
          size='hero'
          alt='Lazu'
        />
      </Link>
    </div>
  );
}

/* Animates its own height between two contents.
 *
 * The old height has to be read while the DOM still holds the old content, so
 * it is measured in the render phase — this is the "First" read of a FLIP, and
 * the effect below plays it. Without it the column snaps between step heights,
 * which is what made the previous email→code transition read as a flash. */
function AuthMorph({
  children,
  token,
}: {
  children: ReactNode;
  token: string;
}) {
  const boxRef = useRef<HTMLDivElement>(null);
  const innerRef = useRef<HTMLDivElement>(null);
  const lastToken = useRef(token);
  const fromHeight = useRef<number | null>(null);

  if (lastToken.current !== token && boxRef.current) {
    fromHeight.current = boxRef.current.getBoundingClientRect().height;
  }

  useLayoutEffect(() => {
    if (lastToken.current === token) return;
    lastToken.current = token;

    const box = boxRef.current;
    const inner = innerRef.current;
    const from = fromHeight.current;
    fromHeight.current = null;
    if (!box || !inner || from == null || prefersReducedMotion()) return;

    const to = inner.getBoundingClientRect().height;
    if (Math.abs(to - from) < 1) return;

    // Animated rather than transitioned, so the element's own height is never
    // pinned to an inline value: a CSS transition here leaves the box stuck at
    // the old height whenever its transitionend does not arrive, which clips
    // the taller step away behind `overflow: hidden`.
    const play = box.animate([{ height: `${from}px` }, { height: `${to}px` }], {
      duration: 240,
      easing: 'cubic-bezier(0.2, 0.8, 0.2, 1)',
    });
    // Clip only while the height is moving; a permanently hidden overflow
    // would cut the focus ring off a full-width field.
    box.style.overflow = 'hidden';
    const unclip = () => {
      box.style.overflow = '';
    };
    play.addEventListener('finish', unclip);
    play.addEventListener('cancel', unclip);
    return () => {
      play.cancel();
    };
  }, [token]);

  return (
    <div className='auth-morph' ref={boxRef}>
      <div className='auth-morph__inner' key={token} ref={innerRef}>
        {children}
      </div>
    </div>
  );
}

/* Grows or collapses a field in place.
 *
 * Switching the door between a code and a password used to swap the whole
 * panel — AuthMorph keyed its content by credential, so the provider buttons,
 * the segmented switch you had just pressed, the address you had typed and
 * the consent notice all unmounted, remounted at opacity 0 and faded back in.
 * That is the blink. Now only the thing that actually changes moves: the
 * password field opens from a zero-height grid row and closes the same way,
 * and everything around it is pushed rather than repainted. */
const REVEAL_MS = 240;

export function AuthReveal({
  children,
  open,
}: {
  children: ReactNode;
  open: boolean;
}) {
  const [present, setPresent] = useState(open);
  const [shown, setShown] = useState(open);
  // Overflow is clipped only while the row is moving; a permanently hidden
  // overflow would cut the focus ring off the field inside.
  const [settled, setSettled] = useState(open);

  useEffect(() => {
    const instant = prefersReducedMotion();
    if (open) {
      setPresent(true);
      if (instant) {
        setShown(true);
        setSettled(true);
        return undefined;
      }
      // One painted frame at 0fr, so the transition has somewhere to start.
      const frame = requestAnimationFrame(() => setShown(true));
      const settle = setTimeout(() => setSettled(true), REVEAL_MS + 20);
      return () => {
        cancelAnimationFrame(frame);
        clearTimeout(settle);
      };
    }
    setShown(false);
    setSettled(false);
    if (instant) {
      setPresent(false);
      return undefined;
    }
    const unmount = setTimeout(() => setPresent(false), REVEAL_MS + 20);
    return () => clearTimeout(unmount);
  }, [open]);

  if (!present) return null;
  return (
    <div
      className='auth-reveal'
      data-settled={settled || undefined}
      data-state={shown ? 'open' : 'closed'}
    >
      <div className='auth-reveal__inner'>{children}</div>
    </div>
  );
}

export function AuthPanel({
  children,
  description,
  footer,
  heading = 'page',
  morphToken = 'static',
  title,
}: AuthPanelProps) {
  const hidden = heading === 'hidden';
  return (
    <section className='auth-column'>
      <AuthMark />
      <AuthMorph token={morphToken}>
        <div className='auth-body'>
          {hidden ? (
            <h1 className='sr-only'>{title}</h1>
          ) : (
            <div className={`auth-heading auth-heading--${heading}`}>
              <h1 className='auth-title'>{title}</h1>
              {description ? (
                <p className='auth-description'>{description}</p>
              ) : null}
            </div>
          )}
          {children}
          {footer ? <div className='auth-footer'>{footer}</div> : null}
        </div>
      </AuthMorph>
      <AuthExits />
    </section>
  );
}

export function AuthTextField({
  aside,
  autoComplete,
  disabled,
  error,
  icon: Icon,
  id,
  label,
  name,
  onChange,
  placeholder,
  readOnly,
  register,
  type = 'text',
  value,
}: AuthTextFieldProps) {
  const isPassword = type === 'password';
  const [revealed, setRevealed] = useState(false);
  /* A revealed field that is emptied has to go back to hidden. Switching the
     door to the code and back inside the 240ms collapse cancels the unmount,
     so this component survives with `revealed` still true — and the next
     password would be typed in the clear without anyone asking for that. */
  useEffect(() => {
    if (!value) setRevealed(false);
  }, [value]);
  const effectiveType = isPassword && revealed ? 'text' : type;
  const inputClassNames = [
    Icon ? 'auth-field-input--with-icon' : null,
    isPassword ? 'auth-field-input--with-trailing' : null,
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <Field>
      <div className='auth-field-labelrow'>
        <Label htmlFor={id}>{label}</Label>
        {aside}
      </div>
      <div className='auth-field-control'>
        {Icon ? (
          <Icon aria-hidden='true' className='auth-field-icon' size={15} />
        ) : null}
        <Input
          id={id}
          name={name}
          aria-describedby={error ? `${id}-error` : undefined}
          autoComplete={autoComplete}
          className={inputClassNames || undefined}
          disabled={disabled}
          error={Boolean(error)}
          onChange={
            onChange
              ? (event: ChangeEvent<HTMLInputElement>) =>
                  onChange(event.target.value)
              : undefined
          }
          placeholder={placeholder}
          readOnly={readOnly}
          type={effectiveType}
          value={value}
          {...register}
        />
        {isPassword ? (
          <button
            type='button'
            className='auth-field-trailing-button'
            aria-label={revealed ? 'Hide password' : 'Show password'}
            aria-pressed={revealed}
            onClick={() => setRevealed((prev) => !prev)}
            tabIndex={-1}
          >
            {revealed ? <EyeOff size={16} /> : <Eye size={16} />}
          </button>
        ) : null}
      </div>
      <FieldMessage error id={`${id}-error`}>
        {error}
      </FieldMessage>
    </Field>
  );
}

export function AuthDivider({ children }: AuthDividerProps) {
  return (
    <div className='auth-divider' role='separator'>
      <span aria-hidden='true' />
      <span>{children}</span>
      <span aria-hidden='true' />
    </div>
  );
}

/* Which credential the visitor uses is a route between two ways in, not the
 * action that gets them in — so the selected half takes the neutral selected
 * surface rather than the accent, and never outranks the primary button. */
export function AuthSegmented({
  ariaLabel,
  onChange,
  options,
  value,
}: AuthSegmentedProps) {
  return (
    <div className='auth-seg' role='tablist' aria-label={ariaLabel}>
      {options.map((option) => (
        <button
          key={option.value}
          aria-selected={option.value === value}
          className='auth-seg__option'
          // The selected half paints a surface, so it opts out of the global
          // native-button reset in index.css.
          data-owns-chrome
          onClick={() => onChange(option.value)}
          role='tab'
          type='button'
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}
