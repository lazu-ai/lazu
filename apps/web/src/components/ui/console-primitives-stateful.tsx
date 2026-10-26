import React from 'react';
import {
  Checkbox,
  DatePicker,
  Input,
  InputNumber,
  RadioGroup,
  Select,
  Switch,
  TextArea,
  FormContext,
  asDomProps,
  getCollapsePanelKey,
  normalizeCollapseKeys,
  valueFromEvent,
  type ConsoleComponent,
  type ConsolePrimitiveProps,
  type FormApi,
  type FormContextValue,
  type FormProps,
  type FormRule,
  type FormValues,
} from './console-primitives-core';
import { cn } from '../../lib/cn';

function getFieldValue(
  context: FormContextValue | null,
  field: string | undefined,
  initValue: unknown,
) {
  if (!context || !field) return initValue;
  if (Object.prototype.hasOwnProperty.call(context.values, field)) {
    return context.values[field];
  }
  return initValue ?? '';
}

function FormFieldShell({ children, extraText, label }: ConsolePrimitiveProps) {
  return (
    <label className='lazu-form-field'>
      {label ? <span className='lazu-form-field__label'>{label}</span> : null}
      {children}
      {extraText ? (
        <span className='lazu-form-field__help'>{extraText}</span>
      ) : null}
    </label>
  );
}

function useRegisterField(field?: string, rules?: FormRule[]) {
  const context = React.useContext(FormContext);
  React.useEffect(() => {
    if (!context || !field) return undefined;
    context.register(field, rules);
    return () => context.unregister(field);
  }, [context, field, rules]);
  return context;
}

function createFormControl(
  Control: React.ComponentType<ConsolePrimitiveProps>,
  valueProp: 'checked' | 'value' = 'value',
): ConsoleComponent {
  return function FormControl({
    field,
    initValue,
    label,
    extraText,
    onChange,
    rules,
    ...props
  }: ConsolePrimitiveProps) {
    const context = useRegisterField(field, rules);
    const value = getFieldValue(context, field, initValue);
    const controlProps: ConsolePrimitiveProps = {
      ...props,
      [valueProp]: value,
      onChange: (nextValue: unknown, event: unknown) => {
        const normalized = valueFromEvent(nextValue);
        if (field) context?.setValue(field, normalized);
        onChange?.(
          nextValue && typeof nextValue === 'object' && 'target' in nextValue
            ? nextValue
            : normalized,
          nextValue && typeof nextValue === 'object' && 'target' in nextValue
            ? normalized
            : event,
        );
      },
    };

    return (
      <FormFieldShell label={label} extraText={extraText}>
        <Control {...controlProps} />
      </FormFieldShell>
    );
  };
}

type FormComponent = React.ForwardRefExoticComponent<
  FormProps & React.RefAttributes<FormApi>
> & {
  AutoComplete: ConsoleComponent;
  Checkbox: ConsoleComponent;
  DatePicker: ConsoleComponent;
  Input: ConsoleComponent;
  InputNumber: ConsoleComponent;
  RadioGroup: ConsoleComponent;
  Section: ConsoleComponent;
  Select: ConsoleComponent;
  Slot: ConsoleComponent;
  Switch: ConsoleComponent;
  TextArea: ConsoleComponent;
};

