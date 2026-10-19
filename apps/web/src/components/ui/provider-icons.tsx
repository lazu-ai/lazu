import { Building2 } from 'lucide-react';
import {
  providerDescriptorName,
  resolveProviderBrandDescriptor,
  type ProviderBrand,
} from './provider-brands';
import type { CSSProperties, ReactNode } from 'react';

type ProviderIconDefinition = {
  label: string;
  color: string;
  background: string;
};

const PROVIDER_ICONS: Record<string, ProviderIconDefinition> = {
  ai360: { label: '360', color: '#0f766e', background: '#ccfbf1' },
  claude: { label: 'CL', color: '#8a3d1f', background: '#ffedd5' },
  cloudflare: { label: 'CF', color: '#c2410c', background: '#ffedd5' },
  cohere: { label: 'CO', color: '#2563eb', background: '#dbeafe' },
  coze: { label: 'CZ', color: '#7c3aed', background: '#ede9fe' },
  deepseek: { label: 'DS', color: '#1d4ed8', background: '#dbeafe' },
  dify: { label: 'DF', color: '#6d28d9', background: '#ede9fe' },
  doubao: { label: 'DB', color: '#be123c', background: '#ffe4e6' },
  fastgpt: { label: 'FG', color: '#047857', background: '#d1fae5' },
  gemini: { label: 'GE', color: '#4338ca', background: '#e0e7ff' },
  hunyuan: { label: 'HY', color: '#0369a1', background: '#e0f2fe' },
  jina: { label: 'JI', color: '#b45309', background: '#fef3c7' },
  jimeng: { label: 'JM', color: '#be185d', background: '#fce7f3' },
  kling: { label: 'KL', color: '#4d7c0f', background: '#ecfccb' },
  layers: { label: 'LY', color: '#475569', background: '#f1f5f9' },
  minimax: { label: 'MM', color: '#a21caf', background: '#fae8ff' },
  mistral: { label: 'MI', color: '#b45309', background: '#fef3c7' },
  moonshot: { label: 'MO', color: '#334155', background: '#e2e8f0' },
  ollama: { label: 'OL', color: '#18181b', background: '#f4f4f5' },
  openai: { label: 'AI', color: '#047857', background: '#d1fae5' },
  openrouter: { label: 'OR', color: '#2563eb', background: '#dbeafe' },
  perplexity: { label: 'PX', color: '#0f766e', background: '#ccfbf1' },
  qwen: { label: 'QW', color: '#1d4ed8', background: '#dbeafe' },
  siliconcloud: { label: 'SC', color: '#7c2d12', background: '#ffedd5' },
  spark: { label: 'SP', color: '#c026d3', background: '#fae8ff' },
  wenxin: { label: 'WX', color: '#1e40af', background: '#dbeafe' },
  xai: { label: 'xA', color: '#18181b', background: '#f4f4f5' },
  xinference: { label: 'XI', color: '#4c1d95', background: '#ede9fe' },
  yi: { label: 'YI', color: '#047857', background: '#d1fae5' },
  zhipu: { label: 'ZP', color: '#7e22ce', background: '#f3e8ff' },
};

const SIZE_ALIASES: Record<string, number> = {
  large: 32,
  small: 18,
  'extra-extra-small': 14,
};

const parseIconPropValue = (raw: string | null | undefined): unknown => {
  if (raw == null) return true;
  let value = String(raw).trim();

  if (value.startsWith('{') && value.endsWith('}')) {
    value = value.slice(1, -1).trim();
  }

  if (
    (value.startsWith('"') && value.endsWith('"')) ||
    (value.startsWith("'") && value.endsWith("'"))
  ) {
    return value.slice(1, -1);
  }

  if (value === 'true') return true;
  if (value === 'false') return false;
  if (/^-?\d+(?:\.\d+)?$/.test(value)) return Number(value);

  return value;
};

const normalizeIconKey = (raw: string): string =>
  raw.replace(/[^a-zA-Z0-9]/g, '').toLowerCase();

const getInitials = (raw: string): string => {
  const words = raw
    .replace(/([a-z])([A-Z])/g, '$1 $2')
    .split(/[^a-zA-Z0-9]+/)
    .filter(Boolean);

  if (words.length === 0) return '?';
  if (words.length === 1) return words[0].slice(0, 2).toUpperCase();
  return words
    .slice(0, 2)
    .map((word) => word[0])
    .join('')
    .toUpperCase();
};

