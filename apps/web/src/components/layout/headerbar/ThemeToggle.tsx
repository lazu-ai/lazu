import { Sun, Moon } from 'lucide-react';
import { useActualTheme } from '../../../context/Theme';

type ThemeToggleProps = {
  onThemeToggle: (theme: 'light' | 'dark') => void;
  t: (key: string, options?: Record<string, unknown>) => string;
};

/* Two segments, light and dark, in one radio group. The current side carries
   the white sheet; both icons stay in the text colour. */
const ThemeToggle = ({ onThemeToggle, t }: ThemeToggleProps) => {
  const actualTheme = useActualTheme();
  const isDark = actualTheme === 'dark';
  const options = [
    {
      value: 'light' as const,
      icon: Sun,
      label: t('page.console.personal.theme.light', { defaultValue: 'Light' }),
    },
    {
      value: 'dark' as const,
      icon: Moon,
      label: t('page.console.personal.theme.dark', { defaultValue: 'Dark' }),
    },
  ];

  return (
    <div
      className={`theme-pill-toggle ${isDark ? 'is-dark' : ''}`}
      role='radiogroup'
      aria-label={t('page.console.personal.theme.label', {
        defaultValue: 'Theme',
      })}
    >
      <span className='theme-pill-toggle__sheet' aria-hidden='true' />
      {options.map(({ value, icon: Icon, label }) => {
        const checked = value === (isDark ? 'dark' : 'light');
        return (
          <button
            key={value}
            type='button'
            role='radio'
            aria-checked={checked}
            aria-label={label}
            title={label}
            className='theme-pill-toggle__option'
            onClick={() => {
              if (!checked) onThemeToggle(value);
            }}
          >
            <Icon size={15} strokeWidth={1.7} aria-hidden='true' />
          </button>
        );
      })}
    </div>
  );
};

export default ThemeToggle;
