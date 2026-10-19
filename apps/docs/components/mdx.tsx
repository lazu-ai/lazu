import { AlertTriangle, CheckCircle2, Info, XCircle } from "lucide-react";
import { type ReactNode } from "react";
import { withBase } from "./base-path";
export {
  CodeSample,
  EndpointAside,
  EndpointGrid,
  EndpointPage,
  EndpointTile,
  Field,
  FieldList,
  MethodBadge,
  MiniCard,
} from "./endpoint-reference";
export { EndpointRail } from "./endpoint-rail";
export { ApiExplorer } from "./api-explorer";
export { DocsHome } from "./docs-home";

type CardProps = {
  children?: ReactNode;
  description?: string;
  href?: string;
  title: string;
};

type CalloutProps = {
  children: ReactNode;
  type?: "danger" | "info" | "note" | "success" | "tip" | "warning";
};

export function Cards({ children }: { children: ReactNode }) {
  return <div className="dub-docs-cards">{children}</div>;
}

export function Card({ children, description, href, title }: CardProps) {
  const content = (
    <>
      <strong>{title}</strong>
      {description ? <span>{description}</span> : null}
      {children ? <div>{children}</div> : null}
    </>
  );

  if (href) {
    return (
      <a className="dub-docs-card" href={withBase(href)}>
        {content}
      </a>
    );
  }

  return <div className="dub-docs-card">{content}</div>;
}

const CALLOUT_ICON = {
  danger: XCircle,
  info: Info,
  note: Info,
  success: CheckCircle2,
  tip: CheckCircle2,
  warning: AlertTriangle,
} as const;

export function Callout({ children, type = "info" }: CalloutProps) {
  const ToneIcon = CALLOUT_ICON[type];

  return (
    <aside
      className="dub-docs-callout"
      data-type={type}
      role={type === "danger" || type === "warning" ? "alert" : "note"}
    >
      <ToneIcon
        aria-hidden="true"
        className="dub-docs-callout__icon"
        size={16}
      />
      <div className="dub-docs-callout__body">{children}</div>
    </aside>
  );
}
