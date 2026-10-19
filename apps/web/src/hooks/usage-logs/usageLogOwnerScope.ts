export type UsageLogOwnerScope = {
  key: string;
  /** Log type as the API numbers them; '0' is every type. */
  logType: string;
  model: string;
  userID: string;
  username: string;
};

/** The log types the filter offers. A deep link asking for anything else is
 *  ignored rather than sending an unknown value to the API. */
const LOG_TYPES = new Set(['0', '1', '2', '3', '4', '5']);

/** Parse the scope carried by deep links into this page: the display handle and
 *  immutable owner, the Key, the model, and the log type. Every caller that
 *  links here — the overview's failure notice, a model card, a Key row — writes
 *  one of these, so each one has to be read back here or the destination
 *  silently shows everything. */
export const getUsageLogOwnerScope = (search: string): UsageLogOwnerScope => {
  const params = new URLSearchParams(search);
  const logType = params.get('type') || '';
  return {
    key: params.get('key') || '',
    logType: LOG_TYPES.has(logType) ? logType : '',
    model: params.get('model') || params.get('model_name') || '',
    userID: params.get('user_id') || '',
    username: params.get('owner') || '',
  };
};
