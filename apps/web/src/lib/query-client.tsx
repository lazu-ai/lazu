import {
  QueryClient,
  QueryClientProvider,
  onlineManager,
} from '@tanstack/react-query';
import { ReactQueryDevtools } from '@tanstack/react-query-devtools';
import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { Toaster } from 'sonner';
import { readApiError } from './api-error';
import type { ReactNode } from 'react';

/* A read the server turned away for being too quick says when to come back.
 * Waits that short are absorbed with one quiet retry instead of painting an
 * error the reader can do nothing about; longer ones surface as usual. */
const RATE_LIMIT_RETRY_MAX_SECONDS = 10;

export const rateLimitRetryAfter = (error: unknown): number | null => {
  const info = readApiError(error);
  if (info.status !== 429) return null;
  const seconds = Number(info.details.retry_after_seconds);
  return Number.isFinite(seconds) && seconds > 0 ? seconds : 1;
};

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      networkMode: 'online',
      refetchOnWindowFocus: false,
      retry: (failureCount, error) => {
        const wait = rateLimitRetryAfter(error);
        return (
          failureCount < 1 &&
          wait !== null &&
          wait <= RATE_LIMIT_RETRY_MAX_SECONDS
        );
      },
      retryDelay: (_attempt, error) => (rateLimitRetryAfter(error) ?? 1) * 1000,
      staleTime: 30 * 1000,
    },
    mutations: {
      networkMode: 'online',
      retry: 0,
    },
  },
});

if (typeof window !== 'undefined') {
  onlineManager.setEventListener((setOnline) => {
    const updateOnline = () => setOnline(window.navigator.onLine);

    window.addEventListener('online', updateOnline, false);
    window.addEventListener('offline', updateOnline, false);
    updateOnline();

    return () => {
      window.removeEventListener('online', updateOnline);
      window.removeEventListener('offline', updateOnline);
    };
  });
}

interface AppQueryProviderProps {
  children: ReactNode;
}

export function AppQueryProvider({ children }: AppQueryProviderProps) {
  const enableDevtools = import.meta.env.VITE_QUERY_DEVTOOLS === 'true';

  return (
    <QueryClientProvider client={queryClient}>
      {children}
      {enableDevtools ? (
        <ReactQueryDevtools buttonPosition='bottom-left' />
      ) : null}
    </QueryClientProvider>
  );
}

// No richColors: the ink/paper toast carries its semantic in the icon, and
// sonner's tinted surfaces would fight the reversal.
//
// The toaster lives on <body>, beside the dialog portals: inside #root's
// stacking context every toast sat under an open dialog, and an undo raised
// from a dialog could not be pressed. It mounts after hydration so the
// prerendered markup and the first client render agree.
export function AppToaster() {
  const [host, setHost] = useState<HTMLElement | null>(null);
  useEffect(() => setHost(document.body), []);
  if (!host) return null;
  return createPortal(
    <Toaster
      closeButton
      expand={false}
      mobileOffset={{ bottom: 'var(--app-toast-mobile-bottom, 16px)' }}
      position='bottom-right'
      toastOptions={{
        classNames: {
          toast: 'app-toast',
          title: 'app-toast__title',
          description: 'app-toast__description',
          actionButton: 'app-toast__action',
          cancelButton: 'app-toast__cancel',
        },
      }}
    />,
    host,
  );
}

export { queryClient };
