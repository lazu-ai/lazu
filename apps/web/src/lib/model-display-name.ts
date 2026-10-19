/** Public Seed names; API snippets and copy controls retain the canonical ID. */
export function modelDisplayName(id: string, name?: string): string {
  switch (id) {
    case 'seedream-5.0-flash':
      return 'Seedream 5.0 Flash';
    case 'seedream-5.0-lite':
      return 'Seedream 5.0 Lite';
    case 'seedream-5.0-pro':
      return 'Seedream 5.0 Pro';
    case 'seedream-4.5':
      return 'Seedream 4.5';
    case 'seed-2.0-pro':
      return 'Seed 2.0 Pro';
    case 'seed-2.1-turbo':
      return 'Seed 2.1 Turbo';
    default:
      return name || id;
  }
}
