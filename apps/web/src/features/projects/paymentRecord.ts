import { queryOptions } from '@tanstack/react-query';
import { API } from '../../helpers/api';
import { unwrap } from '../../lib/api-error';

export type TopupRecordData = {
  id: string;
  project_id: string;
  project_name: string;
  project_shared?: boolean;
  paid_at: number;
  created_at: number;
  paid_amount_microusd: number;
  order_amount_microusd: number;
  credited_microusd: number;
  bonus_microusd?: number;
  payment_status: string;
  payment_method: string;
  invoice_status: string;
  invoice_number: string;
  invoice_ready: boolean;
  receipt_ready: boolean;
  bill_to?: {
    name?: string | null;
    address?: {
      city?: string | null;
      state?: string | null;
      country?: string | null;
    } | null;
    tax_ids?: Array<{ value?: string | null }> | null;
  } | null;
};

export function accountPaymentQueryOptions(topupId: string) {
  return queryOptions({
    queryKey: ['account-payment', topupId],
    queryFn: async (): Promise<TopupRecordData> => {
      // A failure rejects with its status; the record view tells a missing
      // payment (404) from one it cannot reach yet.
      return unwrap<TopupRecordData>(
        await API.get(`/api/user/payments/${encodeURIComponent(topupId)}`, {
          skipErrorHandler: true,
        }),
      );
    },
    retry: false,
    staleTime: 3_000,
  });
}
