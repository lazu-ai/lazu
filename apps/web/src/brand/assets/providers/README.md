# Model provider logos

These SVGs are vendored for the user-facing model catalog. Keeping them local
avoids runtime CDN dependencies and makes provider identity reviewable with the
rest of the UI.

- Source package: `@lobehub/icons-static-svg@1.94.0`
- Upstream: <https://github.com/lobehub/lobe-icons>
- Package license: MIT
- Vendored license: [`LICENSE.lobe-icons.txt`](./LICENSE.lobe-icons.txt)
- Retrieved: 2026-08-24

The filenames use company names rather than model-family names where possible.
For example, Claude resolves to `anthropic.svg`, Gemini to `google.svg`, GLM to
`zhipu-ai.svg`, and Qwen to `alibaba-cloud.svg`.

Do not add letter-avatar or handcrafted-logo fallbacks here. Unknown/custom
providers should use a neutral UI icon until a verified company asset is added.

Seed uses the existing ByteDance mark, matching the identity on the
[official Seed site](https://seed.bytedance.com/en/). Its family is labelled
Seed while its maker remains ByteDance.
