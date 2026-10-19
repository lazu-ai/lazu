import {
  createContext,
  startTransition,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from 'react';

type PrerenderState = {
  route: string | null;
};

const PrerenderContext = createContext<PrerenderState>({ route: null });

export const PrerenderProvider = ({
  children,
  route,
}: {
  children: ReactNode;
  route: string | null;
}) => {
  const [activeRoute, setActiveRoute] = useState(route);

  // The route marker suppresses network work during renderToString and the
  // first hydration pass. Once React owns the browser tree, clear it so live
  // marketing facts can load instead of remaining in their SSG placeholders.
  useEffect(() => {
    if (activeRoute) {
      // Let still-hydrating Suspense boundaries finish before enabling live data.
      startTransition(() => setActiveRoute(null));
    }
  }, [activeRoute]);

  return (
    <PrerenderContext.Provider value={{ route: activeRoute }}>
      {children}
    </PrerenderContext.Provider>
  );
};

export const usePrerender = () => useContext(PrerenderContext);
