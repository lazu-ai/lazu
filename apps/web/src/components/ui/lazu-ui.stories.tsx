import { Bell, Check, MoreHorizontal, UploadCloud } from 'lucide-react';
import { useState } from 'react';
import { Badge } from './badge';
import { Button } from './button';
import { Checkbox } from './checkbox';
import { DataCard } from './data-card';
import { DataSurface } from './data-surface';
import { DataTableShell } from './data-table-shell';
import { DatePicker, DateRangePicker } from './date-picker';
import { Drawer, DrawerTrigger } from './drawer';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from './dropdown-menu';
import { EmptyState } from './empty-state';
import { ErrorState } from './error-state';
import { Field, FieldMessage, Label } from './field';
import { FilterBar } from './filter-bar';
import { IconButton } from './icon-button';
import { Input } from './input';
import { LoadingState } from './loading-state';
import { Modal, ModalTrigger } from './modal';
import { PageHeader } from './page-header';
import { Pagination } from './pagination';
import { Popover, PopoverContent, PopoverTrigger } from './popover';
import { MultiSelect, Select, SelectItem } from './select';
import { Skeleton } from './skeleton';
import { StatusDot } from './status-dot';
import { Switch } from './switch';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './tabs';
import { Tag } from './tag';
import { Textarea } from './textarea';
import { notify } from './toast';
import { Toolbar, ToolbarGroup } from './toolbar';
import { Tooltip } from './tooltip';
import { Upload } from './upload';
import type { ComponentType, ReactNode } from 'react';

type SampleRow = {
  id: number;
  name: string;
  price: string;
  status: string;
};

const StoryDataTableShell = DataTableShell as unknown as ComponentType<{
  columns: unknown[];
  data: SampleRow[];
  mobileCard?: boolean;
  rowKey?: string;
}>;

export default {
  title: 'Lazu UI/Components',
  parameters: {
    layout: 'padded',
  },
};

const options = [
  { label: 'OpenAI', value: 'openai' },
  { label: 'Anthropic', value: 'anthropic' },
  { label: 'Gemini', value: 'gemini' },
];

const sampleRows: SampleRow[] = [
  { id: 1, name: 'gpt-4.1', status: 'active', price: '$2.00/M' },
  { id: 2, name: 'claude-sonnet', status: 'trial', price: '$3.00/M' },
];

const sampleColumns = [
  { accessorKey: 'name', header: 'Model' },
  { accessorKey: 'status', header: 'Status' },
  { accessorKey: 'price', header: 'Price' },
];

function Surface({ children }: { children: ReactNode }) {
  return <div style={{ maxWidth: 760 }}>{children}</div>;
}

export const Buttons = {
  render: () => (
    <div style={{ display: 'grid', gap: 12 }}>
      <Toolbar>
        <ToolbarGroup>
          <Button variant='primary'>Primary</Button>
          <Button variant='secondary'>Secondary</Button>
          <Button variant='ghost'>Ghost</Button>
          <Button variant='danger'>Danger</Button>
        </ToolbarGroup>
        <IconButton aria-label='Notifications'>
          <Bell size={16} />
        </IconButton>
      </Toolbar>
      <Toolbar>
        <Button loading>Loading</Button>
        <Button disabled>Disabled</Button>
      </Toolbar>
    </div>
  ),
};

export const BadgesAndTags = {
  render: () => (
    <Toolbar>
      <Badge variant='neutral'>Neutral</Badge>
      <Badge variant='success'>Success</Badge>
      <Badge variant='warning'>Warning</Badge>
      <Badge variant='danger'>Danger</Badge>
      <Tag removable onRemove={() => undefined}>
        removable
      </Tag>
    </Toolbar>
  ),
};

export const FormFields = {
  render: () => (
    <Surface>
      <Field>
        <Label htmlFor='model-name'>Model name</Label>
        <Input id='model-name' placeholder='gpt-4.1' />
        <FieldMessage>Shown under an input.</FieldMessage>
      </Field>
      <Field>
        <Label htmlFor='description'>Description</Label>
        <Textarea id='description' placeholder='Optional description' />
        <FieldMessage error>Validation error state.</FieldMessage>
      </Field>
      <Field>
        <Label htmlFor='invalid-model'>Invalid model</Label>
        <Input
          id='invalid-model'
          aria-invalid='true'
          aria-describedby='invalid-model-error'
          defaultValue='unknown/model'
        />
        <FieldMessage id='invalid-model-error' error>
          Choose a model from the Workspace allowlist.
        </FieldMessage>
      </Field>
    </Surface>
  ),
};

