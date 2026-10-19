export type UsageLogUrlRange = {
  exact: true;
  from: Date;
  to: Date;
};

export const getUsageLogUrlRange = (
  search: string,
): UsageLogUrlRange | null => {
  const params = new URLSearchParams(search);
  const start = Number(params.get('start_timestamp'));
  const end = Number(params.get('end_timestamp'));
  if (
    !Number.isFinite(start) ||
    !Number.isFinite(end) ||
    start <= 0 ||
    end <= start
  ) {
    return null;
  }

  return {
    exact: true,
    from: new Date(start * 1000),
    to: new Date(end * 1000),
  };
};
