import { type ReactNode, useContext } from 'react';
import { ControlAppearanceContext } from './control-appearance';

type ConsolePageProps = {
  appearance?: 'default' | 'review';
  title: ReactNode;
  description?: ReactNode;
  badge?: ReactNode;
  actions?: ReactNode;
  /* One to three numbers that answer the page's own question, set beside the
   * title: 成员 1, 余额 $1.00. A page head with a heading and nothing else is
   * furniture; with the page's own numbers in it, the top of every page says
   * something before you read a row. */
  stats?: ReactNode;
  /* The page's peer views. They sit on the head's own rule, so a page with
   * tabs has one line under its head and a page without tabs has none. */
  tabs?: ReactNode;
  children: ReactNode;
  className?: string;
};

const ConsolePage = ({
  appearance,
  title,
  description,
  badge,
  actions,
  stats,
  tabs,
  children,
  className,
}: ConsolePageProps) => {
  const inheritedAppearance = useContext(ControlAppearanceContext);
  return (
    <ControlAppearanceContext.Provider
      value={appearance ?? inheritedAppearance}
    >
      <section
        className={`console-page-shell${appearance === 'review' ? ' console-review' : ''}${className ? ` ${className}` : ''}`}
        data-has-tabs={tabs ? 'true' : undefined}
      >
        <header className='console-page-topbar'>
          <div className='console-page-topbar__copy'>
            <div className='console-page-topbar__title-row'>
              <h1 className='console-page-topbar__title'>{title}</h1>
              {stats ? (
                <div className='console-page-topbar__stats'>{stats}</div>
              ) : null}
              {badge ? (
                <span className='console-page-topbar__badge'>{badge}</span>
              ) : null}
            </div>
            {description ? (
              <div className='console-page-topbar__description'>
                {description}
              </div>
            ) : null}
          </div>
          {actions ? (
            <div className='console-page-topbar__actions'>{actions}</div>
          ) : null}
        </header>
        {tabs ? <div className='console-page-tabs'>{tabs}</div> : null}
        <div className='console-page-body'>{children}</div>
      </section>
    </ControlAppearanceContext.Provider>
  );
};

export default ConsolePage;
