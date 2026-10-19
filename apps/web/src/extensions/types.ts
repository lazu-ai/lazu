import type { SettingField } from '../pages/Setting/settingsRuntime';
import type { TFunction } from 'i18next';
import type { ReactNode } from 'react';
import type { ZodTypeAny } from 'zod';

/* A web extension adds a feature this repository does not carry. Builds that
   compile extensions in place each one at src/extensions/<name>/extension.tsx;
   src/extensions/index.ts finds them. Every member is optional, and a build
   without extensions behaves exactly as the core. */

export type StatusPayload = Record<string, unknown> | null | undefined;

/* A full-screen app of its own under a path prefix (no console sidebar, no
   public header or footer), e.g. a creative product beside the console. */
export interface ExtensionSurface {
  /* Path prefix, e.g. "/app". Matches the prefix itself and below. */
  prefix: string;
  /* Extra class on the app shell while the surface is open. */
  shellClassName?: string;
  /* Loads the surface's module ahead of navigation (link hover/focus). */
  preload?: () => Promise<unknown>;
}

export interface OpsTabContext {
  pathname: string;
  params: Readonly<Record<string, string | undefined>>;
  title: string;
  /* The 运营 tab bar, for pages that draw their own frame. */
  nav: ReactNode;
}

/* A tab of 运营 (/console/ops). */
export interface OpsTab {
  id: string;
  /* Where the tab links. */
  to: string;
  /* NavLink exact matching. */
  end?: boolean;
  /* Position among all tabs; the core's 公告 is 10 and 邮件 is 50. */
  order: number;
  label: (t: TFunction) => string;
  /* Router patterns that open 运营 on this tab. */
  routes: string[];
  /* The tab owns the bare /console/ops address. */
  index?: boolean;
  matches: (pathname: string) => boolean;
  render: (context: OpsTabContext) => ReactNode;
}

/* An entry in the console sidebar's 工作台 group. */
export interface SidebarEntry {
  /* Module key, as listed in the sidebar module settings. */
  moduleKey: string;
  to: string;
  label: (t: TFunction) => string;
  /* Hidden unless it returns true. */
  enabled?: (status: StatusPayload) => boolean;
  /* Opens outside the console shell, so it carries the outbound marker. */
  leavesConsole?: boolean;
  /* Icon name understood by helpers/lucide (e.g. "image"). */
  iconKey?: string;
}

/* What a wallet shows about the next top-up. */
export interface TopUpOffer {
  note: ReactNode;
  bonusUSD?: (amount: number) => number;
  bonusLabel?: (bonus: number) => string;
}

/* Copy for a link, by marketing locale (en, zh, zh-TW, ja); en is the
   fallback. */
export type LocalizedLabel = Record<string, string>;

/* Links an extension adds to the public header and footer. Paths are bare
   (no locale prefix); the host puts them in the reader's locale. */
export interface PublicNav {
  /* order among the header's own items: models 10, docs 30, FAQ 50. */
  header?: {
    key: string;
    path: string;
    order: number;
    label: LocalizedLabel;
  }[];
  /* index is the position within the column's own items. */
  footer?: {
    column: 'product' | 'developers' | 'company' | 'legal';
    index: number;
    path: string;
    label: LocalizedLabel;
  }[];
}

export interface WebExtension {
  name: string;
  /* <Route> elements the app renders beside its own. */
  routes?: ReactNode;
  surfaces?: ExtensionSurface[];
  opsTabs?: OpsTab[];
  sidebar?: SidebarEntry[];
  /* Module keys switched on by default in both sidebar presets. */
  defaultModules?: { workspace?: string[] };
  /* Settings › 账户与访问 › 部署: extra fields, their defaults and schema. */
  deploymentSettings?: {
    fields: (t: TFunction) => SettingField[];
    defaults: Record<string, unknown>;
    schema: Record<string, ZodTypeAny>;
  };
  /* The header or footer for server-rendered pages of a section the
     extension owns (nginx splices them in); null for any other section.
     pathname is the section with its locale prefix. */
  publicChrome?: (
    part: 'header' | 'footer',
    pathname: string,
    section: string,
  ) => ReactNode | null;
  publicNav?: PublicNav;
  /* Runs once when the app boots, before the first render. */
  onAppStart?: () => void;
  /* Hooks; called on every render of their host, in a fixed order. */
  useTopUpOffer?: (enabled: boolean) => TopUpOffer | null;
  /* Shown under the wallet; className is the host's layout slot. */
  WalletAside?: (props: { className?: string }) => ReactNode;
  /* Shown at the top of the dashboard for the signed-in user. */
  DashboardTop?: (props: { userId?: number | string }) => ReactNode;
  /* Warms queries the dashboard will read for the signed-in account;
     sameAccount() is false once another account has signed in. */
  prefetchConsole?: (
    identity: string,
    sameAccount: () => boolean,
  ) => Promise<void>;
}
