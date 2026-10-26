import { useQuery } from '@tanstack/react-query';
import { ArrowLeft, Plus, Search } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, Navigate, useNavigate, useParams } from 'react-router-dom';
import { PlanStatusBadge } from './PlanStatusBadge';
import {
  countChanges,
  dateToIso,
  draftFromPlan,
  draftProblems,
  emptyDraft,
  endDateToIso,
  endIsoToDate,
  formatUsd,
  isoToDate,
  laneLabel,
  payloadFromDraft,
  planStatus,
  previewRows,
  vendorModelCounts,
  zheLabel,
  type PlanDraft,
  type PlanLanes,
  type PricePlanMember,
  type RuleHit,
} from './pricePlans';
import {
  usePricePlanMutations,
  usePricePlanQuery,
  usePricingCatalogQuery,
} from './usePricePlans';
import {
  Button,
  ConsolePage,
  DatePicker,
  ErrorState,
  Input,
  Modal,
  PendingBlock,
  PendingValue,
  Popover,
  PopoverContent,
  PopoverTrigger,
  Tabs,
  TabsList,
  TabsTrigger,
} from '../../components/ui';
import { coerceItemsPayload } from '../../components/ui/table-tools';
import { notify } from '../../components/ui/toast';
import { API } from '../../helpers';
import { isRoot } from '../../helpers/utils';
import { useDelayedReveal } from '../../hooks/common/useDelayedReveal';
import { unwrap } from '../../lib/api-error';
import type { TFunction } from 'i18next';
import './model-ops.css';
import './price-plans.css';

const ns = 'page.console.modelOps.plans';
const LANES: PlanLanes[] = ['direct', 'all', 'cheap'];

/** − [ N ] % — the one way a percentage is typed on this page. */
function PctInput({
  label,
  onChange,
  placeholder,
  size = 'md',
  value,
}: {
  label: string;
  onChange: (value: string) => void;
  placeholder?: string;
  size?: 'md' | 'sm';
  value: string;
}) {
  return (
    <span className={`pp-pct pp-pct--${size}`}>
      <span aria-hidden='true'>−</span>
      <Input
        aria-label={label}
        inputMode='decimal'
        placeholder={placeholder}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
      <span aria-hidden='true'>%</span>
    </span>
  );
}

function ruleWhy(t: TFunction, hit: RuleHit | null) {
  if (!hit) return t(`${ns}.preview.none`, { defaultValue: '无' });
  if (hit.kind === 'default')
    return t(`${ns}.preview.default`, {
      defaultValue: '全场 {{zhe}}',
      zhe: zheLabel(hit.pct),
    });
  if (hit.kind === 'model')
    return t(`${ns}.preview.exception`, {
      defaultValue: '例外 {{zhe}}',
      zhe: zheLabel(hit.pct),
    });
  return `${hit.subject} ${zheLabel(hit.pct)}`;
}

type SearchUser = {
  display_name: string;
  id: string;
  internal_id: number;
  price_plan_id: number;
  price_plan_name: string;
  username: string;
};

