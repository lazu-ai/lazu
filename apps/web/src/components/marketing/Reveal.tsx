import { useEffect, useRef } from 'react';
import { usePrerender } from '../../context/Prerender';
import type React from 'react';

// IntersectionObserver-based fade-in wrapper. Originally lived inside
// LazuLanding; lifted into a shared marketing module so PublicHeader
// (and any future public surface) can reuse the same animation
// primitive without re-implementing the observer.
//
// `Tag` is constrained to an intrinsic HTML element name so consumers
// can pick `'nav' | 'section' | 'div' | …` without each call site
// having to spell out the matching props type. React's
// `JSX.IntrinsicElements[Tag]` resolves to the right attribute shape
// for whatever element name is passed.
type RevealProps<Tag extends keyof JSX.IntrinsicElements = 'div'> = {
  delay?: 1 | 2 | 3 | 4;
  as?: Tag;
  className?: string;
  children?: React.ReactNode;
} & Omit<JSX.IntrinsicElements[Tag], 'ref' | 'className' | 'children'>;

const Reveal = <Tag extends keyof JSX.IntrinsicElements = 'div'>({
  delay = 1,
  as,
  className = '',
  ...props
}: RevealProps<Tag>) => {
  const Component = (as ?? 'div') as React.ElementType;
  const ref = useRef<HTMLElement | null>(null);
  const { route: prerenderRoute } = usePrerender();
  const prerendered = Boolean(prerenderRoute);

  useEffect(() => {
    if (prerendered) return undefined;
    const el = ref.current;
    if (!el) return undefined;
    const io = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) {
            entry.target.classList.add('is-in');
            io.unobserve(entry.target);
          }
        });
      },
      { threshold: 0.05, rootMargin: '0px 0px -20px 0px' },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [prerendered]);

  return (
    <Component
      ref={ref}
      className={`lazu-landing__reveal lazu-landing__reveal--d${delay} ${prerendered ? 'is-in' : ''} ${className}`.trim()}
      {...props}
    />
  );
};

export default Reveal;
