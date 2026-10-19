type QueryKeyObject = Record<string, unknown>;
type QueryKeyPart =
  | string
  | number
  | boolean
  | null
  | undefined
  | QueryKeyObject
  | readonly unknown[];

export const queryKeys = {
  status: ['status'] as const,
  user: ['user'] as const,
  dashboard: {
    root: ['dashboard'] as const,
    summary: () => [...queryKeys.dashboard.root, 'summary'] as const,
  },
  console: {
    root: ['console'] as const,
    table: (resource: string, params: QueryKeyObject = {}) =>
      [...queryKeys.console.root, resource, params] as const,
    detail: (resource: string, id: QueryKeyPart) =>
      [...queryKeys.console.root, resource, id] as const,
  },
};
