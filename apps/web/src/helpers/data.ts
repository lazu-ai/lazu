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
  if (data.footer_html) {
    localStorage.setItem('footer_html', String(data.footer_html));
  }
  setStorageValue('micro_usd_per_usd', data.micro_usd_per_usd || 1_000_000);
  // quota_display_type is no longer mirrored into storage: amounts are USD
  // everywhere, so there is nothing for a reader to switch. Clear any value a
  // previous build left behind rather than leaving a stale key around.
  localStorage.removeItem('quota_display_type');
  setStorageValue('enable_drawing', data.enable_drawing);
  setStorageValue('enable_data_export', data.enable_data_export);
  localStorage.setItem('chats', JSON.stringify(data.chats));
  setStorageValue('data_export_default_time', data.data_export_default_time);
  if (data.docs_link) {
    localStorage.setItem('docs_link', String(data.docs_link));
  } else {
    localStorage.removeItem('docs_link');
  }
}

export function setUserData(data: unknown) {
  localStorage.setItem('user', JSON.stringify(data));
}
