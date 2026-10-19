import i18n from '../i18n/i18n';

/** Translate a string that is itself an i18n key; return anything else as
 *  it is. Server responses no longer carry prose to translate: failures are
 *  rendered from their code (lib/api-error), and success copy is written by
 *  the console with t(). */
export const translateMessage = (message: unknown): string => {
  const raw = String(message ?? '').trim();
  if (!raw) return '';
  if (i18n.exists(raw) || i18n.exists(`translation:${raw}`)) {
    return String(i18n.t(raw));
  }
  return raw;
};
