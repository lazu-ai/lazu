import React, { lazy, Suspense } from 'react';
import {
  Navigate,
  Route,
  Routes,
  useLocation,
  useParams,
} from 'react-router-dom';
import Loading from './components/layout/AppLoading';
import MarketingLayout from './components/marketing/MarketingLayout';
import { useConsoleScope } from './context/ConsoleScope';
import { extensionOpsTabs, webExtensions } from './extensions';
import { TokenGate, WalletGate } from './features/projects/gates';
import { ProjectRoute, TeamRoute } from './features/projects/ProjectRoute';
import { PrivateRoute, RootRoute, ModuleRoute } from './helpers/auth';
import {
  About,
  FAQ,
  Home,
  PrivacyPolicy,
  UserAgreement,
} from './lib/marketing-pages';
import { notFoundLoader } from './lib/not-found-preload';

const LoginForm = lazy(() => import('./components/auth/LoginForm'));
const OIDCCallback = lazy(() => import('./components/auth/OIDCCallback'));
const Forbidden = lazy(() => import('./pages/Forbidden'));
const Setting = lazy(() => import('./pages/Setting'));
const PasswordResetForm = lazy(
  () => import('./components/auth/PasswordResetForm'),
);
const PasswordResetConfirm = lazy(
  () => import('./components/auth/PasswordResetConfirm'),
);
const Token = lazy(() => import('./pages/Token'));
const PaymentReturn = lazy(() => import('./pages/PaymentReturn'));
const PaymentDocument = lazy(() => import('./pages/PaymentDocument'));
const Wallet = lazy(() => import('./pages/Wallet'));
const Usage = lazy(() => import('./pages/Usage'));
const SessionDetail = lazy(
  () => import('./pages/Log/sessions/SessionDetailPage'),
);
const KeyInspector = lazy(() => import('./pages/KeyInspector'));
const OpsPage = lazy(() => import('./pages/Console/Ops'));
const SearchBackend = lazy(() => import('./pages/Console/SearchBackend'));
const ModelRoutesPage = lazy(() => import('./pages/Routes'));
const ChannelHealthPage = lazy(() => import('./pages/ChannelHealth'));
const OrganizationsPage = lazy(() => import('./pages/Organizations'));
const ModelOpsPage = lazy(() => import('./pages/ModelOps'));
const ModelOpsSourcesPage = lazy(() => import('./pages/ModelOps/SourcesPage'));
const PricePlansPage = lazy(() => import('./pages/ModelOps/PricePlansPage'));
const PricePlanEditorPage = lazy(
  () => import('./pages/ModelOps/PricePlanEditorPage'),
);
function PricePlanRedirect() {
  const { id = '' } = useParams();
  return (
    <Navigate replace to={`/console/price-plans/${encodeURIComponent(id)}`} />
  );
}
const PersonalSetting = lazy(
  () => import('./components/settings/PersonalSetting'),
);
const Dashboard = lazy(() => import('./pages/Dashboard'));
/* Member details open as a dialog on the members list (2026-09-28); old
   links to /console/project/members/:userId land there. */
function MemberDetailRedirect() {
  const { userId = '' } = useParams();
  return (
    <Navigate
      replace
      to={`/console/project/members?edit=${encodeURIComponent(userId)}`}
    />
  );
}
const ProjectMembersPage = lazy(
  () => import('./features/projects/pages/MembersPage'),
);
const ProjectSettingsPage = lazy(
  () => import('./features/projects/pages/SettingsPage'),
);
const InviteAcceptPage = lazy(
  () => import('./features/projects/pages/InviteAcceptPage'),
);
const Quickstart = lazy(() => import('./pages/Quickstart'));
const NotFound = lazy(notFoundLoader);
/** Retired: every Key in the console now has an owner and a row, and that row
 *  answers "how much has this Key used" in place.
 *
 *  A Key Pass holds a key without a console account, so it cannot see that
 *  table — /console/token admits only workspace managers and members. Its
 *  bookmark goes to the public inspector, which is the page this route used to
 *  wrap. Everyone else lands on the row that replaced it. */
const RetiredKeyInspector = () => {
  const { activeScope } = useConsoleScope();
  return (
    <Navigate
      to={activeScope?.kind === 'key_pass' ? '/key' : '/console/token'}
      replace
    />
  );
};

