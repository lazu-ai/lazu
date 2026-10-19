/** Team-project scope carried by the console scope and by deep links from
 *  a Key row or a rejection chip (`result=rejected&reason=<code>`). */
export type UsageProjectScope = {
  project_id?: string;
  reason?: string;
  result?: string;
  effort_clamped?: string;
  token_id?: string;
  /** A member deep link ("被拒 N 次" on a member row) scopes to one actor. */
  user_id?: string;
};

export const getUsageProjectScope = (
  search: string,
  activeProjectId?: string,
): UsageProjectScope => {
  const params = new URLSearchParams(search);
  const scope: UsageProjectScope = {};
  const projectId = params.get('project_id') || activeProjectId || '';
  if (projectId) scope.project_id = projectId;
  if (params.get('result') === 'rejected') scope.result = 'rejected';
  if (params.get('effort_clamped') === 'true') scope.effort_clamped = 'true';
  const reason = params.get('reason') || '';
  if (reason) scope.reason = reason;
  const tokenId = params.get('token_id') || '';
  if (tokenId) scope.token_id = tokenId;
  const userId = params.get('user_id') || '';
  if (userId) scope.user_id = userId;
  return scope;
};
