const colors = [
  'amber',
  'blue',
  'cyan',
  'green',
  'grey',
  'indigo',
  'light-blue',
  'lime',
  'orange',
  'pink',
  'purple',
  'red',
  'teal',
  'violet',
  'yellow',
] as const;

export type NamedColor = (typeof colors)[number];

export function stringToColor(str?: string | null): NamedColor {
  if (!str) {
    return colors[0];
  }

  let sum = 0;
  for (let i = 0; i < str.length; i++) {
    sum += str.charCodeAt(i);
  }
  const index = sum % colors.length;
  return colors[index];
}
