import { API } from '../../../helpers/api';
import { unwrap } from '../../../lib/api-error';
import { queryKeys } from '../../../lib/query-keys';

export type MailStatus =
  | 'generating'
  | 'sent'
  | 'delivered'
  | 'delayed'
  | 'bounced'
  | 'complained'
  | 'failed'
  | 'skipped';

export type MailDelivery = {
  id: number;
  kind: string;
  user_id: number;
  username: string;
  receiver: string;
  subject: string;
  ref_key?: string | null;
  period: string;
  template: string;
  lang: string;
  timezone: string;
  status: MailStatus | string;
  provider_id: string;
  last_event: string;
  error: string;
  sensitive: boolean;
  has_body: boolean;
  created_at: number;
  generated_at: number;
  sent_at: number;
  event_at: number;
  checked_at: number;
};

export type MailDeliveryPage = {
  items: MailDelivery[];
  total: number;
  page: number;
  page_size: number;
};

export type MailDeliveryDetail = {
  mail: MailDelivery;
  html: string;
  snapshot: string;
};

export type DigestKind = 'weekly' | 'monthly';

type NextSend = { asia_shanghai: number; america_los_angeles: number };

export type DigestRun = {
  kind: string;
  period: string;
  status: string;
  reason: string;
  count: number;
  first_at: number;
};

export type DigestOverview = {
  enabled: boolean;
  candidates: number;
  weekly_off: number;
  monthly_off: number;
  next: { weekly: NextSend; monthly: NextSend };
  runs: DigestRun[] | null;
  settings: DigestSettings;
};

export type DigestSettings = {
  min_spend_usd: number;
  min_saving_usd: number;
  min_saving_pct: number;
};

export type MailListParams = {
  kind: string;
  status: string;
  q: string;
  period: string;
  p: number;
  page_size: number;
};

export const MAIL_PAGE_SIZE = 20;

export const mailKeys = {
  root: [...queryKeys.console.root, 'ops-mail'] as const,
  list: (params: MailListParams) => queryKeys.console.table('ops-mail', params),
  detail: (id: string) => queryKeys.console.detail('ops-mail', id),
  digest: [...queryKeys.console.root, 'ops-mail', 'digest'] as const,
};

export const mailApi = {
  list: async (params: MailListParams) =>
    unwrap<MailDeliveryPage>(
      await API.get('/api/ops/mail/deliveries', {
        params: Object.fromEntries(
          Object.entries(params).filter(([, value]) => value !== ''),
        ),
      }),
    ),
  get: async (id: string) =>
    unwrap<MailDeliveryDetail>(
      await API.get(`/api/ops/mail/deliveries/${encodeURIComponent(id)}`),
    ),
  resend: async (id: number) =>
    unwrap(await API.post(`/api/ops/mail/deliveries/${id}/resend`)),
  regenerate: async (id: number) =>
    unwrap(await API.post(`/api/ops/mail/deliveries/${id}/regenerate`)),
  copy: async (id: number) =>
    unwrap<{ receiver: string }>(
      await API.post(`/api/ops/mail/deliveries/${id}/copy`),
    ),
  digest: async () =>
    unwrap<DigestOverview>(await API.get('/api/ops/mail/digest')),
  setDigest: async (body: { enabled?: boolean; settings?: DigestSettings }) =>
    unwrap<{ enabled: boolean; settings: DigestSettings }>(
      await API.put('/api/ops/mail/digest', body),
    ),
  generateDrafts: async (kind: DigestKind) =>
    unwrap<{ drafts: number }>(
      await API.post('/api/ops/mail/digest/drafts', null, {
        params: { kind },
      }),
    ),
};