const resolveSize = (size: number | string | unknown, fallback: number) => {
  if (typeof size === 'number') return size;
  if (typeof size === 'string') {
    const trimmed = size.trim();
    if (trimmed in SIZE_ALIASES) return SIZE_ALIASES[trimmed];
    const numeric = Number(trimmed);
    if (Number.isFinite(numeric)) return numeric;
  }
  return fallback;
};

export function getProviderIcon(iconName: unknown, size = 14): ReactNode {
  const normalized = typeof iconName === 'string' ? iconName.trim() : iconName;

  if (!normalized) {
    return <ProviderAvatar label='?' size={size} title='Unknown provider' />;
  }

  const iconDescriptor = String(normalized);
  const segments = iconDescriptor.split('.');
  const baseKey = providerDescriptorName(iconDescriptor) || iconDescriptor;
  const brand = resolveProviderBrandDescriptor(baseKey);
  const definition = PROVIDER_ICONS[normalizeIconKey(baseKey)] ?? {
    label: getInitials(baseKey),
    color: '#475569',
    background: '#f1f5f9',
  };

  const props: Record<string, unknown> = {};

  const propertyIndex = segments.findIndex((segment) => segment.includes('='));
  for (const segment of propertyIndex === -1
    ? []
    : segments.slice(propertyIndex)) {
    const equalsIndex = segment.indexOf('=');
    if (equalsIndex === -1) continue;
    const key = segment.slice(0, equalsIndex).trim();
    const value = segment.slice(equalsIndex + 1).trim();
    props[key] = parseIconPropValue(value);
  }

  const resolvedSize = resolveSize(props.size, size);
  if (brand.logoSrc) {
    return <ProviderLogo brand={brand} size={resolvedSize} />;
  }

  return (
    <ProviderAvatar
      label={definition.label}
      color={definition.color}
      background={definition.background}
      size={resolvedSize}
      title={baseKey}
    />
  );
}

export function ProviderLogo({
  brand,
  className,
  decorative = false,
  size = 24,
}: {
  brand: ProviderBrand;
  className?: string;
  decorative?: boolean;
  size?: number;
}) {
  const frameStyle: CSSProperties = {
    alignItems: 'center',
    background: 'var(--lazu-surface)',
    border: '1px solid var(--lazu-border)',
    borderRadius: 'var(--lazu-radius-badge)',
    color: brand.monochrome
      ? 'var(--lazu-evidence-strong)'
      : 'var(--lazu-text-muted)',
    display: 'inline-flex',
    flex: '0 0 auto',
    height: size,
    justifyContent: 'center',
    overflow: 'hidden',
    width: size,
  };

  return (
    <span
      aria-hidden={decorative || undefined}
      aria-label={decorative ? undefined : brand.companyName}
      className={className}
      data-provider-logo={brand.id}
      role={decorative ? undefined : 'img'}
      style={frameStyle}
      title={decorative ? undefined : brand.companyName}
    >
      {brand.logoSrc ? (
        brand.logoSvg ? (
          <span
            aria-hidden='true'
            // The string is a source-controlled local asset. Keeping the SVG
            // inline lets its currentColor paths inherit the theme without the
            // broken URL-mask fallback that rendered as a solid square.
            dangerouslySetInnerHTML={{ __html: brand.logoSvg }}
            style={{
              display: 'inline-flex',
              fontSize: size * 0.72,
              lineHeight: 1,
            }}
          />
        ) : (
          <img
            alt=''
            aria-hidden='true'
            src={brand.logoSrc}
            style={{ height: '72%', objectFit: 'contain', width: '72%' }}
          />
        )
      ) : (
        <Building2 aria-hidden='true' size={Math.max(12, size * 0.56)} />
      )}
    </span>
  );
}

function ProviderAvatar({
  background = '#f1f5f9',
  color = '#475569',
  label,
  size,
  title,
}: {
  background?: string;
  color?: string;
  label: string;
  size: number;
  title: string;
}) {
  const style: CSSProperties = {
    alignItems: 'center',
    background,
    border: '1px solid color-mix(in srgb, currentColor 18%, transparent)',
    borderRadius: '999px',
    color,
    display: 'inline-flex',
    flex: '0 0 auto',
    fontSize: Math.max(8, Math.round(size * 0.38)),
    fontWeight: 700,
    height: size,
    justifyContent: 'center',
    lineHeight: 1,
    width: size,
  };

  return (
    <span aria-label={title} role='img' style={style} title={title}>
      {label}
    </span>
  );
}
