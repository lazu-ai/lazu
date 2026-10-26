import { asMicroUSD, formatMoney } from './money';
import { usdToMicroUSD } from './render';

const dollars = 1;

// @ts-expect-error plain dollar numbers must be converted before rendering.
formatMoney(dollars);

formatMoney(asMicroUSD(1));
formatMoney(usdToMicroUSD(dollars));

export {};