const Form = React.forwardRef<FormApi, FormProps>(
  (
    {
      children,
      getFormApi,
      initValues = {},
      layout: _layout,
      onSubmit,
      values,
      ...props
    },
    ref,
  ) => {
    const [internalValues, setInternalValues] = React.useState<FormValues>(
      () => ({ ...(values || initValues) }),
    );
    const rulesRef = React.useRef(new Map<string, FormRule[]>());

    React.useEffect(() => {
      if (values) setInternalValues({ ...values });
    }, [values]);

    const api = React.useMemo<FormApi>(
      () => ({
        getValue: (field) => internalValues[field],
        getValues: () => internalValues,
        setValue: (field, value) =>
          setInternalValues((current) => ({ ...current, [field]: value })),
        setValues: (nextValues) =>
          setInternalValues((current) => ({ ...current, ...nextValues })),
        validate: async () => {
          for (const [field, rules = []] of rulesRef.current.entries()) {
            const value = internalValues[field];
            for (const rule of rules) {
              if (!rule?.validator) continue;
              const valid = await rule.validator(rule, value);
              if (valid === false) throw new Error(rule.message || field);
            }
          }
          return internalValues;
        },
        register: (field, rules) => rulesRef.current.set(field, rules || []),
        unregister: (field) => {
          rulesRef.current.delete(field);
        },
      }),
      [internalValues],
    );

    React.useImperativeHandle(ref, () => api, [api]);
    React.useEffect(() => {
      getFormApi?.(api);
    }, [api, getFormApi]);

    return (
      <FormContext.Provider value={{ ...api, values: internalValues }}>
        <form
          {...props}
          className='lazu-form'
          onSubmit={(event) => {
            event.preventDefault();
            onSubmit?.(internalValues);
          }}
        >
          {children}
        </form>
      </FormContext.Provider>
    );
  },
) as FormComponent;

Form.displayName = 'Form';
Form.Input = createFormControl(Input);
Form.TextArea = createFormControl(TextArea);
Form.InputNumber = createFormControl(InputNumber);
Form.Switch = createFormControl(Switch, 'checked');
Form.Checkbox = createFormControl(Checkbox, 'checked');
Form.Select = createFormControl(Select);
Form.DatePicker = createFormControl(DatePicker);
Form.AutoComplete = createFormControl(Input);
Form.RadioGroup = function FormRadioGroup({
  field,
  initValue,
  label,
  extraText,
  onChange,
  children,
}: ConsolePrimitiveProps) {
  const context = useRegisterField(field, []);
  const value = getFieldValue(context, field, initValue);
  return (
    <FormFieldShell label={label} extraText={extraText}>
      <RadioGroup
        value={value}
        onChange={(nextValue: unknown) => {
          if (field) context?.setValue(field, nextValue);
          onChange?.(nextValue);
        }}
      >
        {children}
      </RadioGroup>
    </FormFieldShell>
  );
};
Form.Slot = function FormSlot({ children, label }: ConsolePrimitiveProps) {
  return <FormFieldShell label={label}>{children}</FormFieldShell>;
};
Form.Section = function FormSection({ children, text }: ConsolePrimitiveProps) {
  return (
    <section className='lazu-form-section'>
      {text ? <h3 className='lazu-form-section__title'>{text}</h3> : null}
      {children}
    </section>
  );
};

function getTabKey(
  child: React.ReactElement<ConsolePrimitiveProps>,
  fallback: number,
): React.Key {
  const candidate = child.props.itemKey ?? child.props.value ?? child.key;
  return typeof candidate === 'string' ||
    typeof candidate === 'number' ||
    typeof candidate === 'bigint'
    ? candidate
    : fallback;
}

function TabsBase({
  activeKey,
  children,
  defaultActiveKey,
  onChange,
  type,
  ...props
}: ConsolePrimitiveProps) {
  const panes = React.Children.toArray(children).filter(
    (child): child is React.ReactElement<ConsolePrimitiveProps> =>
      React.isValidElement<ConsolePrimitiveProps>(child),
  );
  const initialKey = Array.isArray(defaultActiveKey)
    ? defaultActiveKey[0]
    : defaultActiveKey;
  const [internalKey, setInternalKey] = React.useState<React.Key | null>(
    initialKey ?? (panes[0] ? getTabKey(panes[0], 0) : null),
  );
  const currentKey =
    (Array.isArray(activeKey) ? activeKey[0] : activeKey) ?? internalKey;
  return (
    <div
      {...asDomProps<HTMLDivElement>(props)}
      className={cn('lazu-tabs', type && `lazu-tabs--${type}`)}
    >
      <div className='lazu-tabs-list lazu-tabs__list'>
        {panes.map((pane, index) => {
          const key = getTabKey(pane, index);
          return (
            <button
              key={key}
              type='button'
              className='lazu-tabs-trigger'
              data-state={currentKey === key ? 'active' : 'inactive'}
              onClick={() => {
                setInternalKey(key);
                onChange?.(key);
              }}
            >
              {pane.props.tab}
            </button>
          );
        })}
      </div>
      {panes.map((pane, index) => {
        const key = getTabKey(pane, index);
        return currentKey === key ? (
          <div key={key} className='lazu-tabs-content lazu-tabs__content'>
            {pane.props.children}
          </div>
        ) : null;
      })}
    </div>
  );
}

