import {
  createContext,
  useCallback,
  useContext,
  type ReactNode,
  useState,
  useEffect,
} from 'react';

export type ThemeMode = 'light' | 'dark' | 'auto';
export type ActualThemeMode = Exclude<ThemeMode, 'auto'>;
export type SetThemeMode = (newTheme: ThemeMode | boolean) => void;

const ThemeContext = createContext<ThemeMode>('auto');
export const useTheme = () => useContext(ThemeContext);

const ActualThemeContext = createContext<ActualThemeMode>('light');
export const useActualTheme = () => useContext(ActualThemeContext);

const SetThemeContext = createContext<SetThemeMode>(() => undefined);
export const useSetTheme = () => useContext(SetThemeContext);

const THEME_STORAGE_KEY = 'lazu.theme';
const LEGACY_THEME_STORAGE_KEY = 'theme-mode';

const normalizeTheme = (value: unknown): ThemeMode => {
  if (value === 'light' || value === 'dark' || value === 'auto') {
    return value;
  }
  return 'auto';
};

// 检测系统主题偏好
const getSystemTheme = (): ActualThemeMode => {
  if (typeof window !== 'undefined' && window.matchMedia) {
    return window.matchMedia('(prefers-color-scheme: dark)').matches
      ? 'dark'
      : 'light';
  }
  return 'light';
};

type ThemeProviderProps = {
  children: ReactNode;
};

export const ThemeProvider = ({ children }: ThemeProviderProps) => {
  const [theme, _setTheme] = useState(() => {
    try {
      return normalizeTheme(
        localStorage.getItem(THEME_STORAGE_KEY) ||
          localStorage.getItem(LEGACY_THEME_STORAGE_KEY),
      );
    } catch {
      return 'auto';
    }
  });

  const [systemTheme, setSystemTheme] = useState(getSystemTheme());

  // 计算实际应用的主题
  const actualTheme = theme === 'auto' ? systemTheme : theme;

  // 监听系统主题变化
  useEffect(() => {
    if (typeof window !== 'undefined' && window.matchMedia) {
      const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');

      const handleSystemThemeChange = (e: MediaQueryListEvent) => {
        setSystemTheme(e.matches ? 'dark' : 'light');
      };

      mediaQuery.addEventListener('change', handleSystemThemeChange);

      return () => {
        mediaQuery.removeEventListener('change', handleSystemThemeChange);
      };
    }
  }, []);

  // 应用主题到DOM
  useEffect(() => {
    const body = document.body;
    body.setAttribute('theme-mode', actualTheme);

    if (actualTheme === 'dark') {
      document.documentElement.classList.add('dark');
    } else {
      document.documentElement.classList.remove('dark');
    }

    let hasCustomLogo = false;
    try {
      const configuredLogo = localStorage.getItem('logo');
      hasCustomLogo = Boolean(configuredLogo && configuredLogo !== '/logo.png');
    } catch {
      // Storage can be unavailable in hardened browser contexts.
    }
    if (!hasCustomLogo) {
      const favicon = document.querySelector<HTMLLinkElement>('#app-favicon');
      if (favicon) {
        favicon.href =
          actualTheme === 'dark'
            ? '/favicon-paper.svg'
            : '/favicon-graphite.svg';
      }
    }
  }, [actualTheme]);

  const setTheme = useCallback<SetThemeMode>((newTheme) => {
    let themeValue: ThemeMode;

    if (typeof newTheme === 'boolean') {
      // 向后兼容原有的 boolean 参数
      themeValue = newTheme ? 'dark' : 'light';
    } else if (typeof newTheme === 'string') {
      // 新的字符串参数支持 'light', 'dark', 'auto'
      themeValue = newTheme;
    } else {
      themeValue = 'auto';
    }

    _setTheme(themeValue);
    localStorage.setItem(THEME_STORAGE_KEY, themeValue);
    localStorage.setItem(LEGACY_THEME_STORAGE_KEY, themeValue);
  }, []);

  return (
    <SetThemeContext.Provider value={setTheme}>
      <ActualThemeContext.Provider value={actualTheme}>
        <ThemeContext.Provider value={theme}>{children}</ThemeContext.Provider>
      </ActualThemeContext.Provider>
    </SetThemeContext.Provider>
  );
};
