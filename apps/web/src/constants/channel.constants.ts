export const CHANNEL_OPTIONS = [
  { value: 1, color: 'green', label: 'OpenAI' },
  { value: 8, color: 'pink', labelKey: 'channel.provider.custom' },
  {
    value: 14,
    color: 'indigo',
    label: 'Anthropic Claude',
  },
  {
    value: 24,
    color: 'orange',
    label: 'Google Gemini',
  },
  {
    value: 48,
    color: 'blue',
    label: 'xAI',
  },
];

export type ChannelOption = (typeof CHANNEL_OPTIONS)[number];

export const getChannelOptionLabel = (
  option: ChannelOption,
  t: (key: string) => string,
): string => {
  const labelKey = 'labelKey' in option ? option.labelKey : undefined;
  if (labelKey) {
    return t(labelKey);
  }
  return 'label' in option ? String(option.label ?? '') : '';
};

export const MODEL_TABLE_PAGE_SIZE = 10;
