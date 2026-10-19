/** Chat links are configured by an administrator as URL templates. */

/** True when the template substitutes the Key at all. One that only needs the
 *  gateway address can be opened without spending a reveal — and works for a
 *  member who may not read the value. */
export const chatTemplateNeedsKey = (value?: string): boolean => {
  const url = String(value || '').trim();
  if (url.startsWith('fluent')) return true;
  return url.includes('{key}') || url.includes('{cherryConfig}');
};

/** True when following this template would put the Key into an https URL,
 *  where it reaches the destination server and every log on the way. Those
 *  templates are not offered: the connect dialog's manual steps are how a web
 *  app gets set up. A custom scheme (cherrystudio://, opencat://) hands the
 *  value to an installed app instead and stays. */
export const chatTemplateLeaksKey = (value?: string): boolean => {
  const url = String(value || '').trim();
  if (!url.includes('{key}') && !url.includes('{cherryConfig}')) return false;
  // Resolve relative and protocol-relative links exactly as browser navigation
  // does; whitespace inside a scheme must not bypass the guard either.
  try {
    const protocol = new URL(url, 'https://lazu.invalid').protocol;
    return [
      'http:',
      'https:',
      'javascript:',
      'data:',
      'blob:',
      'file:',
      'about:',
    ].includes(protocol);
  } catch {
    return true;
  }
};
