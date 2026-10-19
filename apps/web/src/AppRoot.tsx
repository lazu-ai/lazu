import PageLayout from './components/layout/PageLayout';
import { ConsoleScopeProvider } from './context/ConsoleScope';
import { PrerenderProvider } from './context/Prerender';
import { StatusProvider } from './context/Status';
import { ThemeProvider } from './context/Theme';
import { UserProvider } from './context/User';
import { ProjectProvider } from './features/projects/ProjectContext';
import { AppToaster, AppQueryProvider } from './lib/query-client';
import type { ReactNode } from 'react';

export const AppProviders = ({
  children,
  prerenderRoute,
}: {
  children: ReactNode;
  prerenderRoute: string | null;
}) => (
  <AppQueryProvider>
    <StatusProvider>
      <UserProvider>
        <ConsoleScopeProvider>
          <ProjectProvider>
            <PrerenderProvider route={prerenderRoute}>
              {children}
            </PrerenderProvider>
          </ProjectProvider>
        </ConsoleScopeProvider>
      </UserProvider>
    </StatusProvider>
  </AppQueryProvider>
);

export const AppFrame = () => (
  <ThemeProvider>
    <PageLayout />
    <AppToaster />
  </ThemeProvider>
);
