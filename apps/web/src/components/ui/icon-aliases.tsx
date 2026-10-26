// Compatibility re-exports of lucide-react icons under the Semi-style
// `IconFoo` names that the pre-rebuild console code referenced. This
// file is NOT a Semi UI leftover — the actual Semi dependency was
// removed during the frontend rebuild (PR #1). The shim exists so the
// rebuild PR could swap Semi for lucide without touching every
// consumer in one sitting; ~30 files still import from here. New code
// should import the named lucide icon directly:
//
//   import { Bell } from 'lucide-react';                        // ✅ new code
//   import { IconBell } from '@/components/ui/icon-aliases'; // existing call sites
//
// Migrate call sites opportunistically when you're already in the file.
import { Copy } from 'lucide-react';
import type { LucideIcon, LucideProps } from 'lucide-react';

type LegacyIconSize = LucideProps['size'] | 'small' | 'large';
type LegacyIconProps = Omit<LucideProps, 'size'> & {
  size?: LegacyIconSize;
};

const normalizeSize = (size: LegacyIconSize): LucideProps['size'] => {
  if (size === 'small') return 14;
  if (size === 'large') return 22;
  return size || 16;
};

function makeIcon(Icon: LucideIcon) {
  return function LegacyIcon({ size, ...props }: LegacyIconProps) {
    return <Icon size={normalizeSize(size)} {...props} />;
  };
}
export const IconCopy = makeIcon(Copy);
