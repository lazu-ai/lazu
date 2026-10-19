import React, { type ReactNode } from 'react';
import { withTranslation, type WithTranslation } from 'react-i18next';
import { copy } from '../../helpers/clipboard';
import {
  canReloadForStaleChunk,
  isStaleChunkError,
  reloadForStaleChunk,
} from '../../lib/stale-chunk';
import { Button, StatusPage } from '../ui';

type ErrorBoundaryProps = WithTranslation & {
  children?: ReactNode;
  /* Standalone routes need to paint the Yunmu ground. Console routes already
     sit inside the shell's ground and must not add a nested 100vh canvas. */
  ground?: boolean;
  /* The boundary wraps every route. Without a reset, one crashed page stays
     on screen after the reader navigates away through the sidebar. */
  resetKey?: string;
};

type ErrorBoundaryState = {
  copied: boolean;
  error: unknown;
  hasError: boolean;
  /* A stale chunk after a deploy: the page is reloading onto the new
     release, so there is no crash to report. */
  reloading: boolean;
};

class ErrorBoundary extends React.Component<
  ErrorBoundaryProps,
  ErrorBoundaryState
> {
  constructor(props: ErrorBoundaryProps) {
    super(props);
    this.state = {
      copied: false,
      error: null,
      hasError: false,
      reloading: false,
    };
  }

  static getDerivedStateFromError(error: unknown): Partial<ErrorBoundaryState> {
    // Decided during render so the crash page never flashes before the
    // reload; componentDidCatch below starts the reload itself.
    return {
      copied: false,
      error,
      hasError: true,
      reloading: isStaleChunkError(error) && canReloadForStaleChunk(),
    };
  }

  componentDidCatch(error: Error, errorInfo: React.ErrorInfo) {
    if (this.state.reloading && reloadForStaleChunk()) return;
    if (this.state.reloading) this.setState({ reloading: false });
    console.error('[ErrorBoundary]', error, errorInfo);
  }

  componentDidUpdate(previous: ErrorBoundaryProps) {
    if (
      this.state.hasError &&
      !this.state.reloading &&
      previous.resetKey !== this.props.resetKey
    ) {
      this.setState({ copied: false, error: null, hasError: false });
    }
  }

  /* What a reader pastes into a bug report: the error, where it happened
   * and when. */
  private copyDetails = async () => {
    const { error } = this.state;
    const message =
      error instanceof Error
        ? `${error.name}: ${error.message}`
        : String(error ?? 'Unknown error');
    const route =
      typeof window === 'undefined'
        ? ''
        : `${window.location.pathname}${window.location.search}`;
    const copied = await copy(
      [message, `route: ${route}`, `time: ${new Date().toISOString()}`].join(
        '\n',
      ),
    );
    if (copied) this.setState({ copied: true });
  };

  render() {
    // Hold the space blank while the new release loads.
    if (this.state.reloading) return null;
    if (this.state.hasError) {
      const { ground = true, t } = this.props;
      return (
        <StatusPage
          action={
            <Button onClick={() => window.location.reload()} variant='primary'>
              {t('page.errorBoundary.reload')}
            </Button>
          }
          description={t('page.errorBoundary.description')}
          ground={ground}
          live
          mark='Error'
          secondaryAction={
            <Button onClick={() => void this.copyDetails()} variant='ghost'>
              {this.state.copied
                ? t('page.errorBoundary.copied')
                : t('page.errorBoundary.copyAction')}
            </Button>
          }
          title={t('page.errorBoundary.title')}
        />
      );
    }
    return this.props.children;
  }
}

export default withTranslation()(ErrorBoundary);
