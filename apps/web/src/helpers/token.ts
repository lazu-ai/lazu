import { API } from './api';
import { unwrap } from '../lib/api-error';

export type TokenOption = {
  id: string;
  maskedKey: string;
  name: string;
};

type RawTokenOption = {
  id?: number | string;
  masked_key?: string;
  name?: string;
};

/**
 * 获取任务创建可用 token（仅返回 id + 掩码）
 * @returns {Promise<Array<{id:string,name:string,maskedKey:string}>>}
 */
export async function fetchTokenOptions(): Promise<TokenOption[]> {
  try {
    const data = unwrap<unknown>(await API.get('/api/token/options?size=200'));
    const options: RawTokenOption[] = Array.isArray(data) ? data : [];
    return options
      .map((item) => ({
        id: String(item.id ?? ''),
        name: item.name || '',
        maskedKey: item.masked_key || '************',
      }))
      .filter((item) => item.id !== '');
  } catch (error) {
    console.error('Error fetching token options:', error);
    return [];
  }
}

/**
 * 兼容旧调用：仅返回 token id 列表（不再暴露 key）
 * @returns {Promise<string[]>}
 */
export async function fetchTokenKeys(): Promise<string[]> {
  const options = await fetchTokenOptions();
  return options.map((item) => item.id);
}

/**
 * 获取服务器地址
 * @returns {string} 服务器地址
 */
export function getServerAddress(): string {
  const status = localStorage.getItem('status');
  let serverAddress = '';

  if (status) {
    try {
      const parsedStatus = JSON.parse(status) as { server_address?: string };
      serverAddress = parsedStatus.server_address || '';
    } catch (error) {
      console.error('Failed to parse status from localStorage:', error);
    }
  }

  if (!serverAddress) {
    serverAddress = window.location.origin;
  }

  return serverAddress;
}
