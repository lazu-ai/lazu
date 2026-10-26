import { Suspense } from 'react';
import { useTranslation } from 'react-i18next';
import { Navigate, NavLink, useLocation, useParams } from 'react-router-dom';
import MailDetail from './MailDetail';
import MailLog from './MailLog';
import './ops.css';
import { Skeleton } from '../../../components/ui/skeleton';
import { extensionOpsTabs, type OpsTab } from '../../../extensions';
import AnnouncementsPage from '../Announcements';

/* 运营: notices and mail in the core; extensions add their own tabs. Each tab
   is a page of its own URL, like 模型运营's pages. */
const coreTabs: OpsTab[] = [
  {
    id: 'notices',
    to: '/console/ops/notices',
    order: 10,
    label: (t) => t('page.console.ops.tab.notices', { defaultValue: '公告' }),
    routes: [],
    matches: (pathname) => pathname.startsWith('/console/ops/notices'),
    render: ({ title, nav }) => (
      <AnnouncementsPage opsTitle={title} opsTabs={nav} />
    ),
  },
  {
    id: 'mail',
    to: '/console/ops/mail',
    order: 50,
    label: (t) => t('page.console.ops.tab.mail', { defaultValue: '邮件' }),
    routes: [],
    matches: (pathname) => pathname.startsWith('/console/ops/mail'),
    render: ({ pathname, params }) => {
      if (pathname.startsWith('/console/ops/mail/digest'))
        return <Navigate replace to='/console/ops/mail' />;
      if (params.mailId)
        return <MailDetail key={params.mailId} mailId={params.mailId} />;
      return <MailLog />;
    },
  },
];

// Read on use: the extension list is not ready while modules load.
const allTabs = () =>
  [...coreTabs, ...extensionOpsTabs()].sort((a, b) => a.order - b.order);

export function OpsNav() {
  const { t } = useTranslation();
  return (
    <nav
      className='ops-nav'
      aria-label={t('page.console.ops.title', { defaultValue: '运营' })}
    >
      {allTabs().map((tab) => (
        <NavLink
          className={({ isActive }) =>
            `ops-nav__item${isActive ? ' is-active' : ''}`
          }
          end={tab.end}
          key={tab.id}
          to={tab.to}
        >
          {tab.label(t)}
        </NavLink>
      ))}
    </nav>
  );
}

const tabOf = (pathname: string): OpsTab | undefined => {
  const tabs = allTabs();
  return (
    tabs.find((tab) => tab.matches(pathname)) ??
    (pathname.replace(/\/+$/, '') === '/console/ops'
      ? tabs.find((tab) => tab.index)
      : undefined)
  );
};

export default function OpsPage() {
  const { t } = useTranslation();
  const { pathname } = useLocation();
  const params = useParams();
  const tab = tabOf(pathname);
  if (!tab) return <Navigate replace to='/console/ops/notices' />;
  const title = t('page.console.ops.title', { defaultValue: '运营' });
  return (
    <Suspense fallback={<Skeleton className='ops-editor-skeleton' />}>
      {tab.render({ pathname, params, title, nav: <OpsNav /> })}
    </Suspense>
  );
}
