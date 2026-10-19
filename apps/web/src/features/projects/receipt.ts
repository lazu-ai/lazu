/* Receipts: one dark toast, six seconds, at most one link. Errors are
 * written as "what is now true, then one action" — a timeout is an unknown
 * outcome, never a claim that nothing happened. */
import { RECEIPT_SECONDS } from './budget';
import { toast } from '../../components/ui/toast';
import { errorMessage } from '../../lib/api-error';

export const receipt = (
  message: string,
  link?: { label: string; href: string },
) =>
  toast.success(message, {
    duration: RECEIPT_SECONDS * 1000,
    className: 'projects-receipt',
    action: link
      ? {
          label: link.label,
          onClick: () => {
            window.location.assign(link.href);
          },
        }
      : undefined,
  });

/** The sentence a reader sees for a failed project call: the `errors` copy
 *  for its code, never the server's English message. */
export const describeError = (error: unknown): string => errorMessage(error);

export const errorReceipt = (error: unknown) =>
  toast.error(describeError(error), { duration: 8000 });
