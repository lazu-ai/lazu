/* Extensions' part of the server-rendered public pages (scripts and styles
   the API-rendered pages load through public-site/entry.ts). Builds with
   extensions place it at src/extensions/<name>/public-site.ts. Importing it
   loads their styles with the entry's; start() runs after the public chrome
   is mounted. */
const modules = import.meta.glob<{ start?: () => void }>('./*/public-site.ts', {
  eager: true,
});

export function startExtensionPublicSites() {
  for (const key of Object.keys(modules).sort()) modules[key].start?.();
}
