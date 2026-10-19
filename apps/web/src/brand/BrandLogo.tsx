import iconGraphiteSource from './assets/lazu-icon-graphite.svg?raw';
import iconPaperSource from './assets/lazu-icon-paper.svg?raw';
import primaryGraphiteSource from './assets/lazu-primary-graphite.svg?raw';
import primaryPaperSource from './assets/lazu-primary-paper.svg?raw';
import stackedGraphiteSource from './assets/lazu-stacked-graphite.svg?raw';
import stackedPaperSource from './assets/lazu-stacked-paper.svg?raw';
import wordmarkGraphiteSource from './assets/lazu-wordmark-graphite.svg?raw';
import wordmarkPaperSource from './assets/lazu-wordmark-paper.svg?raw';
import type { ComponentPropsWithoutRef, ElementType } from 'react';

const svgDataURL = (source: string) =>
  `data:image/svg+xml,${encodeURIComponent(source)}`;

const LOGO_ASSETS = {
  primary: {
    graphite: svgDataURL(primaryGraphiteSource),
    paper: svgDataURL(primaryPaperSource),
  },
  icon: {
    graphite: svgDataURL(iconGraphiteSource),
    paper: svgDataURL(iconPaperSource),
  },
  wordmark: {
    graphite: svgDataURL(wordmarkGraphiteSource),
    paper: svgDataURL(wordmarkPaperSource),
  },
  stacked: {
    graphite: svgDataURL(stackedGraphiteSource),
    paper: svgDataURL(stackedPaperSource),
  },
};

const SIZE_CLASS = {
  header: 'brand-logo--header',
  sidebar: 'brand-logo--sidebar',
  compact: 'brand-logo--compact',
  hero: 'brand-logo--hero',
  footer: 'brand-logo--footer',
};

type LogoVariant = keyof typeof LOGO_ASSETS;
type LogoTone = keyof (typeof LOGO_ASSETS)['primary'];
type LogoSize = keyof typeof SIZE_CLASS;

type BrandLogoProps<T extends ElementType = 'img'> = {
  variant?: LogoVariant;
  tone?: LogoTone;
  size?: LogoSize;
  className?: string;
  alt?: string;
  as?: T;
} & Omit<ComponentPropsWithoutRef<T>, 'as' | 'className' | 'alt' | 'src'>;

const getLogoAsset = (variant: LogoVariant, tone: LogoTone) => {
  return LOGO_ASSETS[variant]?.[tone] || LOGO_ASSETS.primary.graphite;
};

/**
 * BrandLogo renders only sanctioned Seal logo combinations.
 *
 * Usage:
 * <BrandLogo variant="primary" tone="graphite" size="header" />
 */
const BrandLogo = ({
  variant = 'primary',
  tone = 'graphite',
  size = 'header',
  className = '',
  alt = 'Lazu',
  as: Component = 'img',
  ...props
}: BrandLogoProps) => {
  const logoSrc = getLogoAsset(variant, tone);
  const sizeClass = SIZE_CLASS[size] || SIZE_CLASS.header;

  return (
    <Component
      src={logoSrc}
      alt={alt}
      className={`brand-logo ${sizeClass} ${className}`.trim()}
      {...props}
    />
  );
};

export default BrandLogo;
