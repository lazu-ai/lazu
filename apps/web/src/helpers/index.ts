/* Explicit re-exports keep each public name's source visible. */
export { ModuleRoute, PrivateRoute, RootRoute, authHeader } from './auth';
export {
  downloadTextAsFile,
  formatDecimalValue,
  formatMessageForAPI,
  getLogo,
  getSystemName,
  getUserFromLocalStorage,
  getUserIdFromLocalStorage,
  isRoot,
  isValidMessage,
  safeJsonParse,
  showError,
  showInfo,
  showNotice,
  showSuccess,
  showWarning,
  timestamp2string1,
} from './utils';
export { copy } from './clipboard';
export { API, postSSE, updateAPI } from './api';
export { getMicroUSDPerUSD, microUSDToUSD, usdToMicroUSD } from './render';
export {
  rehypeSplitWordsIntoSpans,
  type StreamingWordSplitOptions,
} from './rehype-streaming';
export {
  absMicroUSD,
  addMicroUSD,
  asMicroUSD,
  formatMoney,
  negateMicroUSD,
  type MicroUSD,
} from './money';
export { getLogOther } from './log';
export { setStatusData, setUserData } from './data';
export { toBoolean } from './boolean';
export { sanitizeMarkup } from './sanitize';
export {
  getPrimaryModelPrice,
  hasConfiguredModelPrice,
  inferModelOwner,
  resolveModelBrand,
  type ModelPricingRecord,
} from './modelPricing';
export { resolveGatewayHref, type GatewayScopeKind } from './gatewayNavigation';
export { ROLE_ADMIN, ROLE_USER, isAdminRole } from './roles';
