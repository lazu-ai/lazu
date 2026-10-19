import { asMicroUSD } from './money';
import { renderQuota, usdToMicroUSD } from './render';

const dollars = 1;

// @ts-expect-error plain dollar numbers must be converted before rendering.
renderQuota(dollars);

renderQuota(asMicroUSD(1));
renderQuota(usdToMicroUSD(dollars));

export {};
