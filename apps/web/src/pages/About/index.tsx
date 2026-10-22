import { useTranslation } from 'react-i18next';
import { Link, useLocation } from 'react-router-dom';
import './about.css';
import {
  localizedMarketingPath,
  splitMarketingPath,
} from '../../helpers/marketingLocale';

type Fact = { label: string; value: string };
type MachineEntry = { path: string; text: string };

const asArray = <T,>(value: unknown): T[] =>
  Array.isArray(value) ? (value as T[]) : [];

// Who runs Lazu, where it is registered and how to reach it — verifiable
// facts, mirrored in the Organization schema (legalName / address /
// contactPoint). Design: archive:brand/explorations/seo-surfaces-2026-09/About.dc.html
const About = () => {
  const { t } = useTranslation('marketing');
  const { locale } = splitMarketingPath(useLocation().pathname);
  const facts = asArray<Fact>(t('about.facts', { returnObjects: true }));
  const machine = asArray<MachineEntry>(
    t('about.machine', { returnObjects: true }),
  );
  const termsHref = localizedMarketingPath('/terms', locale);

  return (
    <main className='about-page'>
      <section className='about-page__head'>
        <span className='about-page__eyebrow'>{t('about.eyebrow')}</span>
        <h1 className='about-page__title'>{t('about.title')}</h1>
        <p className='about-page__lede'>{t('about.lede')}</p>
      </section>

      <dl className='about-page__ledger'>
        {facts.map((fact) => (
          <div className='about-page__row' key={fact.label}>
            <dt>{fact.label}</dt>
            <dd>
              {fact.value.includes('@') ? (
                <a href={`mailto:${fact.value}`}>{fact.value}</a>
              ) : fact.value.includes('[[terms]]') ? (
                fact.value.split('[[terms]]').map((part, index) => (
                  <span key={index}>
                    {index > 0 ? (
                      <Link reloadDocument to={termsHref}>
                        {t('about.termsLink')}
                      </Link>
                    ) : null}
                    {part}
                  </span>
                ))
              ) : (
                fact.value
              )}
            </dd>
          </div>
        ))}
      </dl>

      <section className='about-page__machine'>
        <h2 className='about-page__h2'>{t('about.machineTitle')}</h2>
        <p className='about-page__note'>{t('about.machineLede')}</p>
        <dl className='about-page__ledger'>
          {machine.map((entry) => (
            <div className='about-page__row' key={entry.path}>
              <dt>
                <a className='about-page__mono' href={entry.path}>
                  {entry.path}
                </a>
              </dt>
              <dd className='about-page__muted'>{entry.text}</dd>
            </div>
          ))}
        </dl>
      </section>
    </main>
  );
};

export default About;
