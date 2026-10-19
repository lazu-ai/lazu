/**
 * Copy plain text without pulling the UI/toast helper graph into consumers.
 *
 * The legacy execCommand fallback is intentionally retained for browsers and
 * embedded webviews where the async Clipboard API is unavailable.
 */
export async function copy(text: unknown): Promise<boolean> {
  const value = String(text);
  try {
    await navigator.clipboard.writeText(value);
    return true;
  } catch {
    let textarea: HTMLTextAreaElement | null = null;
    try {
      textarea = window.document.createElement('textarea');
      textarea.value = value;
      textarea.setAttribute('readonly', '');
      textarea.style.position = 'fixed';
      textarea.style.left = '-9999px';
      textarea.style.top = '-9999px';
      window.document.body.appendChild(textarea);
      textarea.select();
      // execCommand reports failure by returning false — most often because
      // the click that started this has already expired. Passing that on is
      // what lets callers avoid claiming a copy that never happened.
      return window.document.execCommand('copy');
    } catch (error: unknown) {
      console.error(error);
      return false;
    } finally {
      textarea?.remove();
    }
  }
}
