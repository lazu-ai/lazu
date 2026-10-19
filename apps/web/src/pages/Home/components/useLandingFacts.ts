/* Live numbers for the landing page.
 *
 * Everything shown as a figure on the landing comes from here, and everything
 * here comes from a public endpoint. Nothing is hard-coded: if a call fails, or
 * the page is being prerendered at build time with no backend behind it, the
 * figure is simply absent rather than replaced with something plausible.
 *
 * Sources:
 *   GET /api/pricing        — the published model catalogue and its vendors
 *   GET /api/public/status  — per-family health, used for "available right now"
 *
 * Deliberately not shown: a 90-day uptime figure. The availability the backend
 * reports is observed over the "runtime" window (how many channels are up for a
 * model at this moment, see loadCatalogHealthByModel) — there is no 90-day
 * aggregate to quote, so the landing does not quote one.
 */
import { useQuery } from '@tanstack/react-query';
import { usePrerender } from '../../../context/Prerender';
import { API } from '../../../helpers/api';
import { unwrap } from '../../../lib/api-error';

export type LandingModel = {
  input?: number;
  name: string;
  output?: number;
  vendor: string;
};

/** loading — still asking. ready — we have the catalogue.
 *  unavailable — the call finished and there is nothing to show, either
 *  because it failed or because no model is published yet. The three need
 *  different copy: "loading" left on screen forever is a lie. */
export type LandingFactsState = 'loading' | 'ready' | 'unavailable';

export type LandingFacts = {
  /** Share of catalogue models reporting healthy right now, 0–100. */
  availablePct?: number;
  /** Outcome of the health call — 'unavailable' means it finished with
   *  nothing to show, so the metric should be dropped, not left "loading". */
  availabilityState: LandingFactsState;
  models: LandingModel[];
  modelCount?: number;
  state: LandingFactsState;
  vendorCount?: number;
};

type PricingRow = {
  has_price?: boolean;
  input_per_mtok?: number | null;
  model_name?: string;
  output_per_mtok?: number | null;
  vendor_id?: string;
};

type Vendor = { id?: string; name?: string };

type ModelFamily = {
  available_count?: number;
  model_count?: number;
};

const asArray = <T>(value: unknown): T[] => (Array.isArray(value) ? value : []);

/** One representative model per vendor, biggest catalogues first.
 *
 * Picking "the first four rows" would be arbitrary; picking by price would be
 * a claim about which models matter. One model from each of the largest
 * vendors is both stable and explainable. */
const pickRepresentative = (
  rows: PricingRow[],
  vendorNames: Map<string, string>,
  limit: number,
): LandingModel[] => {
  const byVendor = new Map<string, PricingRow[]>();
  rows.forEach((row) => {
    if (!row.model_name || row.has_price === false) return;
    const vendor = row.vendor_id || '';
    if (!vendor) return;
    const list = byVendor.get(vendor);
    if (list) list.push(row);
    else byVendor.set(vendor, [row]);
  });

  return [...byVendor.entries()]
    .sort((a, b) => b[1].length - a[1].length || a[0].localeCompare(b[0]))
    .slice(0, limit)
    .map(([vendor, list]) => {
      // Within a vendor, the priciest model is the flagship; stable enough to
      // avoid the list reshuffling between loads.
      const flagship = [...list].sort(
        (a, b) => (b.output_per_mtok ?? 0) - (a.output_per_mtok ?? 0),
      )[0];
      return {
        input: flagship.input_per_mtok ?? undefined,
        name: flagship.model_name as string,
        output: flagship.output_per_mtok ?? undefined,
        vendor: vendorNames.get(vendor) || vendor,
      };
    });
};

export const useLandingFacts = (modelLimit = 4): LandingFacts => {
  const { route: prerenderRoute } = usePrerender();
  // During the build-time render there is no backend to ask.
  const enabled = !prerenderRoute && typeof window !== 'undefined';

  const pricing = useQuery({
    enabled,
    queryKey: ['landing-pricing'],
    retry: false,
    staleTime: 5 * 60 * 1000,
    queryFn: async () => {
      const response = await API.get('/api/pricing', {
        skipErrorHandler: true,
      } as never);
      const rows = asArray<PricingRow>(unwrap<unknown>(response));
      const vendors = asArray<Vendor>(response.data?.vendors);
      const vendorNames = new Map(
        vendors
          .filter((vendor) => vendor.id)
          .map((vendor) => [vendor.id as string, vendor.name || vendor.id]),
      );
      // /api/pricing deliberately keeps rows with has_price === false so
      // operators can spot unpriced models — but the landing advertises
      // "models available", and a model nobody can be billed for is not
      // available. Count only priced rows (pickRepresentative already does).
      const priced = rows.filter((row) => row.has_price !== false);
      const usedVendors = new Set(
        priced.map((row) => row.vendor_id).filter(Boolean),
      );
      return {
        models: pickRepresentative(rows, vendorNames as never, modelLimit),
        modelCount: priced.length,
        vendorCount: usedVendors.size || vendors.length,
      };
    },
  });

  const status = useQuery({
    enabled,
    queryKey: ['landing-public-status'],
    retry: false,
    staleTime: 5 * 60 * 1000,
    queryFn: async () => {
      const response = await API.get('/api/public/status', {
        skipErrorHandler: true,
      } as never);
      const families = asArray<ModelFamily>(
        response.data?.data?.model_families,
      );
      const total = families.reduce(
        (sum, family) => sum + (family.model_count ?? 0),
        0,
      );
      const available = families.reduce(
        (sum, family) => sum + (family.available_count ?? 0),
        0,
      );
      return total > 0
        ? Math.round((available / total) * 1000) / 10
        : undefined;
    },
  });

  const models = pricing.data?.models ?? [];
  const state: LandingFactsState = pricing.isError
    ? 'unavailable'
    : pricing.isSuccess
      ? models.length
        ? 'ready'
        : 'unavailable'
      : 'loading';

  const availabilityState: LandingFactsState = status.isError
    ? 'unavailable'
    : status.isSuccess
      ? status.data === undefined
        ? 'unavailable'
        : 'ready'
      : 'loading';

  return {
    availablePct: status.data,
    availabilityState,
    models,
    modelCount: pricing.data?.modelCount,
    state,
    vendorCount: pricing.data?.vendorCount,
  };
};