function App() {
  const location = useLocation();

  return (
    <Suspense fallback={<Loading></Loading>}>
      <Routes>
        {/*
            Marketing surface: every public page renders inside MarketingLayout
            so the language + theme switcher header is always present.
          */}
        <Route element={<MarketingLayout />}>
          <Route path='/' element={<Home />} />
          <Route path='/faq' element={<FAQ />} />
          <Route path='/about' element={<About />} />
          <Route path='/privacy' element={<PrivacyPolicy />} />
          <Route path='/terms' element={<UserAgreement />} />
          {/* Crawlable locale variants. English stays unprefixed; these
              paths force their language via MarketingLayout so the URL —
              not the visitor's stored preference — decides what renders. */}
          {['zh', 'zh-TW', 'ja'].map((locale) => (
            <Route key={locale} path={`/${locale}`}>
              <Route index element={<Home />} />
              <Route path='faq' element={<FAQ />} />
              <Route path='about' element={<About />} />
              <Route path='privacy' element={<PrivacyPolicy />} />
              <Route path='terms' element={<UserAgreement />} />
            </Route>
          ))}
          <Route path='/status' element={<Navigate to='/' replace />} />
          {/* Public surfaces that used to wear the console's header: a
              visitor who is not signed in got a nav full of doors they
              cannot open. */}
          <Route
            path='/key'
            element={
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <KeyInspector />
              </Suspense>
            }
          />
          <Route path='*' element={<NotFound />} />
        </Route>
        <Route
          path='/forbidden'
          element={
            <Suspense fallback={<Loading />}>
              <Forbidden />
            </Suspense>
          }
        />
        <Route
          path='/console/status'
          element={<Navigate to='/console' replace />}
        />
        {/* 模型管理 / 定价运营 / 渠道 merged into 模型运营. The old paths stay as
            redirects because they are bookmarked; 渠道 lands on the 来源 tab. */}
        <Route
          path='/console/model-ops'
          element={
            <RootRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <ModelOpsPage />
              </Suspense>
            </RootRoute>
          }
        />
        {/* 来源 and 专属折扣 were tabs of 模型运营. They are separate objects with
            their own lifecycles, so they are separate routes, linked from the
            model ledger's header rather than sitting beside it. */}
        <Route
          path='/console/model-ops/sources'
          element={
            <RootRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <ModelOpsSourcesPage />
              </Suspense>
            </RootRoute>
          }
        />
        {/* 资料更新 is the 待处理 view of the model ledger now. */}
        <Route
          path='/console/model-ops/updates'
          element={<Navigate replace to='/console/model-ops?view=pending' />}
        />
        {/* 专属折扣 are customer prices: they moved from 模型运营 to sit beside
            组织. The old paths are bookmarked. */}
        <Route
          path='/console/price-plans'
          element={
            <RootRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <PricePlansPage />
              </Suspense>
            </RootRoute>
          }
        />
        <Route
          path='/console/price-plans/:id'
          element={
            <RootRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <PricePlanEditorPage />
              </Suspense>
            </RootRoute>
          }
        />
        <Route
          path='/console/model-ops/price-plans'
          element={<Navigate replace to='/console/price-plans' />}
        />
        <Route
          path='/console/model-ops/price-plans/:id'
          element={<PricePlanRedirect />}
        />
        <Route
          path='/console/model-ops/adjustments'
          element={<Navigate replace to='/console/price-plans' />}
        />
        <Route
          path='/console/models'
          element={
            <PrivateRoute
              allowedScopes={[
                'workspace_manager',
                'member',
                'key_pass',
                'platform',
              ]}
            >
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <ModelRoutesPage mode='console' />
              </Suspense>
            </PrivateRoute>
          }
        />
        {['/console/routes', '/console/model-market'].map((path) => (
          <Route
            key={path}
            path={path}
            element={
              <Navigate
                to={{
                  pathname: '/console/models',
                  search: location.search,
                  hash: location.hash,
                }}
                state={location.state}
                replace
              />
            }
          />
        ))}
        <Route
          path='/console/health'
          element={
            <PrivateRoute
              allowedScopes={[
                'workspace_manager',
                'member',
                'key_pass',
                'platform',
              ]}
            >
              <ModuleRoute
                section='workspace'
                module='channel_health'
                fallback='/console/models'
              >
                <Suspense
                  fallback={<Loading></Loading>}
                  key={location.pathname}
                >
                  <ChannelHealthPage />
                </Suspense>
              </ModuleRoute>
            </PrivateRoute>
          }
        />
        <Route
          path='/console/channel'
          element={<Navigate to='/console/model-ops/sources' replace />}
        />
        <Route
          path='/console/search-backend'
          element={
            <RootRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <SearchBackend />
              </Suspense>
            </RootRoute>
          }
        />
        <Route
          path='/console/token'
          element={
            <PrivateRoute>
              <TokenGate legacy={<Token />} />
            </PrivateRoute>
          }
        />
        <Route
          path='/keyusage'
          element={<Navigate to='/console/key' replace />}
        />
        {/* The old user-management page folded into 组织; bookmarks land there. */}
        <Route
          path='/console/user/*'
          element={<Navigate to='/console/organizations' replace />}
        />
        <Route
          path='/user/reset'
          element={
            <Suspense fallback={<Loading></Loading>} key={location.pathname}>
              <PasswordResetConfirm />
            </Suspense>
          }
        />
        <Route
          path='/auth/oidc/callback'
          element={
            <Suspense fallback={<Loading></Loading>} key={location.pathname}>
              <OIDCCallback />
            </Suspense>
          }
        />
        <Route
          path='/login'
          element={
            <Suspense fallback={<Loading></Loading>} key={location.pathname}>
              <LoginForm />
            </Suspense>
          }
        />
        {/* Password sign-in is not a separate destination any more: it is a
            credential you pick on the one door, not a place you navigate to.
            Both routes stay as redirects so existing links and bookmarks land
            somewhere useful instead of on the 404 page. */}
        <Route
          path='/login/password'
          element={
            <Navigate
              to={{ pathname: '/login', search: location.search }}
              replace
            />
          }
        />
        <Route
          path='/register'
          element={
            <Navigate
              to={{ pathname: '/login', search: location.search }}
              replace
            />
          }
        />
        <Route
          path='/reset'
          element={
            <Suspense fallback={<Loading></Loading>} key={location.pathname}>
              <PasswordResetForm />
            </Suspense>
          }
        />
        <Route
          path='/console/setting'
          element={
            <RootRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <Setting />
              </Suspense>
            </RootRoute>
          }
        />
        <Route
          path='/console/personal'
          element={
            <PrivateRoute
              allowedScopes={[
                'workspace_manager',
                'member',
                'key_pass',
                'platform',
              ]}
            >
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <PersonalSetting />
              </Suspense>
            </PrivateRoute>
          }
        />
        <Route
          path='/console/wallet'
          element={
            <PrivateRoute>
              <ModuleRoute
                section='account'
                module='wallet'
                fallback='/console'
              >
                <Suspense
                  fallback={<Loading></Loading>}
                  key={location.pathname}
                >
                  <WalletGate legacy={<Wallet />} />
                </Suspense>
              </ModuleRoute>
            </PrivateRoute>
          }
        />
        <Route
          path='/payment/success'
          element={
            <PrivateRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <PaymentReturn status='success' />
              </Suspense>
            </PrivateRoute>
          }
        />
        <Route
          path='/payment/document'
          element={
            <PrivateRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <PaymentDocument />
              </Suspense>
            </PrivateRoute>
          }
        />
        <Route
          path='/payment/fail'
          element={
            <PrivateRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <PaymentReturn status='fail' />
              </Suspense>
            </PrivateRoute>
          }
        />
        <Route
          path='/console/usage'
          element={
            <PrivateRoute
              allowedScopes={[
                'workspace_manager',
                'member',
                'key_pass',
                'platform',
              ]}
            >
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <Usage />
              </Suspense>
            </PrivateRoute>
          }
        />
        <Route
          path='/console/usage/sessions/:id'
          element={
            <PrivateRoute
              allowedScopes={[
                'workspace_manager',
                'member',
                'key_pass',
                'platform',
              ]}
            >
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <SessionDetail />
              </Suspense>
            </PrivateRoute>
          }
        />
        <Route path='/console/key' element={<RetiredKeyInspector />} />
        {/* An unmatched console address is still a console address: it keeps
            the sidebar and the console top bar, and must not also collect the
            public header from the marketing catch-all. */}
        <Route path='/console/*' element={<NotFound />} />
        <Route
          path='/console/ops'
          element={
            <RootRoute>
              <ModuleRoute
                section='admin'
                module='announcements'
                fallback='/console/setting'
              >
                <Suspense
                  fallback={<Loading></Loading>}
                  key={location.pathname}
                >
                  <OpsPage />
                </Suspense>
              </ModuleRoute>
            </RootRoute>
          }
        />
        <Route
          path='/console/ops/notices'
          element={
            <RootRoute>
              <ModuleRoute
                section='admin'
                module='announcements'
                fallback='/console/setting'
              >
                <Suspense
                  fallback={<Loading></Loading>}
                  key={location.pathname}
                >
                  <OpsPage />
                </Suspense>
              </ModuleRoute>
            </RootRoute>
          }
        />
        <Route
          path='/console/ops/mail'
          element={
            <RootRoute>
              <ModuleRoute
                section='admin'
                module='announcements'
                fallback='/console/setting'
              >
                <Suspense
                  fallback={<Loading></Loading>}
                  key={location.pathname}
                >
                  <OpsPage />
                </Suspense>
              </ModuleRoute>
            </RootRoute>
          }
        />
        <Route
          path='/console/ops/mail/digest'
          element={
            <RootRoute>
              <ModuleRoute
                section='admin'
                module='announcements'
                fallback='/console/setting'
              >
                <Suspense
                  fallback={<Loading></Loading>}
                  key={location.pathname}
                >
                  <OpsPage />
                </Suspense>
              </ModuleRoute>
            </RootRoute>
          }
        />
        <Route
          path='/console/ops/mail/:mailId'
          element={
            <RootRoute>
              <ModuleRoute
                section='admin'
                module='announcements'
                fallback='/console/setting'
              >
                <Suspense
                  fallback={<Loading></Loading>}
                  key={location.pathname}
                >
                  <OpsPage />
                </Suspense>
              </ModuleRoute>
            </RootRoute>
          }
        />
        {extensionOpsTabs()
          .flatMap((tab) => tab.routes)
          .map((path) => (
            <Route
              key={path}
              path={path}
              element={
                <RootRoute>
                  <ModuleRoute
                    section='admin'
                    module='announcements'
                    fallback='/console/setting'
                  >
                    <Suspense
                      fallback={<Loading></Loading>}
                      key={location.pathname}
                    >
                      <OpsPage />
                    </Suspense>
                  </ModuleRoute>
                </RootRoute>
              }
            />
          ))}
        {/* 公告 moved into 运营 (运营 › 公告). */}
        <Route
          path='/console/announcements'
          element={<Navigate to='/console/ops/notices' replace />}
        />
        <Route
          path='/console'
          element={
            <PrivateRoute
              allowedScopes={['workspace_manager', 'member', 'platform']}
            >
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <Dashboard />
              </Suspense>
            </PrivateRoute>
          }
        />
        {/* Team projects: manager-only pages of the active shared project.
            Members never get these entries; the platform customer page keeps
            its own route above. */}
        <Route
          path='/console/project/members'
          element={
            <PrivateRoute>
              <TeamRoute>
                <Suspense
                  fallback={<Loading></Loading>}
                  key={location.pathname}
                >
                  <ProjectMembersPage />
                </Suspense>
              </TeamRoute>
            </PrivateRoute>
          }
        />
        <Route
          path='/console/project/members/:userId'
          element={
            <PrivateRoute>
              <ProjectRoute need='manager'>
                <MemberDetailRedirect />
              </ProjectRoute>
            </PrivateRoute>
          }
        />
        <Route
          path='/console/project/settings'
          element={
            <PrivateRoute>
              <ProjectRoute need='owner'>
                <Suspense
                  fallback={<Loading></Loading>}
                  key={location.pathname}
                >
                  <ProjectSettingsPage />
                </Suspense>
              </ProjectRoute>
            </PrivateRoute>
          }
        />
        <Route
          path='/invite/:token'
          element={
            <Suspense fallback={<Loading></Loading>} key={location.pathname}>
              <InviteAcceptPage />
            </Suspense>
          }
        />
        <Route
          path='/console/organizations'
          element={
            <PrivateRoute allowedScopes={['platform']}>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <OrganizationsPage />
              </Suspense>
            </PrivateRoute>
          }
        />
        <Route
          path='/console/pricing'
          element={<Navigate to='/console/model-ops' replace />}
        />
        {/* The public catalog (/models, /models/<vendor>/<model>) and the
            pages extensions add are rendered by the API so crawlers read live prices; nginx
            sends those paths there before the SPA is ever served. */}
        {webExtensions().map((extension) => (
          <React.Fragment key={extension.name}>
            {extension.routes}
          </React.Fragment>
        ))}
        <Route
          path='/quickstart'
          element={
            <Suspense fallback={<Loading></Loading>} key={location.pathname}>
              <Quickstart />
            </Suspense>
          }
        />
        {/* Browser-side fallbacks for development/static hosts. Production
            nginx issues the permanent redirects before the SPA is served. */}
        <Route
          path='/user-agreement'
          element={<Navigate to='/terms' replace />}
        />
        <Route
          path='/privacy-policy'
          element={<Navigate to='/privacy' replace />}
        />
        {['zh', 'zh-TW', 'ja'].map((locale) => (
          <React.Fragment key={`legacy-legal-${locale}`}>
            <Route
              path={`/${locale}/user-agreement`}
              element={<Navigate to={`/${locale}/terms`} replace />}
            />
            <Route
              path={`/${locale}/privacy-policy`}
              element={<Navigate to={`/${locale}/privacy`} replace />}
            />
          </React.Fragment>
        ))}
      </Routes>
    </Suspense>
  );
}

export default App;
