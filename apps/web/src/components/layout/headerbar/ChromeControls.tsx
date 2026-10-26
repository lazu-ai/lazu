import { Check, Globe2 } from 'lucide-react';
import React, { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import ThemeToggle from './ThemeToggle';
import { useSetTheme } from '../../../context/Theme';
import { Button } from '../../ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '../../ui/dropdown-menu';

const LANGUAGE_OPTIONS = [
  { value: 'en', label: 'English', shortLabel: 'EN' },
  { value: 'zh', label: '简体中文', shortLabel: 'ZH' },
  { value: 'zh-TW', label: '繁體中文', shortLabel: '繁' },
  { value: 'ja', label: '日本語', shortLabel: 'JA' },
];

const normalizeLanguage = (language?: string | null) => {
  if (!language) return 'en';
  const lower = String(language).replace(/_/g, '-');
  if (/^zh[-]?(tw|hk|hant|mo)/i.test(lower)) return 'zh-TW';
  return lower.split('-')[0].toLowerCase();
};

type ChromeControlsProps = {
  className?: string;
  compact?: boolean;
};

const ChromeControls = ({
  className = '',
  compact = false,
}: ChromeControlsProps) => {
  const { i18n, t } = useTranslation();
  const rawSetTheme = useSetTheme();
  const setTheme =
    typeof rawSetTheme === 'function' ? rawSetTheme : () => undefined;
  const currentLanguage = normalizeLanguage(i18n.language);
  const activeLanguage = useMemo(
    () =>
      LANGUAGE_OPTIONS.find((option) => option.value === currentLanguage) ||
      LANGUAGE_OPTIONS[0],
    [currentLanguage],
  );

  const handleLanguageChange = (language: string) => {
    localStorage.setItem('i18nextLng', language);
    i18n.changeLanguage(language);
  };

  return (
    <div
      className={[
        'chrome-controls',
        compact ? 'chrome-controls--compact' : '',
        className,
      ]
        .filter(Boolean)
        .join(' ')}
    >
      <ThemeToggle onThemeToggle={setTheme} t={t} />
      <DropdownMenu>
        <DropdownMenuTrigger>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className='headerbar-lang-trigger chrome-controls__language-trigger'
            aria-label={t('common.changeLanguage')}
          >
            <Globe2 size={15} />
            <span>{activeLanguage.shortLabel}</span>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align='end'
          className='chrome-controls__language-menu'
        >
          {LANGUAGE_OPTIONS.map((option) => (
            <DropdownMenuItem
              key={option.value}
              onSelect={() => handleLanguageChange(option.value)}
              className='chrome-controls__language-item'
            >
              <span>{option.label}</span>
              {option.value === activeLanguage.value ? (
                <Check size={14} className='chrome-controls__language-check' />
              ) : null}
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
};

export default ChromeControls;
