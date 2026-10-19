/* Platform roles from internal/common/constants.go. There are two: a customer
 * and 管理员. Anything at or above ROLE_ADMIN reaches the whole back office. */
export const ROLE_USER = 1;
export const ROLE_ADMIN = 100;

export const isAdminRole = (role: unknown): boolean => {
  const value =
    typeof role === 'number' || typeof role === 'string' ? Number(role) : NaN;
  return Number.isFinite(value) && value >= ROLE_ADMIN;
};
