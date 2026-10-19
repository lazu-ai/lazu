import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { getDocsBaseURL } from '../../helpers/brandUrls';
import './faq.css';
import type { FAQItem } from './FAQSchema';

const slugify = (value: string) =>
  value
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/(^-|-$)/g, '');

/* Which topic each question belongs to, by its position in the locale array.
 * Every locale translates the same list in the same order, so the index is
 * stable across languages while the questions themselves are not. */
const GROUP_IDS = ['gateway', 'models', 'billing', 'limits'] as const;
type GroupId = (typeof GROUP_IDS)[number];

const GROUP_OF: GroupId[] = [
  'gateway', // self-host
  'models', // how many models
  'models', // which providers
  'gateway', // migrate from OpenAI
  'models', // Claude and GPT from China
  'billing', // how is pricing calculated
  'billing', // stable and agent lanes
  'billing', // payment methods
  'limits', // data privacy
  'gateway', // streaming
  'gateway', // native Anthropic API
  'limits', // rate limits
  'billing', // monitor usage and cost
  'limits', // troubleshoot errors
  'billing', // new-customer offer
  'billing', // referral reward
];

const FAQ = () => {
  const { t } = useTranslation('marketing');
  const items = useMemo(
    () => asFAQItems(t('faq.items', { returnObjects: true })),
    [t],
  );
  const itemIds = useMemo(
    () => items.map((item, index) => slugify(item.q) || `faq-${index + 1}`),
    [items],
  );

  /* Questions in their locale order, gathered under the four topics. A topic
     with nothing in it is not rendered, so a shorter locale never leaves an
     empty heading behind. */
  const groups = useMemo(
    () =>
      GROUP_IDS.map((id) => ({
        id,
        label: t(`faq.groups.${id}`, { defaultValue: '' }),
        entries: items
          .map((item, index) => ({ id: itemIds[index], index, item }))
          .filter(({ index }) => (GROUP_OF[index] ?? 'gateway') === id),
      })).filter((group) => group.entries.length > 0),
    [items, itemIds, t],
  );

  return (
    <div className='faq-page'>
      <div className='faq-page__wrap'>
        <header className='faq-page__header'>
          <div className='faq-page__head-copy'>
            <span className='faq-page__eyebrow'>{t('faq.eyebrow')}</span>
            <h1 className='faq-page__title'>{t('faq.title')}</h1>
            <p className='faq-page__lede'>{t('faq.subtitle')}</p>
          </div>
          <div className='faq-page__head-side'>
            <span className='faq-page__meta'>{t('faq.updatedLabel')}</span>
            <div className='faq-page__actions'>
              <Link
                className='faq-page__btn faq-page__btn--primary'
                to='/login'
              >
                {t('faq.getKey')}
              </Link>
              <a
                className='faq-page__btn'
                href={getDocsBaseURL()}
                target='_blank'
                rel='noopener noreferrer'
              >
                {t('faq.readDocs')}
              </a>
            </div>
          </div>
        </header>

        <div className='faq-page__grid'>
          <nav className='faq-page__toc' aria-label={t('faq.tocLabel')}>
            <span className='faq-page__toc-title'>{t('faq.topics')}</span>
            {groups.map((group) => (
              <a href={`#${group.id}`} key={group.id}>
                {group.label}
              </a>
            ))}
          </nav>

          <div className='faq-page__groups'>
            {groups.map((group) => (
              <section className='faq-page__group' id={group.id} key={group.id}>
                <h2 className='faq-page__group-title'>{group.label}</h2>
                {/* The answers are open. A question worth an accordion is a
                    question the reader has to click to find, and a crawler
                    never clicks. */}
                {group.entries.map(({ id, item }) => (
                  <div className='faq-page__qa' id={id} key={id}>
                    <h3 className='faq-page__q'>{item.q}</h3>
                    <p className='faq-page__a'>{item.a}</p>
                  </div>
                ))}
              </section>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
};

const asFAQItems = (value: unknown): FAQItem[] =>
  Array.isArray(value) ? (value as FAQItem[]) : [];

export default FAQ;
