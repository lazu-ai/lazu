import clsx from 'clsx';
import { lazy, Suspense, useMemo } from 'react';
import './announcement-timeline.css';
import { ANNOUNCEMENT_TYPE_CONFIG } from '../../../constants/dashboard.constants';
import {
  normalizeAnnouncements,
  type Announcement,
  type NormalizedAnnouncement,
} from '../../../helpers/announcement';
import ScrollableContainer, {
  type ScrollableContainerProps,
} from '../ScrollableContainer';
import type { TFunction } from 'i18next';
import type { CSSProperties, ComponentType, ReactNode } from 'react';
import { Empty } from '@/components/ui/console-primitives';
import {
  IllustrationNoContent,
  IllustrationNoContentDark,
} from '@/components/ui/illustrations';

type LegacyProps = {
  children?: ReactNode;
  [key: string]: unknown;
};

const LegacyEmpty = Empty as unknown as ComponentType<LegacyProps>;

type AnnouncementType = keyof typeof ANNOUNCEMENT_TYPE_CONFIG;
type Translate = TFunction | ((key: string) => string);

type AnnouncementTimelineItemProps = {
  item: NormalizedAnnouncement;
  index: number;
  isLast: boolean;
  compact?: boolean;
  showUnreadState?: boolean;
  t: Translate;
};

export type AnnouncementTimelineProps = {
  announcements?: Announcement[];
  /* Stagger the cards in on mount. Only surfaces that appear on demand (the
     What's New panel) want this; an always-on dashboard list would replay it
     on every re-render for no reason. */
  animateEntrance?: boolean;
  maxHeight?: ScrollableContainerProps['maxHeight'];
  limit?: number;
  compact?: boolean;
  scrollable?: boolean;
  showUnreadState?: boolean;
  emptyTitle?: ReactNode;
  emptyDescription?: ReactNode;
  className?: string;
  contentClassName?: string;
  emptyIllustration?: ReactNode;
  emptyIllustrationDark?: ReactNode;
  t?: Translate;
};

const MarkdownRenderer = lazy(
  () => import('../../common/markdown/MarkdownRenderer'),
);

const DEFAULT_EMPTY_ILLUSTRATION_STYLE: CSSProperties = {
  width: 120,
  height: 120,
};

const fallbackT: Translate = (key: string) => key;

const getTypeConfig = (type?: string) =>
  ANNOUNCEMENT_TYPE_CONFIG[type as AnnouncementType] ||
  ANNOUNCEMENT_TYPE_CONFIG.default;

const toMarkdownContent = (value: unknown): string => {
  if (typeof value === 'string') return value;
  if (value == null) return '';
  return String(value);
};

