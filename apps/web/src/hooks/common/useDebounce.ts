import { useEffect, useState } from 'react';

/**
 * 自定义 Hook：防抖处理
 * @param {unknown} value - 需要防抖的值
 * @param {number} delay - 延迟时间（毫秒），默认 300ms
 * @returns {unknown} 防抖后的值
 */
export const useDebounce = <T>(value: T, delay = 300): T => {
  const [debouncedValue, setDebouncedValue] = useState(value);

  useEffect(() => {
    const timer = setTimeout(() => {
      setDebouncedValue(value);
    }, delay);

    return () => {
      clearTimeout(timer);
    };
  }, [value, delay]);

  return debouncedValue;
};

export default useDebounce;
