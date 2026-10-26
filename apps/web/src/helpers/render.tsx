export { getMicroUSDPerUSD, microUSDToUSD, usdToMicroUSD } from './money';

/**
 * rehype 插件：将段落等文本节点拆分为逐词 <span>，并添加淡入动画 class。
 * 仅在流式渲染阶段使用，避免已渲染文字重复动画。
 */
export { rehypeSplitWordsIntoSpans } from './rehype-streaming';
