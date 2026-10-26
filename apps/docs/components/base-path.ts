// The docs are served under lazu.ai/docs (Vocs `basePath`, which Vite exposes
// as BASE_URL = "/docs/"). Vocs' own links add it; plain <a href> and
// window.location reads in these components have to add or strip it here.
const BASE_PATH = import.meta.env.BASE_URL.replace(/\/+$/, "");

/** "/quickstart" -> "/docs/quickstart". Leaves absolute and relative URLs alone. */
export function withBase(href: string): string {
  if (!BASE_PATH || !href.startsWith("/") || href.startsWith("//")) return href;
  if (href === BASE_PATH || href.startsWith(`${BASE_PATH}/`)) return href;
  return `${BASE_PATH}${href}`;
}

/** "/docs/zh/quickstart" -> "/zh/quickstart"; the docs home is "/". */
export function stripBase(pathname: string): string {
  if (!BASE_PATH) return pathname;
  if (pathname === BASE_PATH) return "/";
  if (pathname.startsWith(`${BASE_PATH}/`))
    return pathname.slice(BASE_PATH.length);
  return pathname;
}