function MembersPanel({
  members,
  pending,
  planId,
  t,
}: {
  members: PricePlanMember[];
  pending: boolean;
  planId: number | null;
  t: TFunction;
}) {
  const [draft, setDraft] = useState('');
  const [keyword, setKeyword] = useState('');
  const mutations = usePricePlanMutations();

  useEffect(() => {
    const handle = window.setTimeout(() => setKeyword(draft.trim()), 250);
    return () => window.clearTimeout(handle);
  }, [draft]);

  const results = useQuery({
    enabled: Boolean(planId) && keyword.length > 0,
    queryKey: ['price-plan-member-search', keyword],
    queryFn: async (): Promise<SearchUser[]> => {
      const response = await API.get('/api/user/search', {
        params: {
          exclude_deleted: 'true',
          keyword: keyword.replace(/^#/, ''),
          p: 1,
          page_size: 6,
        },
        skipErrorHandler: true,
      });
      unwrap(response);
      const page = coerceItemsPayload<Record<string, unknown>>(
        response.data,
        1,
        6,
      );
      return page.items.map((row) => ({
        display_name: String(row.display_name || ''),
        id: String(row.id || ''),
        internal_id: Number(row.internal_id) || 0,
        price_plan_id: Number(row.price_plan_id) || 0,
        price_plan_name: String(row.price_plan_name || ''),
        username: String(row.username || ''),
      }));
    },
  });

  const memberIds = new Set(members.map((member) => member.public_id));
  const candidates = (results.data || []).filter(
    (user) => !memberIds.has(user.id),
  );

  return (
    <section className='pp-card pp-members'>
      <div className='pp-card__head'>
        <b>
          {t(`${ns}.edit.members`, { defaultValue: '成员' })}
          {' · '}
          {pending ? <PendingValue chars={2} /> : members.length}
        </b>
      </div>
      {planId ? (
        <label className='pp-search'>
          <Search size={14} aria-hidden='true' />
          <input
            aria-label={t(`${ns}.edit.memberSearch`, {
              defaultValue: '搜索用户名、邮箱或 #ID 添加',
            })}
            placeholder={t(`${ns}.edit.memberSearch`, {
              defaultValue: '搜索用户名、邮箱或 #ID 添加',
            })}
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
          />
        </label>
      ) : (
        <p className='mo-sub'>
          {t(`${ns}.edit.membersAfterSave`, {
            defaultValue: '保存后添加成员',
          })}
        </p>
      )}
      {keyword && planId ? (
        <div className='pp-results'>
          {results.isPending ? (
            <div className='pp-member'>
              <PendingValue chars={16} />
            </div>
          ) : candidates.length === 0 ? (
            <div className='pp-member mo-sub'>
              {t(`${ns}.edit.noMatch`, { defaultValue: '没有匹配的用户' })}
            </div>
          ) : (
            candidates.map((user) => (
              <div className='pp-member' key={user.id}>
                <span className='pp-member__who'>
                  <span>{user.display_name || user.username}</span>
                  <small>
                    #{user.internal_id}
                    {user.price_plan_name
                      ? ` · ${t(`${ns}.edit.onPlan`, {
                          defaultValue: '现属 {{name}}',
                          name: user.price_plan_name,
                        })}`
                      : ''}
                  </small>
                </span>
                <Button
                  size='xs'
                  variant='secondary'
                  loading={
                    mutations.addMember.isPending &&
                    mutations.addMember.variables?.user === user.id
                  }
                  onClick={() =>
                    mutations.addMember.mutate(
                      { id: planId, user: user.id },
                      {
                        onSuccess: () => {
                          setDraft('');
                          setKeyword('');
                        },
                      },
                    )
                  }
                >
                  {t(`${ns}.edit.add`, { defaultValue: '添加' })}
                </Button>
              </div>
            ))
          )}
        </div>
      ) : null}
      {pending ? (
        <PendingBlock height={46} />
      ) : (
        members.map((member) => {
          const name = member.display_name || member.username;
          return (
            <div className='pp-member' key={member.public_id}>
              <span className='pp-member__avatar' aria-hidden='true'>
                {name.slice(0, 2).toUpperCase()}
              </span>
              <span className='pp-member__who'>
                <span>{name}</span>
                <small>#{member.id}</small>
              </span>
              <Button
                size='xs'
                variant='ghost'
                disabled={!planId}
                loading={
                  mutations.removeMember.isPending &&
                  mutations.removeMember.variables?.user === member.public_id
                }
                onClick={() =>
                  planId &&
                  mutations.removeMember.mutate({
                    id: planId,
                    user: member.public_id,
                  })
                }
              >
                {t(`${ns}.edit.removeMember`, { defaultValue: '移出' })}
              </Button>
            </div>
          );
        })
      )}
    </section>
  );
}

/** One plan: its rules on the left, who is on it and what they would pay on
 *  the right, and one save for everything but membership (which applies at
 *  once, like any other account change). */
export default function PricePlanEditorPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const params = useParams();
  const root = isRoot();
  const planId =
    params.id && params.id !== 'new' ? Number(params.id) || null : null;
  const isNew = params.id === 'new';

  const detail = usePricePlanQuery(root ? planId : null);
  const catalog = usePricingCatalogQuery(root);
  const mutations = usePricePlanMutations();

  const [draft, setDraft] = useState<PlanDraft>(emptyDraft);
  const [initial, setInitial] = useState<PlanDraft>(emptyDraft);
  const [showProblems, setShowProblems] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [vendorOpen, setVendorOpen] = useState(false);
  const [modelOpen, setModelOpen] = useState(false);
  const [modelQuery, setModelQuery] = useState('');

  const loadedPlan = detail.data?.plan;
  useEffect(() => {
    if (!loadedPlan) return;
    const next = draftFromPlan(loadedPlan);
    setDraft(next);
    setInitial(next);
    setShowProblems(false);
  }, [loadedPlan]);

  const reveal = useDelayedReveal(Boolean(planId) && detail.isPending);
  const changes = countChanges(draft, initial);
  const problems = draftProblems(draft);
  const counts = useMemo(
    () => vendorModelCounts(catalog.data?.rows || []),
    [catalog.data?.rows],
  );
  const preview = useMemo(
    () => previewRows(draft, catalog.data?.rows || []),
    [draft, catalog.data?.rows],
  );

  if (!root) return <Navigate replace to='/console/model-ops' />;
  if (!isNew && !planId) return <Navigate replace to='/console/price-plans' />;

  const patch = (next: Partial<PlanDraft>) =>
    setDraft((current) => ({ ...current, ...next }));

  const save = () => {
    if (problems.length) {
      setShowProblems(true);
      return;
    }
    mutations.save.mutate(
      { id: planId, payload: payloadFromDraft(draft) },
      {
        onSuccess: (plan) => {
          notify.success(t('common.saved', { defaultValue: '已保存' }));
          if (!planId) {
            navigate(`/console/price-plans/${plan.id}`, {
              replace: true,
            });
            return;
          }
          const next = draftFromPlan(plan);
          setDraft(next);
          setInitial(next);
        },
      },
    );
  };

  const vendorsTaken = new Set(draft.vendors.map((rule) => rule.vendor_id));
  const modelsTaken = new Set(draft.models.map((rule) => rule.model_name));
  const vendorChoices = (catalog.data?.vendors || []).filter(
    (vendor) => !vendorsTaken.has(vendor.id),
  );
  const modelChoices = (catalog.data?.rows || [])
    .map((row) => row.model_name)
    .filter(
      (name) =>
        !modelsTaken.has(name) &&
        name.toLowerCase().includes(modelQuery.trim().toLowerCase()),
    )
    .slice(0, 40);
  const vendorName = new Map(
    (catalog.data?.vendors || []).map((vendor) => [vendor.id, vendor.name]),
  );
  const vendorOfModel = new Map(
    (catalog.data?.rows || []).map((row) => [
      row.model_name,
      row.vendor_id ? (vendorName.get(row.vendor_id) ?? '') : '',
    ]),
  );

  const title = isNew
    ? t(`${ns}.edit.newTitle`, { defaultValue: '新建方案' })
    : initial.name ||
      (detail.isPending ? <PendingValue chars={10} /> : String(planId));

  const status = planStatus({
    active: draft.active,
    ends_at: draft.endsAt || null,
    starts_at: draft.startsAt || null,
  });

  const problem = (key: (typeof problems)[number]) =>
    showProblems && problems.includes(key);

  const body =
    planId && detail.isError ? (
      <ErrorState
        title={t(`${ns}.edit.loadFailed`, { defaultValue: '方案加载失败' })}
        action={
          <Button variant='ghost' onClick={() => detail.refetch()}>
            {t('common.retry', { defaultValue: '重试' })}
          </Button>
        }
      />
    ) : planId && detail.isPending ? (
      <div className='pp-grid'>
        <div className='pp-col'>
          {reveal ? (
            <PendingBlock height={220} />
          ) : (
            <div style={{ height: 220 }} />
          )}
          {reveal ? <PendingBlock height={260} /> : null}
        </div>
        <div className='pp-col'>
          {reveal ? <PendingBlock height={180} /> : null}
        </div>
      </div>
    ) : (
      <div className='pp-grid'>
        <div className='pp-col'>
          <section className='pp-card pp-basics'>
            <label className='pp-field'>
              <span>{t(`${ns}.edit.name`, { defaultValue: '名称' })}</span>
              <Input
                aria-invalid={problem('name') || undefined}
                value={draft.name}
                onChange={(event) => patch({ name: event.target.value })}
              />
            </label>
            <label className='pp-field'>
              <span>
                {t(`${ns}.edit.note`, { defaultValue: '备注（仅平台可见）' })}
              </span>
              <Input
                value={draft.note}
                onChange={(event) => patch({ note: event.target.value })}
              />
            </label>
            <div className='pp-field'>
              <span>
                {t(`${ns}.edit.validity`, { defaultValue: '有效期' })}
              </span>
              <span className='pp-dates'>
                <DatePicker
                  clearable
                  placeholder={t(`${ns}.edit.startNow`, {
                    defaultValue: '立即',
                  })}
                  value={isoToDate(draft.startsAt)}
                  onChange={(date) => patch({ startsAt: dateToIso(date) })}
                />
                <span className='mo-sub'>
                  {t(`${ns}.edit.to`, { defaultValue: '至' })}
                </span>
                <DatePicker
                  clearable
                  placeholder={t(`${ns}.forever`, { defaultValue: '长期' })}
                  value={endIsoToDate(draft.endsAt)}
                  onChange={(date) => patch({ endsAt: endDateToIso(date) })}
                />
              </span>
              {problem('dates') ? (
                <small className='pp-error'>
                  {t(`${ns}.edit.badDates`, {
                    defaultValue: '结束须晚于开始',
                  })}
                </small>
              ) : null}
            </div>
            <div className='pp-field'>
              <span>{t(`${ns}.edit.lanes`, { defaultValue: '适用线路' })}</span>
              <Tabs
                value={draft.lanes}
                onValueChange={(value) => patch({ lanes: value as PlanLanes })}
              >
                <TabsList>
                  {LANES.map((lane) => (
                    <TabsTrigger key={lane} value={lane}>
                      {laneLabel(t, lane)}
                    </TabsTrigger>
                  ))}
                </TabsList>
              </Tabs>
            </div>
            <div className='pp-field'>
              <span>
                {t(`${ns}.edit.defaultPct`, { defaultValue: '全场默认' })}
              </span>
              <PctInput
                label={t(`${ns}.edit.defaultPct`, { defaultValue: '全场默认' })}
                placeholder={t(`${ns}.edit.noDiscount`, {
                  defaultValue: '不打折',
                })}
                value={draft.defaultPct}
                onChange={(defaultPct) => patch({ defaultPct })}
              />
              {problem('defaultPct') ? (
                <small className='pp-error'>
                  {t(`${ns}.edit.badPct`, { defaultValue: '填 0–100 之间' })}
                </small>
              ) : null}
            </div>
          </section>

          <section className='pp-card pp-rules'>
            <div className='pp-card__head'>
              <b>
                {t(`${ns}.edit.vendorRules`, { defaultValue: '供应商规则' })}
              </b>
              <span className='mo-sub'>
                {t(`${ns}.edit.vendorRulesNote`, {
                  defaultValue: '含新上线模型',
                })}
              </span>
              <Popover open={vendorOpen} onOpenChange={setVendorOpen}>
                <PopoverTrigger>
                  <Button className='pp-card__action' size='sm'>
                    <Plus size={14} aria-hidden='true' />
                    {t(`${ns}.edit.addVendor`, { defaultValue: '添加供应商' })}
                  </Button>
                </PopoverTrigger>
                <PopoverContent align='end' className='pp-picker'>
                  {catalog.isPending ? (
                    <PendingValue chars={12} />
                  ) : vendorChoices.length === 0 ? (
                    <span className='mo-sub'>
                      {t(`${ns}.edit.noneLeft`, { defaultValue: '没有可选的' })}
                    </span>
                  ) : (
                    vendorChoices.map((vendor) => (
                      <Button
                        key={vendor.id}
                        className='pp-picker__option'
                        size='sm'
                        variant='ghost'
                        onClick={() => {
                          patch({
                            vendors: [
                              ...draft.vendors,
                              {
                                pct: '',
                                vendor_id: vendor.id,
                                vendor_name: vendor.name,
                              },
                            ],
                          });
                          setVendorOpen(false);
                        }}
                      >
                        <span>{vendor.name}</span>
                        <small>
                          {t(`${ns}.edit.modelCount`, {
                            count: counts.get(vendor.id) ?? 0,
                            defaultValue: '{{count}} 个模型',
                          })}
                        </small>
                      </Button>
                    ))
                  )}
                </PopoverContent>
              </Popover>
            </div>
            {draft.vendors.length ? (
              <>
                <div className='pp-rule pp-rule--head'>
                  <span>
                    {t(`${ns}.edit.vendor`, { defaultValue: '供应商' })}
                  </span>
                  <span className='pp-rule__end'>
                    {t(`${ns}.edit.discount`, { defaultValue: '折扣' })}
                  </span>
                  <span>
                    {t(`${ns}.edit.covers`, { defaultValue: '覆盖模型' })}
                  </span>
                  <span />
                </div>
                {draft.vendors.map((rule, index) => (
                  <div className='pp-rule' key={rule.vendor_id}>
                    <span>{rule.vendor_name}</span>
                    <span className='pp-rule__end'>
                      <PctInput
                        label={rule.vendor_name}
                        size='sm'
                        value={rule.pct}
                        onChange={(pct) =>
                          patch({
                            vendors: draft.vendors.map((item, i) =>
                              i === index ? { ...item, pct } : item,
                            ),
                          })
                        }
                      />
                    </span>
                    <span className='mo-sub'>
                      {catalog.isPending ? (
                        <PendingValue chars={6} />
                      ) : (
                        t(`${ns}.edit.modelCount`, {
                          count: counts.get(rule.vendor_id) ?? 0,
                          defaultValue: '{{count}} 个模型',
                        })
                      )}
                    </span>
                    <Button
                      size='xs'
                      variant='ghost'
                      onClick={() =>
                        patch({
                          vendors: draft.vendors.filter((_, i) => i !== index),
                        })
                      }
                    >
                      {t(`${ns}.edit.remove`, { defaultValue: '删除' })}
                    </Button>
                  </div>
                ))}
              </>
            ) : (
              <div className='pp-none'>
                {t(`${ns}.edit.none`, { defaultValue: '暂无' })}
              </div>
            )}
            {problem('vendorPct') ? (
              <small className='pp-error pp-rules__error'>
                {t(`${ns}.edit.badPct`, { defaultValue: '填 0–100 之间' })}
              </small>
            ) : null}
          </section>

          <section className='pp-card pp-rules'>
            <div className='pp-card__head'>
              <b>{t(`${ns}.edit.modelRules`, { defaultValue: '模型例外' })}</b>
              <Popover
                open={modelOpen}
                onOpenChange={(open) => {
                  setModelOpen(open);
                  if (!open) setModelQuery('');
                }}
              >
                <PopoverTrigger>
                  <Button className='pp-card__action' size='sm'>
                    <Plus size={14} aria-hidden='true' />
                    {t(`${ns}.edit.addModel`, { defaultValue: '添加例外' })}
                  </Button>
                </PopoverTrigger>
                <PopoverContent align='end' className='pp-picker'>
                  <Input
                    aria-label={t(`${ns}.edit.modelSearch`, {
                      defaultValue: '搜索模型',
                    })}
                    placeholder={t(`${ns}.edit.modelSearch`, {
                      defaultValue: '搜索模型',
                    })}
                    value={modelQuery}
                    onChange={(event) => setModelQuery(event.target.value)}
                  />
                  <div className='pp-picker__list'>
                    {catalog.isPending ? (
                      <PendingValue chars={12} />
                    ) : modelChoices.length === 0 ? (
                      <span className='mo-sub'>
                        {t(`${ns}.edit.noneLeft`, {
                          defaultValue: '没有可选的',
                        })}
                      </span>
                    ) : (
                      modelChoices.map((name) => (
                        <Button
                          key={name}
                          className='pp-picker__option'
                          size='sm'
                          variant='ghost'
                          onClick={() => {
                            patch({
                              models: [
                                ...draft.models,
                                { model_name: name, pct: '' },
                              ],
                            });
                            setModelOpen(false);
                            setModelQuery('');
                          }}
                        >
                          <span className='pp-mono'>{name}</span>
                          <small>{vendorOfModel.get(name)}</small>
                        </Button>
                      ))
                    )}
                  </div>
                </PopoverContent>
              </Popover>
            </div>
            {draft.models.length ? (
              draft.models.map((rule, index) => (
                <div className='pp-rule' key={rule.model_name}>
                  <span className='pp-mono'>{rule.model_name}</span>
                  <span className='pp-rule__end'>
                    <PctInput
                      label={rule.model_name}
                      size='sm'
                      value={rule.pct}
                      onChange={(pct) =>
                        patch({
                          models: draft.models.map((item, i) =>
                            i === index ? { ...item, pct } : item,
                          ),
                        })
                      }
                    />
                  </span>
                  <span className='mo-sub'>
                    {rule.pct.trim() === '0'
                      ? t(`${ns}.edit.noDiscount`, { defaultValue: '不打折' })
                      : vendorOfModel.get(rule.model_name) || ''}
                  </span>
                  <Button
                    size='xs'
                    variant='ghost'
                    onClick={() =>
                      patch({
                        models: draft.models.filter((_, i) => i !== index),
                      })
                    }
                  >
                    {t(`${ns}.edit.remove`, { defaultValue: '删除' })}
                  </Button>
                </div>
              ))
            ) : (
              <div className='pp-none'>
                {t(`${ns}.edit.none`, { defaultValue: '暂无' })}
              </div>
            )}
            {problem('modelPct') ? (
              <small className='pp-error pp-rules__error'>
                {t(`${ns}.edit.badPct`, { defaultValue: '填 0–100 之间' })}
              </small>
            ) : null}
          </section>
        </div>

        <div className='pp-col'>
          <MembersPanel
            members={detail.data?.members || []}
            pending={Boolean(planId) && detail.isPending}
            planId={planId}
            t={t}
          />

          <section className='pp-card pp-preview'>
            <div className='pp-card__head'>
              <b>
                {t(`${ns}.preview.title`, {
                  defaultValue: '预览 · 按官方价 /1M',
                })}
              </b>
            </div>
            <div className='pp-prow pp-prow--head'>
              <span>{t(`${ns}.preview.model`, { defaultValue: '模型' })}</span>
              <span>
                {t(`${ns}.preview.official`, { defaultValue: '官方' })}
              </span>
              <span>{t(`${ns}.preview.mine`, { defaultValue: '专属' })}</span>
            </div>
            {catalog.isPending ? (
              <PendingBlock height={5 * 44} />
            ) : catalog.isError ? (
              <div className='pp-none'>
                {t(`${ns}.preview.failed`, { defaultValue: '价格加载失败' })}
              </div>
            ) : (
              <>
                {preview.rows.map((row) => (
                  <div className='pp-prow' key={row.model}>
                    <span className='pp-prow__model'>
                      <span className='pp-mono'>{row.model}</span>
                      <small>{ruleWhy(t, row.hit)}</small>
                    </span>
                    <span className='pp-mono pp-prow__official'>
                      {formatUsd(row.official)}
                    </span>
                    <b className='pp-mono'>
                      {row.mine == null
                        ? t(`${ns}.preview.standard`, {
                            defaultValue: '标准价',
                          })
                        : formatUsd(row.mine)}
                    </b>
                  </div>
                ))}
                {preview.total > preview.rows.length ? (
                  <div className='pp-none'>
                    {t(`${ns}.preview.more`, {
                      count: preview.total - preview.rows.length,
                      defaultValue: '另有 {{count}} 个模型',
                    })}
                  </div>
                ) : null}
              </>
            )}
          </section>
        </div>
      </div>
    );

  return (
    <ConsolePage
      appearance='review'
      className='model-ops-page--compact pp-editor'
      title={
        <span className='pp-title'>
          {title}
          {!planId || detail.isPending ? null : (
            <PlanStatusBadge status={status} t={t} />
          )}
        </span>
      }
      description={
        <Link className='pp-back' to='/console/price-plans'>
          <ArrowLeft size={14} aria-hidden='true' />
          {t(`${ns}.navLabel`, { defaultValue: '专属折扣' })}
        </Link>
      }
      actions={
        <>
          {planId ? (
            <Button
              variant='ghost-danger'
              onClick={() => setConfirmDelete(true)}
            >
              {t(`${ns}.edit.delete`, { defaultValue: '删除' })}
            </Button>
          ) : null}
          <Button
            disabled={Boolean(planId) && detail.isPending}
            onClick={() => patch({ active: !draft.active })}
          >
            {draft.active
              ? t(`${ns}.edit.deactivate`, { defaultValue: '停用方案' })
              : t(`${ns}.edit.activate`, { defaultValue: '启用方案' })}
          </Button>
        </>
      }
    >
      {body}

      {!(planId && (detail.isPending || detail.isError)) ? (
        <div className='pp-savebar'>
          <span className='mo-sub'>
            {changes
              ? t(`${ns}.edit.changes`, {
                  count: changes,
                  defaultValue: '{{count}} 处改动',
                })
              : t(`${ns}.edit.noChanges`, { defaultValue: '没有改动' })}
          </span>
          <Button
            disabled={!changes || mutations.save.isPending}
            onClick={() => {
              setDraft(initial);
              setShowProblems(false);
            }}
          >
            {t(`${ns}.edit.revert`, { defaultValue: '撤销' })}
          </Button>
          <Button
            variant='primary'
            disabled={!changes && !isNew}
            loading={mutations.save.isPending}
            onClick={save}
          >
            {t(`${ns}.edit.save`, { defaultValue: '保存' })}
          </Button>
        </div>
      ) : null}

      <Modal
        open={confirmDelete}
        title={t(`${ns}.edit.deleteTitle`, {
          defaultValue: '删除「{{name}}」？',
          name: initial.name,
        })}
        description={t(`${ns}.edit.deleteHint`, {
          defaultValue: '成员回到标准价。',
        })}
        busy={mutations.remove.isPending}
        onOpenChange={(open) => {
          if (!open) setConfirmDelete(false);
        }}
        footer={
          <>
            <Button variant='ghost' onClick={() => setConfirmDelete(false)}>
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              variant='soft-danger'
              loading={mutations.remove.isPending}
              onClick={() =>
                planId &&
                mutations.remove.mutate(planId, {
                  onSuccess: () =>
                    navigate('/console/price-plans', {
                      replace: true,
                    }),
                })
              }
            >
              {t(`${ns}.edit.delete`, { defaultValue: '删除' })}
            </Button>
          </>
        }
      />
    </ConsolePage>
  );
}
