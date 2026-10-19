/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

For commercial licensing, please contact support@quantumnous.com
*/

import { useContext, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { useLandingFacts } from './useLandingFacts';
import BrandLogo from '../../../brand/BrandLogo';
import Reveal from '../../../components/marketing/Reveal';
import { StatusContext } from '../../../context/Status';
import { useActualTheme } from '../../../context/Theme';
import { getDocsBaseURL } from '../../../helpers/brandUrls';
import '../../../styles/fonts.css';
import '../lazu-landing.css';

type StatItem = { key: string; label: string };

type FeatureCopy = {
  body: string;
  bullets: string[];
  panelMeta: string;
  panelTitle: string;
  title: string;
};

type StepCopy = { body: string; title: string };
type FaqCopy = { a: string; q: string };

const asArray = <T,>(value: unknown): T[] =>
  Array.isArray(value) ? value : [];

/* Key ids are illustrative and identical in every language.
   The model list lives in the marketing copy instead, because llms.txt and the
   SEO assets are generated from the same array — see scripts/generate-seo-assets.mjs. */
/* The people in the hero's console shot. Illustrative and identical in every
   language, like the key ids below — the live figures on this page are the
   three under the rule, which come from the API. */
const MEMBERS = [
  {
    cap: null,
    initial: 'J',
    name: 'Jessy',
    pct: 44,
    role: 'admin',
    used: '$96.00',
  },
  {
    cap: 100,
    initial: 'W',
    name: 'Wen',
    pct: 91,
    role: 'member',
    used: '$91.00',
  },
  {
    cap: 50,
    initial: 'K',
    name: 'Kai',
    pct: 24,
    role: 'member',
    used: '$12.00',
  },
  {
    cap: 30,
    initial: 'N',
    name: 'nightly-batch',
    pct: 50,
    role: 'service',
    used: '$15.00',
  },
];

const KEYS = [
  { id: 'sk-lazu-4b7c…', name: 'Wen', off: false },
  { id: 'sk-lazu-9d21…', name: 'CI', off: false },
  { id: 'sk-lazu-1f08…', name: 'Intern', off: true },
];

const SPEND = [
  { name: 'design-team', total: '$261.00' },
  { name: 'Jessy', total: '$96.00' },
  { name: 'Wen', total: '$91.00' },
  { name: 'ci-runner', total: '$41.00' },
];

const QUOTA_PRESETS = ['$30', '$50', '$100', '$200'];

const prefersReducedMotion = () =>
  typeof window !== 'undefined' &&
  window.matchMedia('(prefers-reduced-motion: reduce)').matches;

/** Counts a number into place, and always lands on the real value.
 *
 * requestAnimationFrame does not run while a tab is in the background, so the
 * animation alone cannot be trusted to finish — a visitor who opened the page
 * in a background tab used to come back to a row of zeros. The timer is the
 * safety net: whatever happens to the animation, the true number is on screen
 * within a second. */
const useCountUp = (target: number, decimals: number, run: boolean) => {
  const [value, setValue] = useState(0);
  useEffect(() => {
    if (!run) return undefined;
    if (prefersReducedMotion()) {
      setValue(target);
      return undefined;
    }
    let raf = 0;
    const start = performance.now();
    const tick = (now: number) => {
      const p = Math.min((now - start) / 900, 1);
      setValue(target * (1 - Math.pow(1 - p, 3)));
      if (p < 1) raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    const settle = setTimeout(() => setValue(target), 1000);
    return () => {
      cancelAnimationFrame(raf);
      clearTimeout(settle);
    };
  }, [run, target]);
  return value.toFixed(decimals).replace(/\B(?=(\d{3})+(?!\d))/g, ',');
};

const Stat = ({
  loading,
  label,
  suffix,
  value,
}: {
  label: string;
  loading: string;
  suffix?: string;
  value?: number;
}) => {
  const decimals = value !== undefined && !Number.isInteger(value) ? 1 : 0;
  const shown = useCountUp(value ?? 0, decimals, value !== undefined);
  return (
    <div className='lazu-landing__stat'>
      <b>
        {value === undefined ? (
          <span className='lazu-landing__stat-wait'>{loading}</span>
        ) : (
          <>
            {shown}
            {suffix || ''}
          </>
        )}
      </b>
      <span>{label}</span>
    </div>
  );
};

/** Tilts the console shot up as it scrolls into place.
 *
 * The shot starts leaning back and settles flat as it reaches its resting
 * position, so the hero has one piece of movement tied to the scroll rather
 * than a timed animation that plays whether or not anyone is looking. */
const useShotTilt = (ref: React.RefObject<HTMLDivElement | null>) => {
  useEffect(() => {
    const node = ref.current;
    if (!node || typeof window === 'undefined') return undefined;
    if (prefersReducedMotion()) return undefined;
    let ticking = false;
    const apply = () => {
      const rect = node.getBoundingClientRect();
      const p = Math.min(
        Math.max(1 - (rect.top - 120) / (window.innerHeight * 0.7), 0),
        1,
      );
      node.style.transform = `rotateX(${(1 - p) * 10}deg) translateY(${
        (1 - p) * -6
      }px) scale(${0.975 + 0.025 * p})`;
      ticking = false;
    };
    const onScroll = () => {
      if (ticking) return;
      ticking = true;
      requestAnimationFrame(apply);
    };
    apply();
    window.addEventListener('scroll', onScroll, { passive: true });
    window.addEventListener('resize', onScroll);
    return () => {
      window.removeEventListener('scroll', onScroll);
      window.removeEventListener('resize', onScroll);
    };
  }, [ref]);
};

/** True as soon as the component has mounted.
 *
 * The quota meter is above the fold on most screens, so it does not need an
 * IntersectionObserver — and an observer would never fire in a background tab,
 * leaving the bar at zero width. A mount effect always runs. */
const useAfterMount = () => {
  const [ready, setReady] = useState(false);
  useEffect(() => setReady(true), []);
  return ready;
};

const LazuLanding = () => {
  const { t } = useTranslation('marketing');
  const [statusState] = useContext(StatusContext);
  const actualTheme = useActualTheme();
  const shotRef = useRef<HTMLDivElement | null>(null);
  const ready = useAfterMount();
  useShotTilt(shotRef);

  const stats = useMemo(
    () => asArray<StatItem>(t('landing.stats.items', { returnObjects: true })),
    [t],
  );
  const features = useMemo(
    () =>
      asArray<FeatureCopy>(
        t('landing.features.items', { returnObjects: true }),
      ),
    [t],
  );
  const steps = useMemo(
    () => asArray<StepCopy>(t('landing.steps.items', { returnObjects: true })),
    [t],
  );
  const faqs = useMemo(
    () => asArray<FaqCopy>(t('landing.faq.items', { returnObjects: true })),
    [t],
  );
  const facts = useLandingFacts();

  const statValue = (key: string) =>
    key === 'models'
      ? facts.modelCount
      : key === 'vendors'
        ? facts.vendorCount
        : facts.availablePct;
  // The health call finishing empty must drop the metric, not pin "loading".
  const statVisible = (key: string) =>
    key !== 'available' || facts.availabilityState !== 'unavailable';
  const statSuffix = (key: string) => (key === 'available' ? '%' : undefined);

  const docsLink = useMemo(
    () =>
      import.meta.env.VITE_BRAND_DOCS_URL ||
      statusState?.status?.docs_link ||
      getDocsBaseURL(),
    [statusState],
  );

  const barWidth = (pct: number) => (ready ? `${pct}%` : '0%');
  const balance = useCountUp(1240, 2, ready);
  const roleLabel = (role: string) =>
    role === 'admin'
      ? t('landing.shot.roleAdmin')
      : role === 'service'
        ? t('landing.shot.roleService')
        : t('landing.shot.roleMember');

  /* Each entry of the ledger is a real surface from the console, set as lines
     on the page rather than as a card: hairlines, serif figures, mono ids. */
  const panels = [
    // 01 — every model behind one account, straight from /api/pricing
    <div className='lazu-landing__panel' key='models'>
      <div className='lazu-landing__panel-h'>
        <b>{features[0]?.panelTitle}</b>
        <span>
          {facts.modelCount === undefined
            ? ''
            : t('landing.features.panelCount', { count: facts.modelCount })}
        </span>
      </div>
      {facts.models.length ? (
        facts.models.map((model) => (
          <div className='lazu-landing__row' key={model.name}>
            <span className='lazu-landing__row-g'>
              <b>{model.name}</b>
              <span className='lazu-landing__row-s'>{model.vendor}</span>
            </span>
            {/* No per-model health exists in these responses — a green
                "available" pill here would be a claim we cannot back. Show
                the other real figure instead. */}
            <span className='lazu-landing__money'>
              {model.input === undefined ? '' : `$${model.input} / 1M in`}
            </span>
            <span className='lazu-landing__money'>
              {model.output === undefined ? '' : `$${model.output} / 1M out`}
            </span>
          </div>
        ))
      ) : (
        <div className='lazu-landing__row lazu-landing__row--wait'>
          <span className='lazu-landing__row-g'>
            {t(
              facts.state === 'loading'
                ? 'landing.features.panelEmpty'
                : 'landing.features.panelUnavailable',
            )}
          </span>
        </div>
      )}
    </div>,

    // 02 — a cap that actually stops
    <div className='lazu-landing__panel' key='quota'>
      <div className='lazu-landing__panel-h'>
        <b>{features[1]?.panelTitle}</b>
        <span>{features[1]?.panelMeta}</span>
      </div>
      <div className='lazu-landing__quota-edit'>
        <div className='lazu-landing__lbl'>
          {t('landing.features.quotaEditor.thisMonth')}
        </div>
        <div className='lazu-landing__chips'>
          {QUOTA_PRESETS.map((preset) => (
            <span
              className={`lazu-landing__pill${
                preset === '$100' ? ' lazu-landing__pill--pick' : ''
              }`}
              key={preset}
            >
              {preset}
            </span>
          ))}
          <span className='lazu-landing__pill'>
            {t('landing.features.quotaEditor.unlimited')}
          </span>
        </div>
        <div className='lazu-landing__lbl lazu-landing__lbl--gap'>
          {t('landing.features.quotaEditor.used')}
        </div>
        <div className='lazu-landing__spent'>
          <b>$91.00</b>
          <span>{t('landing.features.quotaEditor.warn')}</span>
        </div>
        <div className='lazu-landing__bar'>
          <i className='is-hot' style={{ width: barWidth(91) }} />
        </div>
      </div>
    </div>,

    // 03 — one key per person
    <div className='lazu-landing__panel' key='keys'>
      <div className='lazu-landing__panel-h'>
        <b>{features[2]?.panelTitle}</b>
        <span>{features[2]?.panelMeta}</span>
      </div>
      {KEYS.map((key) => (
        <div className='lazu-landing__row' key={key.id}>
          <span className='lazu-landing__row-g'>
            <b>{key.name}</b>
            <span className='lazu-landing__row-s'>{key.id}</span>
          </span>
          <span
            className={`lazu-landing__pill ${
              key.off ? 'lazu-landing__pill--off' : 'lazu-landing__pill--ok'
            }`}
          >
            {key.off
              ? t('landing.features.keyStatusOff')
              : t('landing.features.keyStatusOk')}
          </span>
        </div>
      ))}
    </div>,

    // 04 — a bill you can read
    <div className='lazu-landing__panel' key='usage'>
      <div className='lazu-landing__panel-h'>
        <b>{features[3]?.panelTitle}</b>
        <span>{features[3]?.panelMeta}</span>
      </div>
      {SPEND.map((row) => (
        <div className='lazu-landing__row' key={row.name}>
          <span className='lazu-landing__row-g'>
            <b>{row.name}</b>
          </span>
          <span className='lazu-landing__money lazu-landing__money--sum'>
            {row.total}
          </span>
        </div>
      ))}
    </div>,
  ];

  return (
    <div className='lazu-landing'>
      <div className='lazu-landing__page'>
        <main className='lazu-landing__main'>
          {/* ---- Hero: the statement, then the console it describes ---- */}
          <section className='lazu-landing__hero'>
            <div className='lazu-landing__wrap'>
              <Reveal delay={1} className='lazu-landing__eyebrow'>
                {t('landing.hero.eyebrow', {
                  defaultValue: t('landing.hero.whatBold'),
                })}
              </Reveal>

              <div className='lazu-landing__hero-row'>
                <Reveal as='h1' delay={1} className='lazu-landing__headline'>
                  {t('landing.hero.headline')}
                  {t('landing.hero.headlineEm', { defaultValue: '' }) ? (
                    <em>{t('landing.hero.headlineEm')}</em>
                  ) : null}
                </Reveal>

                <div className='lazu-landing__hero-aside'>
                  <Reveal as='p' delay={2} className='lazu-landing__what'>
                    {t('landing.hero.whatBefore')}
                    <b>{t('landing.hero.whatBold')}</b>
                    {t('landing.hero.whatAfter')}
                  </Reveal>

                  <Reveal delay={3} className='lazu-landing__cta-row'>
                    <Link
                      className='lazu-landing__btn lazu-landing__btn--primary'
                      to='/login'
                    >
                      {t('landing.hero.ctaPrimary')}
                    </Link>
                    <Link
                      className='lazu-landing__btn lazu-landing__btn--ghost'
                      to='/console'
                    >
                      {t('landing.hero.ctaSecondary')}
                    </Link>
                    <span className='lazu-landing__free'>
                      {t('landing.hero.free')}
                    </span>
                  </Reveal>
                </div>
              </div>

              {/* The console, at the page the sentence above is about. It is a
                  drawing of a real surface, not a screenshot, so it stays in
                  the visitor's language and never shows a stale build. */}
              <Reveal delay={4} className='lazu-landing__shot-wrap'>
                <div className='lazu-landing__shot' ref={shotRef}>
                  <div className='lazu-landing__shot-bar'>
                    <span className='lazu-landing__dots'>
                      <i />
                      <i />
                      <i />
                    </span>
                    <span className='lazu-landing__shot-url'>
                      /console/project/members
                    </span>
                    {/* public-web-system.md composition rule 4: a cost figure
                        that no API measured has to say so. The people and the
                        money in this drawing are illustrative; the three
                        figures under the rule are the live ones. */}
                    <span className='lazu-landing__shot-demo'>
                      {t('landing.shot.demo', { defaultValue: 'demo data' })}
                    </span>
                  </div>

                  <div className='lazu-landing__console'>
                    <div className='lazu-landing__side'>
                      <BrandLogo
                        variant='primary'
                        tone={actualTheme === 'dark' ? 'paper' : 'graphite'}
                        size='footer'
                        alt='Lazu'
                        className='lazu-landing__side-brand'
                      />
                      <div className='lazu-landing__side-grp'>
                        {t('landing.shot.navWorkspace')}
                      </div>
                      <div className='lazu-landing__side-item'>
                        {t('landing.shot.navOverview')}
                      </div>
                      <div className='lazu-landing__side-item'>
                        {t('landing.shot.navModels')}
                      </div>
                      <div className='lazu-landing__side-item'>
                        {t('landing.shot.navKeys')}
                      </div>
                      <div className='lazu-landing__side-item'>
                        {t('landing.shot.navUsage')}
                      </div>
                      <div className='lazu-landing__side-grp'>
                        {t('landing.shot.navOrg')}
                      </div>
                      <div className='lazu-landing__side-item is-on'>
                        {t('landing.shot.navMembers')}
                      </div>
                      <div className='lazu-landing__side-item'>
                        {t('landing.shot.navSettings')}
                      </div>
                    </div>

                    <div className='lazu-landing__body'>
                      <div className='lazu-landing__body-head'>
                        <span className='lazu-landing__body-title'>
                          <h4>{t('landing.shot.title')}</h4>
                          <span className='lazu-landing__body-sub'>
                            {t('landing.shot.sub')}
                          </span>
                        </span>
                        <span className='lazu-landing__balance'>
                          <b>${balance}</b>
                          <span>{t('landing.shot.balanceNote')}</span>
                        </span>
                      </div>

                      <div className='lazu-landing__tbl'>
                        <div className='lazu-landing__th'>
                          <span>{t('landing.shot.colMember')}</span>
                          <span>{t('landing.shot.colUsed')}</span>
                          <span>{t('landing.shot.colQuota')}</span>
                          <span />
                        </div>
                        {MEMBERS.map((member) => (
                          <div className='lazu-landing__tr' key={member.name}>
                            <span className='lazu-landing__who'>
                              <span
                                className={`lazu-landing__av${
                                  member.role === 'service'
                                    ? ' lazu-landing__av--service'
                                    : ''
                                }`}
                              >
                                {member.initial}
                              </span>
                              <span>
                                <b>{member.name}</b>
                                <span>{roleLabel(member.role)}</span>
                              </span>
                            </span>
                            <span className='lazu-landing__num'>
                              {member.used}
                            </span>
                            <span className='lazu-landing__barwrap'>
                              {/* No cap means no meter to read: the rule stays
                                  empty rather than drawing a share of nothing. */}
                              <span className='lazu-landing__meter'>
                                {member.cap ? (
                                  <i
                                    className={member.pct >= 85 ? 'is-hot' : ''}
                                    style={{ width: barWidth(member.pct) }}
                                  />
                                ) : null}
                              </span>
                              <span>
                                {member.cap
                                  ? t('landing.shot.capHint', {
                                      cap: member.cap,
                                      pct: member.pct,
                                    })
                                  : t('landing.shot.unlimited')}
                              </span>
                            </span>
                            <span className='lazu-landing__act'>
                              {/* The shot is a drawing; a link here would land
                                  on a console the visitor cannot reach yet. */}
                              <span className='lazu-landing__linkish'>
                                {t('landing.shot.edit')}
                              </span>
                            </span>
                          </div>
                        ))}
                      </div>
                    </div>
                  </div>
                </div>
              </Reveal>

              {/* The strip is three figures or nothing: if either live source
                  cannot be fetched it goes away, rather than showing two
                  numbers and a gap or saying "loading" forever. */}
              <Reveal
                delay={4}
                className='lazu-landing__stats'
                hidden={
                  facts.state === 'unavailable' ||
                  facts.availabilityState === 'unavailable'
                }
              >
                {stats
                  .filter((item) => statVisible(item.key))
                  .map((item) => (
                    <Stat
                      key={item.key}
                      label={item.label}
                      loading={t('landing.stats.loading')}
                      suffix={statSuffix(item.key)}
                      value={statValue(item.key)}
                    />
                  ))}
              </Reveal>
            </div>
          </section>

          {/* ---- The ledger: four entries, each with the real surface ---- */}
          <section className='lazu-landing__section'>
            <div className='lazu-landing__wrap'>
              <Reveal className='lazu-landing__sec-head'>
                <div className='lazu-landing__eyebrow'>
                  {t('landing.features.eyebrow')}
                </div>
                <h2>{t('landing.features.title')}</h2>
                <p>{t('landing.features.lede')}</p>
              </Reveal>

              {features.map((feature, index) => (
                <Reveal className='lazu-landing__feat' key={feature.title}>
                  <div className='lazu-landing__feat-n'>
                    {String(index + 1).padStart(2, '0')}
                  </div>
                  <div className='lazu-landing__feat-txt'>
                    <h3>{feature.title}</h3>
                    <p>{feature.body}</p>
                    <ul>
                      {asArray<string>(feature.bullets).map((line) => (
                        <li key={line}>{line}</li>
                      ))}
                    </ul>
                  </div>
                  {panels[index]}
                </Reveal>
              ))}
            </div>
          </section>

          {/* ---- Getting started: three numbered lines ---- */}
          <section className='lazu-landing__section'>
            <div className='lazu-landing__wrap'>
              <Reveal className='lazu-landing__sec-head'>
                <div className='lazu-landing__eyebrow'>
                  {t('landing.steps.eyebrow')}
                </div>
                <h2>{t('landing.steps.title')}</h2>
              </Reveal>
              <Reveal className='lazu-landing__steps'>
                {steps.map((step, index) => (
                  <div className='lazu-landing__step' key={step.title}>
                    <span className='lazu-landing__step-n'>{index + 1}</span>
                    <b>{step.title}</b>
                    <p>{step.body}</p>
                  </div>
                ))}
              </Reveal>
            </div>
          </section>

          {/* ---- FAQ ---- */}
          <section className='lazu-landing__section'>
            <div className='lazu-landing__wrap'>
              <Reveal className='lazu-landing__sec-head'>
                <div className='lazu-landing__eyebrow'>
                  {t('landing.faq.eyebrow')}
                </div>
                <h2>{t('landing.faq.title')}</h2>
              </Reveal>
              <Reveal className='lazu-landing__faq'>
                {faqs.map((item) => (
                  <div className='lazu-landing__faq-q' key={item.q}>
                    <b>{item.q}</b>
                    <p>{item.a}</p>
                  </div>
                ))}
              </Reveal>
            </div>
          </section>

          {/* ---- Final ---- */}
          <section className='lazu-landing__final'>
            <div className='lazu-landing__column'>
              <Reveal as='h2'>{t('landing.final.title')}</Reveal>
              <Reveal as='p'>{t('landing.final.body')}</Reveal>
              <Reveal className='lazu-landing__cta-row'>
                <Link
                  className='lazu-landing__btn lazu-landing__btn--primary'
                  to='/login'
                >
                  {t('landing.final.ctaPrimary')}
                </Link>
                <a
                  className='lazu-landing__btn lazu-landing__btn--ghost'
                  href={docsLink}
                  rel='noreferrer'
                  target='_blank'
                >
                  {t('landing.final.ctaSecondary')}
                </a>
              </Reveal>
            </div>
          </section>
        </main>
      </div>
    </div>
  );
};

export default LazuLanding;
