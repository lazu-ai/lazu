import { asMicroUSD, type MicroUSD } from './money';
import { formatDecimalValue } from './utils';

export function getMicroUSDPerUSD() {
  const microUSDPerUSD = parseFloat(
    localStorage.getItem('micro_usd_per_usd') || '1000000',
  );
  return Number.isFinite(microUSDPerUSD) && microUSDPerUSD > 0
    ? microUSDPerUSD
    : 1000000;
}

export function usdToMicroUSD(amount: number | string): MicroUSD {
  const dollars = Number(amount);
  return asMicroUSD(
    Math.round((Number.isFinite(dollars) ? dollars : 0) * getMicroUSDPerUSD()),
  );
}

export function microUSDToUSD(amountMicrousd: MicroUSD): number {
  return amountMicrousd / getMicroUSDPerUSD();
}

export function renderQuota(quota: MicroUSD | null | undefined, digits = 2) {
  const quotaMicrousd = asMicroUSD(quota);
  const resultUSD = microUSDToUSD(quotaMicrousd);
  const symbol = '$';
  const value = resultUSD;
  const fixedResult = formatDecimalValue(value, digits);
  if (parseFloat(fixedResult) === 0 && quotaMicrousd > 0 && value > 0) {
    const minValue = Math.pow(10, -digits);
    return symbol + formatDecimalValue(minValue, digits);
  }
  return symbol + fixedResult;
}

/**
 * rehype 插件：将段落等文本节点拆分为逐词 <span>，并添加淡入动画 class。
 * 仅在流式渲染阶段使用，避免已渲染文字重复动画。
 */
export { rehypeSplitWordsIntoSpans } from './rehype-streaming';
