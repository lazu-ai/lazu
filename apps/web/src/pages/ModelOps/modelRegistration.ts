import type { ModelInfoPrefill } from '../../hooks/models/useModelInfoQuery';

type LooseRecord = Record<string, unknown>;
const capabilityFingerprint = (value: unknown) =>
  JSON.stringify(
    Object.entries((value || {}) as Record<string, unknown>)
      .filter(([, enabled]) => enabled === true)
      .map(([key]) => key)
      .sort(),
  );

const PREFILL_FIELDS: Array<
  [
    string,
    (values: LooseRecord) => unknown,
    (prefill: ModelInfoPrefill) => unknown,
  ]
> = [
  ['maker', (v) => v.maker || '', (p) => p.maker || ''],
  ['release_date', (v) => v.release_date || '', (p) => p.release_date || ''],
  [
    'knowledge_cutoff',
    (v) => v.knowledge_cutoff || '',
    (p) => p.knowledge_cutoff || '',
  ],
  [
    'context_length',
    (v) => Number(v.context_window) || 0,
    (p) => p.context_length || 0,
  ],
  ['input_limit', (v) => Number(v.input_limit) || 0, (p) => p.input_limit || 0],
  [
    'max_output_tokens',
    (v) => Number(v.max_tokens) || 0,
    (p) => p.max_output_tokens || 0,
  ],
  [
    'official_price',
    (v) =>
      `${Number(v.official_input_per_mtok) || 0}/${Number(v.official_output_per_mtok) || 0}`,
    (p) =>
      `${p.official_input_per_mtok || 0}/${p.official_output_per_mtok || 0}`,
  ],
  [
    'modalities',
    (v) => JSON.stringify(v.modalities || {}),
    (p) => JSON.stringify(p.modalities || {}),
  ],
  [
    'capabilities',
    (v) => capabilityFingerprint(v.capabilities),
    (p) => capabilityFingerprint(p.capabilities),
  ],
  [
    'reasoning_options',
    (v) => JSON.stringify(v.reasoning_options || {}),
    (p) => JSON.stringify(p.reasoning_options || {}),
  ],
  [
    'unsupported_params',
    (v) => JSON.stringify(v.unsupported_params || []),
    (p) => JSON.stringify(p.unsupported_params || []),
  ],
  [
    'open_weights',
    (v) => Boolean(v.open_weights),
    (p) => Boolean(p.open_weights),
  ],
];

/** A new model's facts as the API takes them: official prices per token (the
 *  column's unit), and `prefilled_fields` naming the facts left exactly as
 *  models.dev filled them — the server marks those models.dev, the rest 手填. */
export function withModelInfo(
  values: LooseRecord,
  prefill?: ModelInfoPrefill,
  manualFormFields: ReadonlySet<string> = new Set(),
): LooseRecord {
  if (manualFormFields.has('open_source'))
    values = { ...values, open_weights: Boolean(values.open_source) };
  const aliases: Record<string, string> = {
    open_source: 'open_weights',
    context_window: 'context_length',
    max_tokens: 'max_output_tokens',
    official_input_per_mtok: 'official_price',
    official_output_per_mtok: 'official_price',
  };
  const manual = new Set(
    [...manualFormFields].map((key) => aliases[key] || key),
  );
  const next: LooseRecord = { ...values };
  if (
    prefill &&
    prefill.model_name.trim().toLowerCase() !==
      String(values.model_name).trim().toLowerCase()
  )
    prefill = undefined;
  const input = Number(values.official_input_per_mtok);
  const output = Number(values.official_output_per_mtok);
  if (Number.isFinite(input) && input > 0)
    next.official_input_price = input / 1_000_000;
  if (Number.isFinite(output) && output > 0)
    next.official_output_price = output / 1_000_000;
  delete next.official_input_per_mtok;
  delete next.official_output_per_mtok;
  next.release_date = String(values.release_date || '').trim();
  next.knowledge_cutoff = String(values.knowledge_cutoff || '').trim();
  next.manual_fields = prefill
    ? PREFILL_FIELDS.filter(
        ([field, fromValues, fromPrefill]) =>
          manual.has(field) || fromValues(values) !== fromPrefill(prefill),
      ).map(([field]) => field)
    : PREFILL_FIELDS.filter(([field]) => manual.has(field)).map(
        ([field]) => field,
      );
  next.prefilled_fields = prefill
    ? PREFILL_FIELDS.filter(([field, fromValues, fromPrefill]) => {
        const prefilled = fromPrefill(prefill);
        return (
          !manual.has(field) &&
          prefilled !== '' &&
          prefilled !== 0 &&
          fromValues(values) === prefilled
        );
      }).map(([field]) => field)
    : [];
  return next;
}
