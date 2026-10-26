type StatusStorageData = Record<string, unknown>;

const setStorageValue = (key: string, value: unknown) => {
  localStorage.setItem(key, String(value));
};

export function setStatusData(data: StatusStorageData) {
  localStorage.setItem('status', JSON.stringify(data));
  if (data.system_name) {
    localStorage.setItem('system_name', String(data.system_name));
  }
  if (data.logo) {
    localStorage.setItem('logo', String(data.logo));
  }
  setStorageValue('micro_usd_per_usd', data.micro_usd_per_usd || 1_000_000);
  // quota_display_type is no longer mirrored into storage: amounts are USD
  // everywhere, so there is nothing for a reader to switch. Clear any value a
  // previous build left behind rather than leaving a stale key around.
  localStorage.removeItem('quota_display_type');
  localStorage.setItem('chats', JSON.stringify(data.chats));
}

export function setUserData(data: unknown) {
  localStorage.setItem('user', JSON.stringify(data));
}
