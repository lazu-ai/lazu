import type { ComponentType } from 'react';

// Unknown addresses are answered with the prerendered 404 page. Its URL is
// not the route it was prerendered for, so the client renders it afresh
// rather than hydrating — and React.lazy suspends on that first render even
// when the chunk is already in memory, committing an empty fallback that
// removed the page (footer included) for a frame: a layout shift of ~0.1.
// Loading the chunk first and handing React.lazy a thenable that resolves
// synchronously lets the first render be the finished page.

type NotFoundModule = { default: ComponentType };

let loaded: NotFoundModule | null = null;

const load = () =>
  import('../pages/NotFound').then((module) => {
    loaded = module as NotFoundModule;
    return loaded;
  });

export const preloadNotFound = () => load();

export const notFoundLoader = (): Promise<NotFoundModule> => {
  const ready = loaded;
  if (!ready) return load();
  const thenable = {
    then(onFulfilled?: (value: NotFoundModule) => unknown) {
      onFulfilled?.(ready);
    },
  };
  return thenable as unknown as Promise<NotFoundModule>;
};