export const SelectionControls = {
  render: () => {
    const [enabled, setEnabled] = useState<boolean>(true);

    return (
      <Toolbar>
        <Checkbox
          checked={enabled}
          onCheckedChange={(checked) => setEnabled(checked === true)}
        />
        <Switch checked={enabled} onCheckedChange={setEnabled} />
        <Select defaultValue='openai' aria-label='Provider'>
          {options.map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
        </Select>
      </Toolbar>
    );
  },
};

export const MultiSelectControl = {
  render: () => {
    const [value, setValue] = useState<Array<number | string>>([
      'openai',
      'gemini',
    ]);

    return (
      <MultiSelect
        aria-label='Providers'
        options={options}
        value={value}
        onValueChange={setValue}
        placeholder='Select providers'
      />
    );
  },
};

export const DatePickers = {
  render: () => (
    <Toolbar>
      <DatePicker defaultValue={new Date()} />
      <DateRangePicker
        defaultValue={{
          from: new Date('2026-01-01'),
          to: new Date('2026-01-15'),
        }}
      />
    </Toolbar>
  ),
};

export const Table = {
  render: () => (
    <StoryDataTableShell
      columns={sampleColumns}
      data={sampleRows}
      rowKey='id'
      mobileCard={false}
    />
  ),
};

export const CardsAndSurface = {
  render: () => (
    <Surface>
      <DataSurface>
        <DataCard>
          <PageHeader
            eyebrow='Billing'
            title='Usage overview'
            description='Data surfaces frame dense operational content.'
            actions={<Button size='sm'>Export</Button>}
          />
          <StatusDot tone='success'>Healthy</StatusDot>
        </DataCard>
      </DataSurface>
    </Surface>
  ),
};

export const OperationalStatus = {
  render: () => (
    <Toolbar>
      <StatusDot tone='success'>Healthy</StatusDot>
      <StatusDot tone='warning'>Degraded</StatusDot>
      <StatusDot tone='danger'>Down</StatusDot>
      <StatusDot tone='neutral'>Unknown</StatusDot>
    </Toolbar>
  ),
};

export const FeedbackStates = {
  render: () => (
    <div style={{ display: 'grid', gap: 16, maxWidth: 760 }}>
      <EmptyState
        title='No records yet'
        description='Nothing matches this query.'
        action={<Button variant='primary'>New record</Button>}
      />
      <ErrorState
        title='Request failed'
        description='Retry after checking the upstream channel.'
        action={<Button>Retry</Button>}
        fine='trace: req_9f2ce41b70a3'
      />
      <LoadingState
        title='Loading models'
        description='Fetching latest data.'
      />
      <Skeleton height={48} />
    </div>
  ),
};

export const Overlays = {
  render: () => (
    <Toolbar>
      <Modal
        title='Confirm action'
        description='Modal description.'
        body='Use modals for focused decisions.'
        footer={<Button>Continue</Button>}
      >
        <ModalTrigger>
          <Button>Open modal</Button>
        </ModalTrigger>
      </Modal>
      <Drawer
        title='Channel detail'
        description='Drawer description.'
        body='Use drawers for detail panels.'
        footer={<Button>Save</Button>}
      >
        <DrawerTrigger>
          <Button variant='secondary'>Open drawer</Button>
        </DrawerTrigger>
      </Drawer>
      <Popover>
        <PopoverTrigger>
          <Button variant='ghost'>Open popover</Button>
        </PopoverTrigger>
        <PopoverContent>Popover content</PopoverContent>
      </Popover>
    </Toolbar>
  ),
};

export const MenusAndTooltips = {
  render: () => (
    <Toolbar>
      <DropdownMenu>
        <DropdownMenuTrigger>
          <IconButton aria-label='More actions'>
            <MoreHorizontal size={16} />
          </IconButton>
        </DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem>Duplicate</DropdownMenuItem>
          <DropdownMenuItem>Archive</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <Tooltip content='Tooltip content'>
        <Button variant='secondary'>Hover target</Button>
      </Tooltip>
    </Toolbar>
  ),
};

export const TabsAndPagination = {
  render: () => (
    <div style={{ display: 'grid', gap: 20 }}>
      <Tabs defaultValue='models'>
        <TabsList>
          <TabsTrigger value='models'>Models</TabsTrigger>
          <TabsTrigger value='channels'>Channels</TabsTrigger>
        </TabsList>
        <TabsContent value='models'>Model settings panel</TabsContent>
        <TabsContent value='channels'>Channel settings panel</TabsContent>
      </Tabs>
      <Pagination
        currentPage={2}
        pageSize={10}
        total={84}
        onPageChange={() => {}}
      />
    </div>
  ),
};

export const Filtering = {
  render: () => (
    <FilterBar>
      <Input placeholder='Search models' />
      <Button variant='secondary'>Filter</Button>
    </FilterBar>
  ),
};

export const UploadControl = {
  render: () => (
    <Surface>
      <Upload
        multiple
        defaultValue={[
          new File(['story'], 'pricing.csv', { type: 'text/csv' }),
        ]}
        title='Upload pricing file'
        description='CSV and JSON files are accepted.'
      />
    </Surface>
  ),
};

export const ToastHelpers = {
  render: () => (
    <Button
      onClick={() => notify.success('Saved', { description: 'Toast helper' })}
    >
      <Check size={15} />
      Show toast
    </Button>
  ),
};

export const ToolbarLayout = {
  render: () => (
    <Toolbar>
      <ToolbarGroup>
        <Button variant='primary'>Create</Button>
        <Button variant='secondary'>Import</Button>
      </ToolbarGroup>
      <ToolbarGroup>
        <Tooltip content='Upload'>
          <IconButton aria-label='Upload'>
            <UploadCloud size={16} />
          </IconButton>
        </Tooltip>
      </ToolbarGroup>
    </Toolbar>
  ),
};
