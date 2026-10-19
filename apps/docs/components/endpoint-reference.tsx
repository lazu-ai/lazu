import { type ReactNode, useMemo, useRef, useState } from "react";
import { EndpointBilling } from "./endpoint-billing";

const API_ORIGIN = String(
  import.meta.env.VITE_LAZU_API_ORIGIN || "https://api.lazu.ai",
).replace(/\/+$/, "");

type EndpointPageProps = {
  aside?: ReactNode;
  children: ReactNode;
  description: string;
  eyebrow?: string;
  method: "DELETE" | "GET" | "PATCH" | "POST" | "PUT";
  path: string;
  sendable?: boolean;
  title: string;
};

type CodeSampleProps = {
  children: ReactNode;
  language?: string;
  title: string;
};

type FieldProps = {
  children?: ReactNode;
  fields?: FieldChild[];
  name: string;
  nullable?: boolean;
  required?: boolean;
  type: string;
  values?: string[];
};

type FieldChild = {
  description: ReactNode;
  name: string;
  type: string;
};

type MiniCardProps = {
  children: ReactNode;
  title: string;
};

export function EndpointPage({
  aside,
  children,
  description,
  eyebrow,
  method,
  path,
  sendable = true,
  title,
}: EndpointPageProps) {
  return (
    <div className="lazu-endpoint-page">
      <main className="lazu-endpoint-main">
        {/* The page actions (Copy page ▾) are mounted into this row by
            public/agent-actions.js, next to the eyebrow. */}
        <div className="lazu-page-head">
          {eyebrow ? (
            <div className="lazu-endpoint-eyebrow">{eyebrow}</div>
          ) : null}
        </div>
        <div className="lazu-endpoint-route">
          <MethodBadge method={method} />
          <code>{API_ORIGIN + path}</code>
        </div>
        <h1>{title}</h1>
        <p className="lazu-endpoint-description">{description}</p>
        <EndpointBilling method={method} path={path} />
        {children}
      </main>
      {aside ? (
        <section
          className={
            sendable
              ? "lazu-endpoint-examples lazu-endpoint-examples--live"
              : "lazu-endpoint-examples lazu-endpoint-examples--inline"
          }
        >
          <div className="lazu-endpoint-aside-stack">{aside}</div>
        </section>
      ) : null}
    </div>
  );
}

export function EndpointAside({ children }: { children: ReactNode }) {
  return <div className="lazu-endpoint-aside-stack">{children}</div>;
}

export function MiniCard({ children, title }: MiniCardProps) {
  return (
    <section className="lazu-mini-card">
      <h3>{title}</h3>
      {children}
    </section>
  );
}

export function CodeSample({
  children,
  language = "text",
  title,
}: CodeSampleProps) {
  const [copied, setCopied] = useState(false);
  const [copyFallback, setCopyFallback] = useState(false);
  const codeRef = useRef<HTMLElement>(null);
  const text = useMemo(
    () => (typeof children === "string" ? children : ""),
    [children],
  );

  const copy = async () => {
    if (!text) return;
    setCopyFallback(false);
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1200);
    } catch {
      if (codeRef.current)
        window.getSelection()?.selectAllChildren(codeRef.current);
      setCopyFallback(true);
    }
  };

  return (
    <section className="lazu-cb lazu-code-sample">
      <div className="lazu-cb-head">
        <span className="lazu-cb-title">{title}</span>
        <span className="lazu-cb-tab" data-on="true">
          {language}
        </span>
        <button
          type="button"
          className="lazu-cb-copy"
          aria-label={`Copy ${title}`}
          onClick={copy}
          disabled={!text}
        >
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
      <pre className="lazu-cb-pre">
        <code ref={codeRef}>{children}</code>
      </pre>
      {copyFallback ? (
        <p role="status">Code selected. Press Ctrl+C or ⌘C to copy.</p>
      ) : null}
    </section>
  );
}

export function FieldList({ children }: { children: ReactNode }) {
  return <div className="lazu-field-list">{children}</div>;
}

export function Field({
  children,
  fields,
  name,
  nullable = false,
  required = false,
  type,
  values,
}: FieldProps) {
  return (
    <div className="lazu-field">
      <div className="lazu-field-head">
        <code className="lazu-field-name">{name}</code>
        <span className="lazu-field-type">{type}</span>
        {required ? <strong className="lazu-field-req">required</strong> : null}
        {nullable ? <em className="lazu-field-opt">nullable</em> : null}
      </div>
      {children ? <div className="lazu-field-body">{children}</div> : null}
      {values?.length ? (
        <div className="lazu-field-values">
          {values.map((value) => (
            <code key={value}>{value}</code>
          ))}
        </div>
      ) : null}
      {fields?.length ? (
        <div className="lazu-field-children">
          {fields.map((field) => (
            <div className="lazu-field-child" key={field.name}>
              <div className="lazu-field-child-head">
                <code>{field.name}</code>
                <span>{field.type}</span>
              </div>
              <div className="lazu-field-child-body">{field.description}</div>
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
}

export function EndpointGrid({ children }: { children: ReactNode }) {
  return <div className="lazu-endpoint-grid">{children}</div>;
}

export function EndpointTile({
  children,
  title,
}: {
  children: ReactNode;
  title: string;
}) {
  return (
    <section className="lazu-endpoint-tile">
      <h3>{title}</h3>
      {children}
    </section>
  );
}

export function MethodBadge({
  method,
}: {
  method: "DELETE" | "GET" | "PATCH" | "POST" | "PUT";
}) {
  return (
    <span className="lazu-method-badge" data-method={method}>
      {method}
    </span>
  );
}
