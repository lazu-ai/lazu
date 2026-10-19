import { readStoredStatus } from '../../helpers/statusStorage';

export type StatusPayload =
  | (Record<string, unknown> & {
      HeaderNavModules?: string;
      SidebarModulePreset?: string;
      SidebarModulesAdmin?: string;
      announcements_enabled?: boolean;
      docs_link?: string;
      email_verification?: boolean;
      enable_stripe_topup?: boolean;
      pay_methods?: Array<{ name: string; status?: string; type: string }>;
      privacy_link?: string;
      privacy_policy_enabled?: boolean;
      server_address?: string;
      source_url?: string;
      stripe_min_topup?: number;
      terms_link?: string;
      user_agreement_enabled?: boolean;
    })
  | undefined;

export type StatusState = {
  status?: StatusPayload;
};

export type StatusAction =
  | {
      type: 'set';
      payload: StatusPayload;
    }
  | {
      type: 'unset';
    }
  | {
      type: string;
      payload?: StatusPayload;
    };

export const reducer = (
  state: StatusState,
  action: StatusAction,
): StatusState => {
  switch (action.type) {
    case 'set':
      return {
        ...state,
        status: action.payload,
      };
    case 'unset':
      return {
        ...state,
        status: undefined,
      };
    default:
      return state;
  }
};

const loadInitialStatus = (): StatusPayload => {
  return readStoredStatus() as StatusPayload;
};

export const initialState: StatusState = {
  status: loadInitialStatus(),
};
