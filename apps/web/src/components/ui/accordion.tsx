import { Accordion as AccordionPrimitive } from '@base-ui/react/accordion';
import { ChevronDown } from 'lucide-react';
import React from 'react';
import { cn } from '../../lib/cn';

export const Accordion = AccordionPrimitive.Root;

export const AccordionItem = React.forwardRef<
  HTMLDivElement,
  AccordionPrimitive.Item.Props
>(({ className, ...props }, ref) => (
  <AccordionPrimitive.Item
    ref={ref}
    className={cn('lazu-accordion-item', className)}
    {...props}
  />
));

AccordionItem.displayName = 'AccordionItem';

export const AccordionTrigger = React.forwardRef<
  HTMLButtonElement,
  AccordionPrimitive.Trigger.Props
>(({ className, children, ...props }, ref) => (
  <AccordionPrimitive.Header className='lazu-accordion-header'>
    <AccordionPrimitive.Trigger
      ref={ref}
      className={cn('lazu-accordion-trigger', className)}
      {...props}
    >
      <span className='lazu-accordion-trigger-label'>{children}</span>
      <ChevronDown
        aria-hidden='true'
        className='lazu-accordion-trigger-icon'
        size={16}
      />
    </AccordionPrimitive.Trigger>
  </AccordionPrimitive.Header>
));

AccordionTrigger.displayName = 'AccordionTrigger';

export const AccordionContent = React.forwardRef<
  HTMLDivElement,
  AccordionPrimitive.Panel.Props
>(({ className, children, ...props }, ref) => (
  <AccordionPrimitive.Panel
    ref={ref}
    className={cn('lazu-accordion-content', className)}
    {...props}
  >
    <div className='lazu-accordion-content-inner'>{children}</div>
  </AccordionPrimitive.Panel>
));

AccordionContent.displayName = 'AccordionContent';
