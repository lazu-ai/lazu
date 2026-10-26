import { Copy, Zap } from 'lucide-react';
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { isLoopbackOrigin, isUsableOrigin } from './baseURL';
import { buildDeepLink, describeDeepLink } from './deepLinks';
import { LaneControl, LaneTable } from './LaneControl';
import {
  buildPicker,
  defaultAliasModels,
  defaultExtraModels,
  defaultLightModel,
  defaultModel,
  findPickerModel,
  laneOffer,
  toolEndpointLabel,
  priceLabel,
  type PickerModel,
} from './modelPicker';
import { ModelMultiField, ModelPickerField } from './ModelPickerField';
import { buildSnippet, snippetHidesKey } from './snippets';
import { ToolMono } from './ToolMono';
import {
  CONNECT_GROUPS,
  CONNECT_TOOLS,
  DEFAULT_CONNECT_TOOL,
  defaultFormat,
  findConnectTool,
  modelFamily,
  type ConnectFormat,
  type ConnectTool,
  type ConnectToolId,
} from './tools';
import {
  Button,
  InlineNotice,
  ModalFrame,
  ModalTitle,
  ModalDescription,
  Select,
  SelectItem,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from '../../../components/ui';
import { notify } from '../../../components/ui/toast';
import { copy, showError } from '../../../helpers/utils';
import { errorMessage } from '../../../lib/api-error';
import { cn } from '../../../lib/cn';
import { isLegacyKey, useTokenLaneVendors } from '../token-support';
import { tokenStatusCode } from '../tokenStatusCode';
import type {
  TokenRecord,
  useTokensQuery,
} from '../../../hooks/tokens/useTokensQuery';

export type ConnectClient = Pick<
  ReturnType<typeof useTokensQuery>,
  't' | 'modelOptions' | 'vendorOptions' | 'revealKey' | 'updateTokenConnect'
>;
type Lane = 'direct' | 'cheap';

const KEY_PLACEHOLDER = 'YOUR_LAZU_KEY';
const lastToolStorageKey = (id: unknown) => `token-connect:${String(id)}`;

const readLastTool = (id: unknown): ConnectTool | undefined => {
  try {
    return findConnectTool(localStorage.getItem(lastToolStorageKey(id)));
  } catch {
    return undefined;
  }
};

const rememberTool = (id: unknown, tool: ConnectToolId) => {
  try {
    localStorage.setItem(lastToolStorageKey(id), tool);
  } catch {
    // Remembering the last tool is a convenience only.
  }
};

/** The Lazu origin agents should call: the configured server address, or
 *  the page's own origin when none is published. */
export const connectBaseURL = () => {
  let serverAddress = '';
  try {
    const status = JSON.parse(localStorage.getItem('status') || '{}') as {
      server_address?: string;
    };
    serverAddress = String(status.server_address || '');
  } catch {
    serverAddress = '';
  }
  // config.yaml ships `server_address: http://localhost:3000`, so a
  // deployment that never set its own still reports one. Handing that to a
  // user would point their agent at port 3000 on their own machine, so it is
  // only believed when this console is itself on loopback.
  // Beyond the localhost default, an address that is not a plain http(s)
  // origin is refused outright: this value ends up inside a command the user
  // pastes into a terminal.
  const usable =
    serverAddress &&
    isUsableOrigin(serverAddress) &&
    (!isLoopbackOrigin(serverAddress) ||
      isLoopbackOrigin(window.location.origin))
      ? serverAddress
      : window.location.origin;
  return usable.trim().replace(/\/+$/, '').replace(/\/v1$/, '');
};

const recordModels = (record: TokenRecord | null): string[] => {
  if (!record?.model_limits_enabled) return [];
  const raw = record.model_limits;
  const list = Array.isArray(raw) ? raw : String(raw || '').split(',');
  return list.map((item) => String(item).trim()).filter(Boolean);
};

const FORMAT_LABELS: Record<ConnectFormat, string> = {
  script: 'page.console.token.connect.format.script',
  powershell: 'page.console.token.connect.format.powershell',
  env: 'page.console.token.connect.format.env',
  settings: 'settings.json',
  toml: 'config.toml',
  dotenv: '.env',
  json: 'page.console.token.connect.format.json',
  steps: 'page.console.token.connect.format.steps',
  link: 'page.console.token.connect.format.link',
  python: 'Python',
  typescript: 'TypeScript',
  curl: 'curl',
};

export function ConnectModal({
  initialSecret,
  secretExpiresAt,
  notice,
  initialTool,
  onEditKey,
  onOpenChange,
  open,
  record,
  tokens,
}: {
  initialSecret?: string;
  secretExpiresAt?: number;
  notice?: ReactNode;
  initialTool?: ConnectToolId;
  /** Opens the Key's own editor, where the per-vendor defaults live. The
   *  dialog writes single models; the vendor fallback behind them is edited
   *  there. */
  onEditKey?: (record: TokenRecord) => void;
  onOpenChange: (open: boolean) => void;
  open: boolean;
  record: TokenRecord | null;
  tokens: ConnectClient;
}) {
  const { t } = tokens;
  const [toolId, setToolId] = useState<ConnectToolId>(DEFAULT_CONNECT_TOOL.id);
  const [format, setFormat] = useState<ConnectFormat>('script');
  const [model, setModel] = useState('');
  const [light, setLight] = useState('');
  // True once the user has chosen a light model by hand; the automatic choice
  // stops following the main model from then on.
  const [lightPinned, setLightPinned] = useState(false);
  // Claude Code's /model opus and /model sonnet slots, and the extra models a
  // list tool imports. Each follows the main model until picked by hand.
  const [opus, setOpus] = useState('');
  const [sonnet, setSonnet] = useState('');
  const [opusPinned, setOpusPinned] = useState(false);
  const [sonnetPinned, setSonnetPinned] = useState(false);
  const [extras, setExtras] = useState<string[]>([]);
  const [extrasPinned, setExtrasPinned] = useState(false);
  // One lane per model, not one per Key. An entry exists only where the user
  // moved a model off its vendor default; everything else reads through.
  const [modelLanes, setModelLanes] = useState<Record<string, Lane>>({});
  const [secret, setSecret] = useState('');
  const [secretExpired, setSecretExpired] = useState(false);
  const expiryRef = useRef(secretExpiresAt);
  expiryRef.current = secretExpiresAt;
  const [secretError, setSecretError] = useState('');
  const [revealAttempt, setRevealAttempt] = useState(0);
  const [revealing, setRevealing] = useState(false);
  const [copied, setCopied] = useState(false);
  const [keyCopied, setKeyCopied] = useState(false);
  const [saving, setSaving] = useState(false);
  const [importRequested, setImportRequested] = useState(false);
  const savedConnection = useRef<Pick<
    TokenRecord,
    'model_lanes' | 'product' | 'vendor_groups' | 'version'
  > | null>(null);

  useEffect(() => {
    savedConnection.current = null;
  }, [open, record?.id]);

  useEffect(() => {
    setImportRequested(false);
  }, [
    open,
    record?.id,
    toolId,
    model,
    light,
    opus,
    sonnet,
    extras,
    modelLanes,
  ]);

  useEffect(() => {
    if (!open) {
      setLightPinned(false);
      setOpusPinned(false);
      setSonnetPinned(false);
      setExtrasPinned(false);
    }
  }, [open]);

  const tool = findConnectTool(toolId) ?? DEFAULT_CONNECT_TOOL;
  const legacy = record ? isLegacyKey(record) : false;
  const canReveal =
    Boolean(record) && !legacy && record?.can_reveal_key !== false;
  const capabilities = (record?.capabilities || {}) as { can_update?: boolean };
  const canUpdate = capabilities.can_update !== false;
  const usable = record ? tokenStatusCode(record) === 1 : true;
  const scopedModels = useMemo(() => recordModels(record), [record]);
  const modelFacts = tokens.vendorOptions.models;
  // Vendors this Key refuses. A model owned by one of them would be turned
  // away at the gateway, so it is not on offer here either.
  const blockedVendors = useMemo(() => {
    if (!record?.vendor_limits_enabled) return new Set<string>();
    const limits = Array.isArray(record.vendor_limits)
      ? record.vendor_limits
      : [];
    return new Set(limits.map(String));
  }, [record?.vendor_limits_enabled, record?.vendor_limits]);
  const laneVendors = useTokenLaneVendors(tokens.vendorOptions.vendors);
  const vendorNames = useMemo(
    () =>
      new Map(
        tokens.vendorOptions.vendors.map((vendor) => [
          String(vendor.id),
          String(vendor.name || ''),
        ]),
      ),
    [tokens.vendorOptions.vendors],
  );
  // The offer comes from the pricing catalog, grouped by vendor and newest
  // first. A Key's model scope is a permission rather than a catalog, so it
  // narrows the offer; it never adds a model the catalog no longer describes.
  const picker = useMemo(
    () =>
      buildPicker({
        allowed: scopedModels,
        blockedVendors,
        channelModels: tokens.modelOptions.map((option) =>
          String(option.value),
        ),
        facts: modelFacts,
        toolId: tool.id,
        vendorNames,
      }),
    [
      blockedVendors,
      modelFacts,
      scopedModels,
      tokens.modelOptions,
      tool.id,
      vendorNames,
    ],
  );
  const modelOptions = picker.all;
  // The catalog knows which vendor a model belongs to even when its id says
  // nothing (`sonnet-4`); the name heuristic is only the fallback.
  const familyOfModel = useMemo(() => {
    const byVendorID = new Map(
      laneVendors.map((vendor) => [String(vendor.id), vendor.laneFamily]),
    );
    const byModel = new Map(
      modelFacts.map((fact) => [
        fact.model,
        (fact.vendorID && byVendorID.get(fact.vendorID)) || undefined,
      ]),
    );
    return (id: string) => byModel.get(id) ?? modelFamily(id);
  }, [laneVendors, modelFacts]);
  const vendorGroups = (record?.vendor_groups || {}) as Record<string, Lane>;
  const storedModelLanes = (record?.model_lanes || {}) as Record<string, Lane>;

  // The Key's fallback for a model nobody pinned: its vendor's lane.
  const vendorLaneOf = (id: string): Lane => {
    const vendor = laneVendors.find(
      (entry) => entry.laneFamily === familyOfModel(id),
    );
    return vendor && vendorGroups[String(vendor.id)] === 'cheap'
      ? 'cheap'
      : 'direct';
  };
  const laneOf = (id: string): Lane =>
    laneOffer(findPickerModel(picker, id), tool.id).kind === 'agent-lane-only'
      ? 'cheap'
      : (modelLanes[id] ?? vendorLaneOf(id));
  const setLaneOf = (id: string, next: Lane) =>
    setModelLanes((current) => ({ ...current, [id]: next }));
  const offerFor = (id: string) =>
    laneOffer(findPickerModel(picker, id), tool.id);

  // Every price in the dialog is the price of the lane that model will take.
  const priceForModel = (candidate: PickerModel): string =>
    priceLabel(candidate, laneOf(candidate.id));

  const cheapVendorNames = useMemo(
    () =>
      laneVendors
        .filter((vendor) => vendorGroups[String(vendor.id)] === 'cheap')
        .map((vendor) => vendor.laneLabel),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [laneVendors, record?.vendor_groups],
  );

  // Every model this dialog hands the tool. Their lanes are what it saves,
  // and what the summary line counts.
  const writtenModels = useMemo(() => {
    if (!tool.usesModel) return [] as string[];
    const ids = [model];
    if (tool.extraModels === 'aliases') ids.push(opus, sonnet);
    if (tool.lightSlot) ids.push(light);
    if (tool.extraModels === 'list') ids.push(...extras);
    return ids.filter((id, index) => Boolean(id) && ids.indexOf(id) === index);
  }, [
    extras,
    light,
    model,
    opus,
    sonnet,
    tool.extraModels,
    tool.lightSlot,
    tool.usesModel,
  ]);

  const vendorLabelOf = (id: string): string =>
    findPickerModel(picker, id)?.vendorLabel || '';

  // The rows the extras list shows even when unticked: what this tool was
  // seeded with, so the list does not shrink as models are turned off.
  const extraCandidates = useMemo(
    () =>
      tool.extraModels === 'list'
        ? defaultExtraModels(picker, model, light)
        : [],
    [light, model, picker, tool.extraModels],
  );

  // A chat app chooses its own model, so its lanes are listed per model on
  // the Key instead — and only where there is a choice to make.
  const laneTableModels = useMemo(() => {
    if (tool.usesModel) return [] as PickerModel[];
    const rows: PickerModel[] = [];
    modelOptions.forEach((id) => {
      const candidate = findPickerModel(picker, id);
      if (candidate && laneOffer(candidate, tool.id).kind !== 'direct-only') {
        rows.push(candidate);
      }
    });
    return rows;
  }, [modelOptions, picker, tool.id, tool.usesModel]);

  /** What this import will do, in one line: how many models it writes and on
   *  which lanes, then the vendor defaults everything else still follows. */
  const laneSummary = (
    <div className='tok-connect__summary'>
      <span>
        {writtenModels.length
          ? t('page.console.token.connect.laneSummary', {
              agent: writtenModels.filter((id) => laneOf(id) === 'cheap')
                .length,
              count: writtenModels.length,
              defaultValue:
                '本次写入 {{count}} 个模型 · Agent {{agent}} · 稳定 {{direct}}。',
              direct: writtenModels.filter((id) => laneOf(id) === 'direct')
                .length,
            })
          : null}{' '}
        {cheapVendorNames.length
          ? t('page.console.token.connect.laneDefaults', {
              defaultValue:
                '未列出的模型按厂商默认线路：{{vendors}} 走 Agent 专线、其余稳定。',
              vendors: cheapVendorNames.join('、'),
            })
          : t('page.console.token.connect.laneDefaultsAllDirect', {
              defaultValue: '未列出的模型按厂商默认线路：全部稳定。',
            })}
      </span>
      {onEditKey && record ? (
        <button
          className='tok-connect__summary-link'
          data-owns-chrome='true'
          onClick={() => {
            onOpenChange(false);
            onEditKey(record);
          }}
          type='button'
        >
          {t('page.console.token.connect.laneEditDefaults', {
            defaultValue: '改厂商默认 →',
          })}
        </button>
      ) : null}
    </div>
  );

  /** The lane control under one model field, or nothing when the model has
   *  no lane to choose and nothing to explain. */
  const laneSlotFor = (id: string, labelledBy: string): ReactNode => {
    if (!id) return null;
    return (
      <LaneControl
        disabled={!canUpdate}
        labelledBy={labelledBy}
        offer={offerFor(id)}
        onChange={(next) => setLaneOf(id, next)}
        t={t}
        value={laneOf(id)}
      />
    );
  };

  // Choose the tool once per opening: explicit request, then the Key's own
  // badge, then the last tool used with this Key.
  useEffect(() => {
    if (!open || !record) return;
    const next =
      findConnectTool(initialTool) ??
      findConnectTool(record.product) ??
      readLastTool(record.id) ??
      DEFAULT_CONNECT_TOOL;
    setToolId(next.id);
    setCopied(false);
    const expired =
      expiryRef.current !== undefined && expiryRef.current <= Date.now();
    setSecretExpired(expired);
    setSecret(expired ? '' : initialSecret || '');
    setSecretError('');
  }, [initialSecret, initialTool, open, record]);

  // Read the full value only while the dialog is open; it never leaves this
  // component's state.
  useEffect(() => {
    if (
      !open ||
      !record ||
      initialSecret ||
      !canReveal ||
      record.id === undefined
    ) {
      return;
    }
    let cancelled = false;
    setRevealing(true);
    setSecretError('');
    tokens
      .revealKey(record.id)
      .then((value) => {
        if (
          !cancelled &&
          (expiryRef.current === undefined || expiryRef.current > Date.now())
        )
          setSecret(value);
      })
      .catch((error: unknown) => {
        if (!cancelled) setSecretError(errorMessage(error));
      })
      .finally(() => {
        if (!cancelled) setRevealing(false);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, record?.id, initialSecret, canReveal, revealAttempt]);

  useEffect(() => {
    if (!open) {
      setSecret('');
    }
  }, [open]);

  useEffect(() => {
    if (!open || secretExpiresAt === undefined) return;
    const timer = window.setTimeout(
      () => {
        setSecret('');
        setSecretExpired(true);
        setKeyCopied(false);
        setCopied(false);
      },
      Math.max(0, secretExpiresAt - Date.now()),
    );
    return () => window.clearTimeout(timer);
  }, [open, secretExpiresAt]);

  // Tool change resets format and model defaults, and hands the light model
  // back to the automatic choice.
  useEffect(() => {
    if (!open) return;
    setFormat(defaultFormat(tool, navigator.userAgent));
    setModel(defaultModel(picker));
    setLightPinned(false);
    setOpusPinned(false);
    setSonnetPinned(false);
    setExtrasPinned(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, toolId, modelOptions.join(',')]);

  // The opus and sonnet slots open on the main model's vendor's flagship and
  // balanced models, so /model opus in Claude Code lands on the same vendor
  // the agent already talks to. Each slot the user has set stays put on its
  // own; the other keeps following the main model.
  useEffect(() => {
    if (!open || tool.extraModels !== 'aliases') {
      setOpus('');
      setSonnet('');
      return;
    }
    const slots = defaultAliasModels(picker, model);
    if (!opusPinned) setOpus(slots.opus);
    if (!sonnetPinned) setSonnet(slots.sonnet);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    open,
    model,
    opusPinned,
    sonnetPinned,
    tool.extraModels,
    modelOptions.join(','),
  ]);

  // A list tool opens with the main model's vendor beside the two defaults,
  // so the app's own switcher has the same short list the dialog shows. Once
  // the user edits the list it is theirs.
  useEffect(() => {
    if (!open || tool.extraModels !== 'list') {
      setExtras([]);
      return;
    }
    if (extrasPinned) return;
    setExtras(defaultExtraModels(picker, model, light));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    open,
    model,
    light,
    extrasPinned,
    tool.extraModels,
    modelOptions.join(','),
  ]);

  // The light model opens on one from the main model's own vendor, so a
  // Claude agent's side tasks stay on Claude by default. Once the user picks
  // one themselves it stays put — both fields are theirs to set.
  useEffect(() => {
    if (!open || !tool.lightSlot) {
      setLight('');
      return;
    }
    if (lightPinned) return;
    setLight(defaultLightModel(picker, model));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, model, lightPinned, tool.lightSlot, modelOptions.join(',')]);

  useEffect(() => {
    setModelLanes(storedModelLanes);
    // The Key's own map is the whole starting state; a model absent from it
    // reads its vendor default through laneOf.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, record?.id]);

  useEffect(() => {
    if (!copied) return undefined;
    const timer = window.setTimeout(() => setCopied(false), 1500);
    return () => window.clearTimeout(timer);
  }, [copied]);

  useEffect(() => {
    if (!keyCopied) return undefined;
    const timer = window.setTimeout(() => setKeyCopied(false), 1500);
    return () => window.clearTimeout(timer);
  }, [keyCopied]);

  // Two ways a tool ends up unusable: it needs a model id and has none
  // (Codex, OpenClaw — their installer exits on the spot), or the catalog
  // holds nothing its protocol can call, in which case even a tool that
  // normally leaves the model to the agent has nowhere to send a request.
  const modelMissing =
    modelOptions.length === 0 || (tool.requiresModel && !model);
  // Blocks the user runs carry no Key — the installer asks for it, the rc
  // line shows a placeholder — so the Key gets its own button instead.
  const promptsForKey = snippetHidesKey(tool, format);
  const asksAtPrompt = format === 'script' || format === 'powershell';
  // A Key whose value this member may not read still has a usable dialog.
  // What needs the secret in hand is the import link, which carries it, and
  // the copy-the-Key button; a block that shows a placeholder — because this
  // format never carries the Key, or because nobody here can read it — is
  // worth copying as it stands.
  const snippetCopyable =
    (promptsForKey || Boolean(secret) || !canReveal || secretExpired) &&
    !modelMissing;
  const baseURL = connectBaseURL();
  const key = secret || KEY_PLACEHOLDER;
  const deepLink = tool.quick
    ? buildDeepLink(tool, {
        baseURL,
        homepage: window.location.origin,
        key,
        light,
        model,
        opus,
        sonnet,
      })
    : '';
  const snippet =
    format === 'link'
      ? describeDeepLink(deepLink)
      : buildSnippet(
          tool,
          format,
          { baseURL, extras, key, light, model, opus, sonnet },
          t,
        );
  // CC Switch's import takes one model for these apps, so a list longer than
  // that only reaches the app through the script or the config file.
  const extrasSkipImport =
    tool.quick === 'ccswitch' &&
    tool.extraModels === 'list' &&
    extras.length > 0;

  const quickLabel =
    tool.quick === 'ccswitch'
      ? t('page.console.token.connect.quick.ccswitch', {
          defaultValue: '在 CC Switch 里导入',
        })
      : t('page.console.token.connect.quick.app', {
          defaultValue: '打开 {{tool}} 导入',
          tool: tool.name,
        });
  const quickHint =
    tool.quick === 'ccswitch'
      ? extrasSkipImport
        ? t('page.console.token.connect.quick.ccswitchOneModel', {
            defaultValue:
              '需安装 CC Switch；导入只带默认模型，「同时导入」的模型走一键脚本或配置文件',
          })
        : t('page.console.token.connect.quick.ccswitchHint', {
            defaultValue: '需安装 CC Switch；未安装时用下面的一键脚本',
          })
      : t('page.console.token.connect.quick.appHint', {
          defaultValue: '需安装 {{tool}}；打开后确认导入',
          tool: tool.name,
        });

  const expireSecretBeforeExport = () => {
    if (
      secret &&
      expiryRef.current !== undefined &&
      Date.now() >= expiryRef.current
    ) {
      setSecret('');
      setSecretExpired(true);
      setKeyCopied(false);
      setCopied(false);
      return true;
    }
    return false;
  };

  const openDeepLink = () => {
    if (expireSecretBeforeExport()) return;
    if (!deepLink || !secret || modelMissing || saving) return;
    rememberTool(record?.id, tool.id);
    // The scheme is opened on the click itself — waiting for the save first
    // would spend the gesture the browser needs to hand the URL to an app —
    // and the Key's own tool and lane are written straight after.
    window.location.href = deepLink;
    setImportRequested(true);
    // A custom-scheme launch has no import acknowledgement. In particular,
    // CC Switch can lose the first event while its webview is starting. Keep
    // the dialog available for a user-initiated retry; never resend on a timer.
    void saveChanges();
  };

  const copyKey = async () => {
    if (expireSecretBeforeExport()) return;
    if (!secret) return;
    if (await copy(secret)) {
      setKeyCopied(true);
      return;
    }
    showError(t('page.console.tables.copyFailed'));
  };

  const copySnippet = async () => {
    if (expireSecretBeforeExport()) return false;
    if (!snippetCopyable) return false;
    const ok = await copy(format === 'link' ? deepLink : snippet);
    if (ok) {
      setCopied(true);
      rememberTool(record?.id, tool.id);
    } else {
      showError(t('page.console.tables.copyFailed'));
    }
    return ok;
  };

  /** The map to save: the Key's own entries, plus a pin for every model this
   *  dialog wrote that does not already follow its vendor default, minus the
   *  pins that now match it. A model the dialog never showed is left alone. */
  const nextModelLanes = (
    baseline: Record<string, Lane>,
  ): Record<string, Lane> => {
    const next = { ...baseline };
    writtenModels.forEach((id) => {
      if (laneOf(id) === vendorLaneOf(id)) delete next[id];
      else next[id] = laneOf(id);
    });
    return next;
  };

  const sameLanes = (
    a: Record<string, Lane>,
    b: Record<string, Lane>,
  ): boolean => {
    const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
    return [...keys].every((key) => a[key] === b[key]);
  };

  const pendingChanges = () => {
    if (!record || !canUpdate) return null;
    const baseline = savedConnection.current ?? record;
    const previous = (baseline.model_lanes || {}) as Record<string, Lane>;
    const productChange = String(baseline.product || '') !== tool.id;
    const laneChange = !sameLanes(previous, nextModelLanes(previous));
    if (!productChange && !laneChange) return null;
    return { laneChange, productChange };
  };

  /** Writes the two fields the dialog owns. Both ways out of it — the import
   *  button and 复制并完成 — end here, or a Key imported through the app would
   *  keep routing on its old lane and never show its tool in the list. */
  const saveChanges = async () => {
    const changes = pendingChanges();
    if (!changes || !record || record.id === undefined) return true;
    setSaving(true);
    try {
      const baseline = savedConnection.current ?? record;
      const fields: Parameters<typeof tokens.updateTokenConnect>[1] = {
        expected_version:
          typeof baseline.version === 'number' ? baseline.version : undefined,
      };
      if (changes.productChange) fields.product = tool.id;
      if (changes.laneChange) {
        fields.model_lanes = nextModelLanes(
          (baseline.model_lanes || {}) as Record<string, Lane>,
        );
      }
      const response = await tokens.updateTokenConnect(record.id, fields);
      if (response.data) {
        // The modal stays open after handoff. Retry must not repeat a stale
        // write, and later edits must use the version returned by this save.
        savedConnection.current = {
          model_lanes: response.data.model_lanes,
          product: response.data.product,
          vendor_groups: response.data.vendor_groups,
          version: response.data.version,
        };
      }
      return true;
    } catch (error) {
      showError(error);
      return false;
    } finally {
      setSaving(false);
    }
  };

  const finish = async () => {
    if (!record) return;
    const ok = await copySnippet();
    if (!ok) return;
    if (!(await saveChanges())) return;
    notify.success(
      t('page.console.token.connect.copiedToast', {
        defaultValue: '已复制 {{tool}} 配置',
        tool: tool.name,
      }),
    );
    onOpenChange(false);
  };

  const groupLabel = (group: string) =>
    t(`page.console.token.connect.group.${group}`);

  return (
    <ModalFrame
      open={open}
      onOpenChange={onOpenChange}
      busy={saving}
      closeLabel={t('legacy.i18n.close.6c14bd7f6f')}
      className='lazu-modal-content--xwide tok-connect'
    >
      <div className='tok-connect__grid'>
        <nav
          className='tok-connect__rail'
          aria-label={t('page.console.token.connect.railLabel', {
            defaultValue: '选择工具',
          })}
        >
          <div className='tok-connect__rail-head'>
            <b>
              {t('page.console.token.connect.title', {
                defaultValue: '接入',
              })}
            </b>
            <span>{String(record?.name || '')}</span>
          </div>
          {CONNECT_GROUPS.map((group) => (
            <div className='tok-connect__group' key={group}>
              <span className='tok-connect__group-label'>
                {groupLabel(group)}
              </span>
              {CONNECT_TOOLS.filter((item) => item.group === group).map(
                (item) => {
                  const selected = item.id === tool.id;
                  return (
                    <button
                      key={item.id}
                      type='button'
                      data-owns-chrome='true'
                      aria-current={selected ? 'true' : undefined}
                      className={cn('tok-connect__tool', selected && 'is-on')}
                      onClick={() => setToolId(item.id)}
                    >
                      <ToolMono tool={item} active={selected} size='lg' />
                      {item.name}
                    </button>
                  );
                },
              )}
            </div>
          ))}
        </nav>
        <div className='tok-connect__mobile-tool'>
          <Select
            aria-label={t('page.console.token.connect.railLabel')}
            value={tool.id}
            onValueChange={(value) => setToolId(value as ConnectToolId)}
          >
            {CONNECT_TOOLS.map((item) => (
              <SelectItem key={item.id} value={item.id}>
                {item.name}
              </SelectItem>
            ))}
          </Select>
        </div>
        <div className='tok-connect__main'>
          <div className='tok-connect__head'>
            <ModalTitle className='tok-connect__title'>
              <ToolMono tool={tool} active size='xl' />
              {tool.name}
            </ModalTitle>
            <ModalDescription className='tok-connect__desc'>
              {t(`page.console.token.connect.desc.${tool.id}`)}
            </ModalDescription>
          </div>

          {!usable ? (
            <InlineNotice
              tone='warning'
              title={t('page.console.token.connect.unusable', {
                defaultValue: '这把 Key 目前不可用，配置复制后不会生效',
              })}
            />
          ) : null}
          {legacy ? (
            <InlineNotice
              tone='warning'
              title={t('page.console.token.connect.legacy', {
                defaultValue:
                  '这把旧 Key 只保存了摘要，拿不到完整值；在列表里「重新生成」后再来接入。',
              })}
            />
          ) : null}
          {!legacy && !canReveal && record ? (
            <InlineNotice
              tone='info'
              title={t('page.console.token.connect.noReveal', {
                defaultValue:
                  '你没有查看这把 Key 完整值的权限，下面用占位符代替。',
              })}
            />
          ) : null}
          {notice}
          {secretError ? (
            <InlineNotice
              tone='danger'
              title={secretError}
              actions={
                <Button
                  loading={revealing}
                  onClick={() => setRevealAttempt((attempt) => attempt + 1)}
                >
                  {t('common.retry')}
                </Button>
              }
            />
          ) : null}
          {modelMissing ? (
            <InlineNotice
              tone='warning'
              title={t('page.console.token.connect.noModel', {
                defaultValue:
                  '{{tool}} 需要一个模型，但这把 Key 现在没有可用的 {{protocol}} 模型。先在「编辑」里放开模型范围，或换一个工具。',
                protocol: toolEndpointLabel(tool),
                tool: tool.name,
              })}
            />
          ) : null}

          <div
            className={cn(
              'tok-connect__controls',
              !tool.usesModel
                ? 'tok-connect__controls--one'
                : tool.extraModels === 'aliases'
                  ? 'tok-connect__controls--slots'
                  : !tool.lightSlot && 'tok-connect__controls--two',
            )}
          >
            {tool.usesModel ? (
              <ModelPickerField
                disabled={modelOptions.length === 0}
                id='tok-connect-model'
                label={t('page.console.token.connect.model', {
                  defaultValue: '默认模型',
                })}
                lane={laneSlotFor(model, 'tok-connect-model-label')}
                onChange={setModel}
                priceForModel={priceForModel}
                result={picker}
                t={t}
                value={model}
              />
            ) : null}
            {tool.usesModel && tool.extraModels === 'aliases' ? (
              <>
                <ModelPickerField
                  disabled={modelOptions.length === 0}
                  id='tok-connect-opus'
                  label={t('page.console.token.connect.alias.opus', {
                    defaultValue: '旗舰档 · /model opus',
                  })}
                  lane={laneSlotFor(opus, 'tok-connect-opus-label')}
                  onChange={(next) => {
                    setOpusPinned(true);
                    setOpus(next);
                  }}
                  priceForModel={priceForModel}
                  result={picker}
                  t={t}
                  value={opus}
                />
                <ModelPickerField
                  disabled={modelOptions.length === 0}
                  id='tok-connect-sonnet'
                  label={t('page.console.token.connect.alias.sonnet', {
                    defaultValue: '均衡档 · /model sonnet',
                  })}
                  lane={laneSlotFor(sonnet, 'tok-connect-sonnet-label')}
                  onChange={(next) => {
                    setSonnetPinned(true);
                    setSonnet(next);
                  }}
                  priceForModel={priceForModel}
                  result={picker}
                  t={t}
                  value={sonnet}
                />
              </>
            ) : null}
            {tool.usesModel && tool.lightSlot ? (
              <ModelPickerField
                disabled={modelOptions.length === 0}
                id='tok-connect-light'
                label={
                  tool.extraModels === 'aliases'
                    ? t('page.console.token.connect.alias.haiku', {
                        defaultValue: '轻量档 · /model haiku',
                      })
                    : t('page.console.token.connect.light', {
                        defaultValue: '轻量模型',
                      })
                }
                lane={laneSlotFor(light, 'tok-connect-light-label')}
                onChange={(next) => {
                  setLightPinned(true);
                  setLight(next);
                }}
                priceForModel={priceForModel}
                result={picker}
                t={t}
                value={light}
              />
            ) : null}
            {!tool.usesModel ? (
              <LaneTable
                canUpdate={canUpdate}
                laneOf={laneOf}
                models={laneTableModels}
                offerFor={offerFor}
                onChange={setLaneOf}
                priceForModel={priceForModel}
                t={t}
                vendorLabelOf={vendorLabelOf}
                vendorLaneOf={vendorLaneOf}
              />
            ) : null}
            {tool.usesModel && tool.extraModels === 'list' ? (
              <ModelMultiField
                disabled={modelOptions.length === 0}
                hint={
                  tool.id === 'codex'
                    ? t('page.console.token.connect.extrasHintCodex', {
                        defaultValue:
                          '写成 Codex 的模型目录：/model 里切换，每个模型各选推理强度；默认模型已在目录里',
                      })
                    : tool.lightSlot
                      ? t('page.console.token.connect.extrasHint', {
                          defaultValue:
                            '写进 {{tool}} 的模型列表，在应用里直接切换；默认和轻量模型已经在列表里',
                          tool: tool.name,
                        })
                      : t('page.console.token.connect.extrasHintDefaultOnly', {
                          defaultValue:
                            '写进 {{tool}} 的模型列表，在应用里直接切换；默认模型已经在列表里',
                          tool: tool.name,
                        })
                }
                candidates={extraCandidates}
                fixed={[model, tool.lightSlot ? light : '']}
                id='tok-connect-extras'
                label={t('page.console.token.connect.extras', {
                  defaultValue: '同时导入',
                })}
                laneFor={(id) => (
                  <LaneControl
                    compact
                    disabled={!canUpdate}
                    offer={offerFor(id)}
                    onChange={(next) => setLaneOf(id, next)}
                    t={t}
                    value={laneOf(id)}
                  />
                )}
                note={t('page.console.token.connect.extrasLaneNote', {
                  defaultValue:
                    '每行右侧是该模型的线路；没有 Agent 专线来源的模型不显示开关。Agent 专线由第三方订阅池提供，不是官方 API，只建议在对应的 Agent 工具里用',
                })}
                onChange={(next) => {
                  setExtrasPinned(true);
                  setExtras(next);
                }}
                priceForModel={priceForModel}
                result={picker}
                t={t}
                values={extras}
              />
            ) : null}
            {laneSummary}
          </div>

          {tool.quick ? (
            <div className='tok-connect__quick'>
              <Button
                variant='primary'
                disabled={!secret || modelMissing || saving}
                onClick={openDeepLink}
              >
                <Zap size={14} />
                {importRequested
                  ? t('page.console.token.connect.quick.retry')
                  : quickLabel}
              </Button>
              <span className='tok-connect__quick-hint'>
                {importRequested
                  ? t('page.console.token.connect.quick.pending', {
                      tool: tool.quick === 'ccswitch' ? 'CC Switch' : tool.name,
                    })
                  : quickHint}
                {tool.quick === 'ccswitch' ? (
                  <>
                    {' · '}
                    <a
                      href='https://github.com/farion1231/cc-switch/releases'
                      target='_blank'
                      rel='noopener noreferrer'
                    >
                      {t('page.console.token.connect.quick.download', {
                        defaultValue: '下载 CC Switch',
                      })}
                    </a>
                  </>
                ) : null}
              </span>
            </div>
          ) : null}

          <Tabs
            className='tok-connect__code-area'
            value={format}
            onValueChange={(value) => setFormat(value as ConnectFormat)}
          >
            <TabsList variant='line' className='tok-connect__formats'>
              {tool.formats.map((item) => (
                <TabsTrigger
                  key={item}
                  value={item}
                  className='tok-connect__format'
                >
                  {FORMAT_LABELS[item].startsWith('page.')
                    ? t(FORMAT_LABELS[item])
                    : FORMAT_LABELS[item]}
                </TabsTrigger>
              ))}
            </TabsList>
            <TabsContent
              value={format}
              className={cn(
                'tok-connect__code',
                format === 'link' && 'tok-connect__code--wrap',
              )}
            >
              <div className='tok-connect__code-head'>
                <span>
                  {asksAtPrompt
                    ? t('page.console.token.connect.codePrompts', {
                        defaultValue: '运行后脚本会让你粘贴密钥，输入不会显示',
                      })
                    : promptsForKey
                      ? t('page.console.token.connect.codePlaceholderKey', {
                          defaultValue: '密钥位置留了占位符，复制后把它替换掉',
                        })
                      : secret
                        ? t('page.console.token.connect.codeFilled', {
                            defaultValue: '已填入这把 Key 的完整值',
                          })
                        : t('page.console.token.connect.codePlaceholder', {
                            defaultValue: '密钥位置是占位符',
                          })}
                </span>
                {promptsForKey ? (
                  <button
                    type='button'
                    data-owns-chrome='true'
                    className='tok-connect__copy'
                    disabled={!secret}
                    onClick={() => void copyKey()}
                  >
                    <Copy size={12} aria-hidden='true' />
                    {keyCopied
                      ? t('page.console.token.connect.keyCopied', {
                          defaultValue: '密钥已复制',
                        })
                      : t('page.console.token.connect.copyKey', {
                          defaultValue: '复制密钥',
                        })}
                  </button>
                ) : null}
                <button
                  type='button'
                  data-owns-chrome='true'
                  className='tok-connect__copy'
                  disabled={!snippetCopyable}
                  onClick={() => void copySnippet()}
                >
                  <Copy size={12} aria-hidden='true' />
                  {copied
                    ? t('secret.copied', { defaultValue: '已复制' })
                    : t('secret.copy', { defaultValue: '复制' })}
                </button>
              </div>
              <pre>{snippet}</pre>
            </TabsContent>
          </Tabs>

          {record && canUpdate ? (
            <span className='tok-connect__label-note'>
              {t('page.console.token.connect.markNote', {
                defaultValue:
                  '完成后这把 Key 会标记成「{{tool}}」，列表里能认出来；想改在编辑里改。',
                tool: tool.name,
              })}
            </span>
          ) : null}
        </div>
      </div>
      <div className='tok-modal-foot'>
        <span className='tok-modal-foot__hint'>
          {tool.docsUrl ? (
            <a href={tool.docsUrl} target='_blank' rel='noopener noreferrer'>
              {t('page.console.token.connect.docs', {
                defaultValue: '{{tool}} 配置说明 →',
                tool: tool.name,
              })}
            </a>
          ) : null}
        </span>
        <Button
          variant='secondary'
          disabled={saving}
          onClick={() => onOpenChange(false)}
        >
          {t('legacy.i18n.close.6c14bd7f6f')}
        </Button>
        <Button
          variant='primary'
          loading={saving}
          disabled={!snippetCopyable}
          onClick={() => void finish()}
        >
          <Copy size={14} />
          {t('page.console.token.connect.finish', {
            defaultValue: '复制并完成',
          })}
        </Button>
      </div>
    </ModalFrame>
  );
}
