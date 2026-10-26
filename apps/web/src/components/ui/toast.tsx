import { toast as sonnerToast } from 'sonner';
import { recordStatusIssue } from './status-center';
import { toastConstants } from '../../constants/toast.constants';
import { MOBILE_BREAKPOINT } from '../../hooks/common/useIsMobile';
import type { ExternalToast } from 'sonner';

type ToastMessage = Parameters<typeof sonnerToast.success>[0];

// Narrow screens get a centred toast for every tone, matching the console's
// feedback rules; a caller can still pass its own position.
const narrowScreen =
  typeof window !== 'undefined' &&
  typeof window.matchMedia === 'function' &&
  window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`).matches;

const withDuration = (
  duration: number,
  options?: ExternalToast,
): ExternalToast => ({
  duration,
  ...(narrowScreen ? { position: 'bottom-center' as const } : {}),
  ...options,
});

type TaskToastOptions = {
  error?: ToastMessage;
  loading: ToastMessage;
  success?: ToastMessage;
};

const createTaskToast = (
  messages: TaskToastOptions,
  options?: ExternalToast,
) => {
  const id = sonnerToast.loading(messages.loading, options);

  return {
    dismiss: () => sonnerToast.dismiss(id),
    error: (message: ToastMessage = messages.error || 'Task failed') => {
      if (typeof message === 'string') {
        recordStatusIssue({
          kind: 'error',
          message,
          source: 'Task',
        });
      }
      return sonnerToast.error(message, {
        ...withDuration(toastConstants.ERROR_TIMEOUT, options),
        id,
      });
    },
    id,
    success: (message: ToastMessage = messages.success || 'Task complete') =>
      sonnerToast.success(message, {
        ...withDuration(toastConstants.SUCCESS_TIMEOUT, options),
        id,
      }),
  };
};

export const notify = {
  custom: sonnerToast,
  dismiss: sonnerToast.dismiss,
  error: (message: ToastMessage, options?: ExternalToast) =>
    sonnerToast.error(
      message,
      withDuration(toastConstants.ERROR_TIMEOUT, options),
    ),
  info: (message: ToastMessage, options?: ExternalToast) =>
    sonnerToast.info(
      message,
      withDuration(toastConstants.INFO_TIMEOUT, options),
    ),
  loading: (message: ToastMessage, options?: ExternalToast) =>
    sonnerToast.loading(message, options),
  promise: sonnerToast.promise,
  task: createTaskToast,
  success: (message: ToastMessage, options?: ExternalToast) =>
    sonnerToast.success(
      message,
      withDuration(toastConstants.SUCCESS_TIMEOUT, options),
    ),
  warning: (message: ToastMessage, options?: ExternalToast) =>
    sonnerToast.warning(
      message,
      withDuration(toastConstants.WARNING_TIMEOUT, options),
    ),
};

export const toast = notify;
