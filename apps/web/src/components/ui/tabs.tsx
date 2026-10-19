import { Tabs as TabsPrimitive } from '@base-ui/react/tabs';
import React from 'react';
import { cn } from '../../lib/cn';

export const Tabs = TabsPrimitive.Root;

export const TabsList = React.forwardRef<
  HTMLDivElement,
  TabsPrimitive.List.Props & { variant?: 'pill' | 'line' }
>(({ className, variant = 'pill', ...props }, ref) => (
  <TabsPrimitive.List
    ref={ref}
    className={cn(
      'lazu-tabs-list',
      variant === 'line' && 'lazu-tabs-list--line',
      className,
    )}
    {...props}
  />
));

TabsList.displayName = 'TabsList';

export const TabsTrigger = React.forwardRef<
  HTMLButtonElement,
  TabsPrimitive.Tab.Props
>(({ className, value, ...props }, ref) => {
  return (
    <TabsPrimitive.Tab
      ref={ref}
      className={cn('lazu-tabs-trigger', className)}
      value={value}
      {...props}
    />
  );
});

TabsTrigger.displayName = 'TabsTrigger';

export const TabsContent = React.forwardRef<
  HTMLDivElement,
  TabsPrimitive.Panel.Props
>(({ className, ...props }, ref) => {
  return (
    <TabsPrimitive.Panel
      ref={ref}
      className={cn('lazu-tabs-content', className)}
      {...props}
    />
  );
});

TabsContent.displayName = 'TabsContent';
