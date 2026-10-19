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
import {
  AlertTriangle,
  Bell,
  Bolt,
  ChevronDown,
  ChevronUp,
  CircleDollarSign,
  Clock,
  Copy,
  Edit,
  Gift,
  Github,
  HelpCircle,
  KeyRound,
  Link,
  Lock,
  Mail,
  Minus,
  Plus,
  RefreshCw,
  Save,
  Search,
  Send,
  Shield,
  SquareStack,
  Trash2,
  Type,
  User,
  X,
} from 'lucide-react';
import React from 'react';
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

export const IconAlertTriangle = makeIcon(AlertTriangle);
export const IconBell = makeIcon(Bell);
export const IconBolt = makeIcon(Bolt);
export const IconChevronDown = makeIcon(ChevronDown);
export const IconChevronUp = makeIcon(ChevronUp);
export const IconClose = makeIcon(X);
export const IconCoinMoneyStroked = makeIcon(CircleDollarSign);
export const IconCopy = makeIcon(Copy);
export const IconDelete = makeIcon(Trash2);
export const IconEdit = makeIcon(Edit);
export const IconGift = makeIcon(Gift);
export const IconGithubLogo = makeIcon(Github);
export const IconHelpCircle = makeIcon(HelpCircle);
export const IconHistogram = makeIcon(SquareStack);
export const IconKey = makeIcon(KeyRound);
export const IconLink = makeIcon(Link);
export const IconLock = makeIcon(Lock);
export const IconMail = makeIcon(Mail);
export const IconMinus = makeIcon(Minus);
export const IconMoneyExchangeStroked = makeIcon(CircleDollarSign);
export const IconPlus = makeIcon(Plus);
export const IconPulse = makeIcon(Bolt);
export const IconRefresh = makeIcon(RefreshCw);
export const IconSave = makeIcon(Save);
export const IconSearch = makeIcon(Search);
export const IconSend = makeIcon(Send);
export const IconSetting = makeIcon(SquareStack);
export const IconShield = makeIcon(Shield);
export const IconStopwatchStroked = makeIcon(Clock);
export const IconTextStroked = makeIcon(Type);
export const IconTypograph = makeIcon(Type);
export const IconUser = makeIcon(User);
