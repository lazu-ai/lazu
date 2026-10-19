/* Six boxes that behave like one field.
 *
 * The value lives in the parent as a single string; the boxes are a rendering
 * of it. That is what makes paste, autofill and backspace-across-boxes work
 * without six pieces of state disagreeing with each other.
 */
import { useEffect, useRef } from 'react';
import type { ClipboardEvent, KeyboardEvent, ChangeEvent } from 'react';

type OtpInputProps = {
  disabled?: boolean;
  length: number;
  onChange: (value: string) => void;
  value: string;
};

const digitsOnly = (raw: string) => raw.replace(/\D/g, '');

const OtpInput = ({ disabled, length, onChange, value }: OtpInputProps) => {
  const inputs = useRef<(HTMLInputElement | null)[]>([]);

  // Focus starts on the first empty box, so a code arriving by autofill does
  // not leave the caret parked behind it.
  useEffect(() => {
    const target = Math.min(value.length, length - 1);
    inputs.current[target]?.focus();
  }, [length, value.length]);

  const setAt = (index: number, digit: string) => {
    const next = value.padEnd(length, ' ').split('');
    next[index] = digit || ' ';
    onChange(next.join('').replace(/ /g, '').slice(0, length));
  };

  const handleChange = (
    index: number,
    event: ChangeEvent<HTMLInputElement>,
  ) => {
    const typed = digitsOnly(event.target.value);
    if (!typed) {
      setAt(index, '');
      return;
    }
    // Typing into a full-ish field, or autofilling, can deliver several digits
    // at once; take them from this box rightwards rather than dropping them.
    onChange((value.slice(0, index) + typed).slice(0, length));
  };

  const handleKeyDown = (
    index: number,
    event: KeyboardEvent<HTMLInputElement>,
  ) => {
    if (event.key === 'Backspace' && !value[index] && index > 0) {
      event.preventDefault();
      onChange(value.slice(0, index - 1));
      inputs.current[index - 1]?.focus();
      return;
    }
    if (event.key === 'ArrowLeft' && index > 0) {
      event.preventDefault();
      inputs.current[index - 1]?.focus();
    }
    if (event.key === 'ArrowRight' && index < length - 1) {
      event.preventDefault();
      inputs.current[index + 1]?.focus();
    }
  };

  const handlePaste = (event: ClipboardEvent<HTMLInputElement>) => {
    const pasted = digitsOnly(event.clipboardData.getData('text'));
    if (!pasted) return;
    event.preventDefault();
    onChange(pasted.slice(0, length));
  };

  return (
    <div className='auth-otp'>
      {Array.from({ length }, (_, index) => (
        <input
          key={index}
          ref={(element) => {
            inputs.current[index] = element;
          }}
          aria-label={`${index + 1}`}
          autoComplete={index === 0 ? 'one-time-code' : 'off'}
          className={`auth-otp__box${value[index] ? ' auth-otp__box--filled' : ''}`}
          disabled={disabled}
          inputMode='numeric'
          onChange={(event) => handleChange(index, event)}
          onKeyDown={(event) => handleKeyDown(index, event)}
          onPaste={handlePaste}
          type='text'
          value={value[index] ?? ''}
        />
      ))}
    </div>
  );
};

export default OtpInput;
