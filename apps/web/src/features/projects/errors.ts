/* Team-project calls fail with the console's one error class, LazuApiError.
 * The server builds it from the error envelope; this builds the same object
 * for the few checks the client makes before calling (and for the mock), so
 * pages read `code` / `details` / `status` the same way either way. */
import { LazuApiError } from '../../lib/api-error';

export const projectError = (
  code: string,
  details: Record<string, unknown> = {},
  status = 400,
  message = code,
): LazuApiError =>
  new LazuApiError({
    code,
    type: '',
    lazu: '',
    message,
    requestId: '',
    docs: '',
    details,
    status,
  });
