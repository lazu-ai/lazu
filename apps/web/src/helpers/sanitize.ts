import DOMPurify from 'dompurify';

export const sanitizeMarkup = (unsafeHtml = ''): string =>
  DOMPurify.sanitize(unsafeHtml, {
    USE_PROFILES: { html: true },
  });