const TabPane: ConsoleComponent = () => null;

const Tabs = TabsBase as typeof TabsBase & {
  TabPane: ConsoleComponent;
};
Tabs.TabPane = TabPane;

function CollapseBase({
  accordion = false,
  activeKey,
  children,
  className,
  collapseIcon,
  defaultActiveKey,
  expandIcon,
  onChange,
  ...props
}: ConsolePrimitiveProps) {
  const [internalActiveKeys, setInternalActiveKeys] = React.useState<string[]>(
    () => {
      const defaultKeys = normalizeCollapseKeys(defaultActiveKey);
      if (defaultKeys.length > 0) {
        return accordion ? defaultKeys.slice(0, 1) : defaultKeys;
      }

      const openKeys: string[] = [];
      React.Children.forEach(children, (child, index) => {
        if (
          React.isValidElement<ConsolePrimitiveProps>(child) &&
          child.props.defaultOpen
        ) {
          openKeys.push(getCollapsePanelKey(child, index));
        }
      });
      return accordion ? openKeys.slice(0, 1) : openKeys;
    },
  );

  const isControlled = activeKey !== undefined;
  const currentActiveKeys = isControlled
    ? normalizeCollapseKeys(activeKey)
    : internalActiveKeys;
  const activeKeysRef = React.useRef(currentActiveKeys);
  activeKeysRef.current = currentActiveKeys;

  const updateActiveKeys = (panelKey: string, isOpen: boolean) => {
    const activeKeys = activeKeysRef.current;

    if (accordion && !isOpen && !activeKeys.includes(panelKey)) {
      return;
    }

    const nextKeys = accordion
      ? isOpen
        ? [panelKey]
        : []
      : isOpen
        ? Array.from(new Set([...activeKeys, panelKey]))
        : activeKeys.filter((key) => key !== panelKey);

    const unchanged =
      nextKeys.length === activeKeys.length &&
      nextKeys.every((key, index) => key === activeKeys[index]);
    if (unchanged) return;

    activeKeysRef.current = nextKeys;
    if (!isControlled) {
      setInternalActiveKeys(nextKeys);
    }
    onChange?.(accordion ? nextKeys[0] : nextKeys);
  };

  return (
    <div
      {...asDomProps<HTMLDivElement>(props)}
      className={cn('lazu-collapse', className)}
    >
      {React.Children.map(children, (child, index) =>
        React.isValidElement<ConsolePrimitiveProps>(child)
          ? (() => {
              const panelKey = getCollapsePanelKey(child, index);
              const isOpen = currentActiveKeys.includes(panelKey);
              return React.cloneElement(child, {
                collapseIcon,
                expandIcon,
                isOpen,
                onToggle: (event: React.SyntheticEvent<HTMLDetailsElement>) => {
                  child.props.onToggle?.(event);
                  updateActiveKeys(panelKey, event.currentTarget.open);
                },
              });
            })()
          : child,
      )}
    </div>
  );
}

function CollapsePanel({
  children,
  className,
  collapseIcon,
  defaultOpen,
  expandIcon,
  header,
  isOpen,
  itemKey,
  ...props
}: ConsolePrimitiveProps) {
  const open = isOpen ?? defaultOpen;
  const icon = open ? collapseIcon : expandIcon;
  return (
    <details
      {...(props as React.DetailsHTMLAttributes<HTMLDetailsElement>)}
      className={cn('lazu-collapse-panel', className)}
      data-item-key={itemKey}
      open={open}
    >
      <summary>
        {icon ? (
          <span className='lazu-collapse-panel__icon'>{icon}</span>
        ) : null}
        <span>{header}</span>
      </summary>
      <div className='lazu-collapse-panel__body'>{children}</div>
    </details>
  );
}

const Collapse = CollapseBase as typeof CollapseBase & {
  Panel: ConsoleComponent;
};
Collapse.Panel = CollapsePanel;