const AnnouncementTimelineItem = ({
  item,
  index,
  isLast,
  compact = false,
  showUnreadState = true,
  t,
}: AnnouncementTimelineItemProps) => {
  const config = getTypeConfig(item.type);
  const extraContent = toMarkdownContent(item.extra);
  const affectedModels = Array.isArray(item.affected_models)
    ? item.affected_models.filter(
        (model): model is string => typeof model === 'string' && Boolean(model),
      )
    : [];
  const effectiveDate =
    typeof item.effective_date === 'string'
      ? item.effective_date.slice(0, 10)
      : '';
  const hasModelMetadata =
    item.type === 'model_update' &&
    (affectedModels.length > 0 || Boolean(effectiveDate));

  return (
    <article
      className={clsx(
        'announcement-card',
        compact && 'announcement-card--compact',
      )}
      data-tone={config.tone}
      data-unread={item.isUnread && showUnreadState ? '' : undefined}
      style={{ '--announcement-index': index } as CSSProperties}
    >
      <span aria-hidden='true' className='announcement-card__hairline' />

      <div className='announcement-card__row'>
        <div aria-hidden='true' className='announcement-card__rail'>
          <span className='announcement-card__mark'>
            <i />
          </span>
          {!isLast ? <span className='announcement-card__line' /> : null}
        </div>

        <div className='announcement-card__copy'>
          <div className='announcement-card__chips'>
            <span className='announcement-card__type'>
              {t(config.labelKey)}
            </span>
            {item.isUnread && showUnreadState ? (
              <span className='announcement-card__unread'>
                {t('legacy.i18n.unread.1e230aa201')}
              </span>
            ) : null}
          </div>

          {item.title ? (
            <h4 className='announcement-card__title'>{item.title}</h4>
          ) : null}

          <div className='announcement-card__time'>
            <b>{item.relative || t('legacy.i18n.just_now.9e636642d6')}</b>
            {item.time ? <span>{item.time}</span> : null}
          </div>

          <div className='announcement-card__body'>
            <Suspense
              fallback={<div className='announcement-card__pending'>...</div>}
            >
              <MarkdownRenderer
                content={item.content || ''}
                fontSize={compact ? 13 : 14}
              />
            </Suspense>
          </div>

          {hasModelMetadata ? (
            <dl className='announcement-card__metadata'>
              {affectedModels.length > 0 ? (
                <div>
                  <dt>{t('page.console.announcements.affectedModels')}</dt>
                  <dd>
                    {affectedModels.map((model) => (
                      <code key={model}>{model}</code>
                    ))}
                  </dd>
                </div>
              ) : null}
              {effectiveDate ? (
                <div>
                  <dt>{t('page.console.announcements.effectiveDate')}</dt>
                  <dd>
                    <time dateTime={effectiveDate}>{effectiveDate}</time>
                  </dd>
                </div>
              ) : null}
            </dl>
          ) : null}

          {extraContent ? (
            <div className='announcement-card__extra'>
              <b>{t('legacy.i18n.additional_notes.ee71941a80')}</b>
              <Suspense fallback={<span>{extraContent}</span>}>
                <MarkdownRenderer content={extraContent} fontSize={12} />
              </Suspense>
            </div>
          ) : null}
        </div>
      </div>
    </article>
  );
};

const AnnouncementTimeline = ({
  announcements = [],
  animateEntrance = false,
  maxHeight = '28rem',
  limit,
  compact = false,
  scrollable = true,
  showUnreadState = true,
  emptyTitle,
  emptyDescription,
  className = '',
  contentClassName = '',
  emptyIllustration = (
    <IllustrationNoContent style={DEFAULT_EMPTY_ILLUSTRATION_STYLE} />
  ),
  emptyIllustrationDark = (
    <IllustrationNoContentDark style={DEFAULT_EMPTY_ILLUSTRATION_STYLE} />
  ),
  t = fallbackT,
}: AnnouncementTimelineProps) => {
  const items = useMemo(() => {
    const normalized = normalizeAnnouncements(announcements);
    if (typeof limit === 'number') {
      return normalized.slice(0, limit);
    }
    return normalized;
  }, [announcements, limit]);

  if (items.length === 0) {
    return (
      <div className='flex justify-center items-center py-10'>
        <LegacyEmpty
          image={emptyIllustration}
          darkModeImage={emptyIllustrationDark}
          title={emptyTitle}
          description={emptyDescription}
        />
      </div>
    );
  }

  const timelineContent = (
    <div
      className={clsx(
        'announcement-timeline',
        animateEntrance && 'announcement-timeline--animated',
        contentClassName,
      )}
    >
      {items.map((item, index) => (
        <AnnouncementTimelineItem
          key={item.key || item.id || index}
          item={item}
          index={index}
          isLast={index === items.length - 1}
          compact={compact}
          showUnreadState={showUnreadState}
          t={t}
        />
      ))}
    </div>
  );

  if (!scrollable) {
    return <div className={className}>{timelineContent}</div>;
  }

  return (
    <ScrollableContainer
      maxHeight={maxHeight}
      className={className}
      contentClassName='pr-1'
    >
      {timelineContent}
    </ScrollableContainer>
  );
};

export default AnnouncementTimeline;
