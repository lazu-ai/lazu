/* Route loading — skeleton inheritance.
 *
 * What used to be here for every non-console route was a bordered, max-w-sm
 * box holding a wordmark, a spinner in a 44px outlined square, and two lines
 * of prose, laid over a `fixed inset-0 z-50` curtain. Three things were wrong
 * with it: it looked like a modal dialog nobody had opened; the curtain hid
 * chrome that was already painted; and its flat `--ui-bg` ground was not the
 * ground it covered — the real pages sit on a three-layer gradient with two
 * ambient lights and a 64px grid — so every "finished loading" ended in a
 * visible colour jump.
 *
 * Each lazy route now declares its own outline instead. Console routes keep
 * the shape they already had, /login gets the shape of the door it is about
 * to become, and every other public page gets a heading-and-paragraph
 * outline. Nothing paints at all for the first 300ms, because most chunks
 * arrive inside that window and the best loading state is the one that never
 * appeared.
 */
import { useTranslation } from 'react-i18next';
import { useLocation } from 'react-router-dom';
import { normalizeShellPath } from './route-shell';
import { useDelayedReveal } from '../../hooks/common/useDelayedReveal';
import DoorSkeleton, { useDoorReveal } from '../auth/DoorSkeleton';
import { Skeleton } from '../ui';
import { KeyLookupHeading } from '../ui/key-lookup';

function ContentSkeleton() {
  return (
    <div className='route-skeleton'>
      <Skeleton height={34} width='min(420px, 62%)' />
      <Skeleton height={15} width='min(640px, 88%)' />
      <Skeleton height={15} width='min(600px, 81%)' />
      <Skeleton height={15} width='min(470px, 64%)' />
      <div className='route-skeleton__gap' />
      <Skeleton height={22} width='min(300px, 42%)' />
      <Skeleton height={15} width='min(620px, 85%)' />
      <Skeleton height={15} width='min(560px, 78%)' />
    </div>
  );
}

function ConsoleSkeletonRows() {
  return (
    <>
      <div className='console-route-loading__header'>
        <Skeleton height={28} width='min(320px, 58%)' />
        <Skeleton height={16} width='min(520px, 78%)' />
      </div>
      <div className='console-route-loading__toolbar'>
        <Skeleton height={32} width={132} />
        <Skeleton height={32} width={96} />
      </div>
      <div className='console-route-loading__body'>
        <Skeleton height={42} width='100%' />
        <Skeleton height={42} width='100%' />
        <Skeleton height={42} width='100%' />
        <Skeleton height={42} width='100%' />
        <Skeleton height={42} width='100%' />
      </div>
    </>
  );
}

/* Console-shaped pages outside the console shell. They render the same
 * `.console-page-shell` the sidebar pages do — including its header
 * clearance — so their outline has to be that shape and carry that
 * clearance, or the page lands 60px below where the outline stood. */
const CONSOLE_SHAPED_ROUTES = new Set<string>([]);

const Loading = () => {
  const { t } = useTranslation();
  const location = useLocation();
  const pathname = normalizeShellPath(location.pathname);
  const isLogin = pathname === '/login' || pathname === '/login/password';
  const isConsoleShaped =
    pathname === '/console' ||
    pathname.startsWith('/console/') ||
    CONSOLE_SHAPED_ROUTES.has(pathname);
  // The door's clock is shared with the door itself, which keeps waiting for
  // its sign-in configuration after this fallback hands over; every other
  // route's wait ends when its chunk lands, so it keeps the local clock.
  const doorRevealed = useDoorReveal(isLogin);
  const revealed = useDelayedReveal(!isLogin);

  // Holding the space while hidden keeps the reveal from shifting the page —
  // and on a console-shaped route the wrapper carries the page's own top
  // clearance from the first frame, so the box never moves either.
  if (isLogin) {
    return (
      <div className='route-loading route-loading--door'>
        {doorRevealed ? <DoorSkeleton /> : null}
      </div>
    );
  }

  if (pathname === '/key' || pathname === '/keyusage') {
    return (
      <section className='console-page-shell' aria-busy='true'>
        <div className='console-page-body'>
          <div
            className='key-lookup'
            data-key-loading=''
            style={{ visibility: revealed ? 'visible' : 'hidden' }}
          >
            <KeyLookupHeading />
            <div className='key-lookup__form'>
              <div className='lazu-field'>
                <span className='lazu-label'>
                  {t('legacy.i18n.api_key.5df1d83e18')}
                </span>
                <Skeleton height={38} width='100%' />
              </div>
              <div className='lazu-field'>
                <span className='lazu-label'>
                  {t('legacy.i18n.date_range.3783f29c30')}
                </span>
                <Skeleton height={38} width='100%' />
              </div>
              <Skeleton height={40} width={64} />
            </div>
          </div>
        </div>
      </section>
    );
  }

  if (isConsoleShaped) {
    return (
      <div
        aria-busy={revealed || undefined}
        aria-live={revealed ? 'polite' : undefined}
        className='console-route-loading'
        role={revealed ? 'status' : undefined}
      >
        {revealed ? <ConsoleSkeletonRows /> : null}
      </div>
    );
  }

  if (!revealed) return <div aria-hidden='true' className='route-loading' />;

  return (
    <div
      aria-busy='true'
      aria-live='polite'
      className='route-loading route-loading--content'
      role='status'
    >
      <ContentSkeleton />
    </div>
  );
};

export default Loading;
