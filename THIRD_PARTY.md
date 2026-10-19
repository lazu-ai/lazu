# Third-party licenses

Lazu is distributed under AGPL-3.0-or-later (see [LICENSE](./LICENSE)). It is
based on [new-api](https://github.com/QuantumNous/new-api) (AGPL-3.0), which is
based on [one-api](https://github.com/songquanpeng/one-api) (MIT); attribution
for both is in [NOTICE](./NOTICE).

This file lists what the build pulls in or the tree carries from others. Every
license below is compatible with AGPL-3.0. Where a package offers a choice, the
compatible option is the one relied on: `dompurify` is used under Apache-2.0.

## Go modules

Regenerate with `go-licenses report ./apps/... ./internal/... ./pkg/...`
(`go install github.com/google/go-licenses/v2@latest`).

| Module                                                                                                                                                                                           | License      |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------ |
| [github.com/andybalholm/brotli](https://github.com/andybalholm/brotli/blob/v1.1.1/LICENSE)                                                                                                       | MIT          |
| [github.com/anknown/ahocorasick](https://github.com/anknown/ahocorasick/blob/d75dbd5169c0/LICENSE)                                                                                               | MIT          |
| [github.com/anknown/darts](https://github.com/anknown/darts/blob/83ff685239e6/LICENSE)                                                                                                           | MIT          |
| [github.com/aws/aws-sdk-go-v2](https://github.com/aws/aws-sdk-go-v2/blob/v1.41.5/LICENSE.txt)                                                                                                    | Apache-2.0   |
| [github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream](https://github.com/aws/aws-sdk-go-v2/blob/aws/protocol/eventstream/v1.7.8/aws/protocol/eventstream/LICENSE.txt)                          | Apache-2.0   |
| [github.com/aws/aws-sdk-go-v2/credentials](https://github.com/aws/aws-sdk-go-v2/blob/credentials/v1.17.11/credentials/LICENSE.txt)                                                               | Apache-2.0   |
| [github.com/aws/aws-sdk-go-v2/internal/configsources](https://github.com/aws/aws-sdk-go-v2/blob/internal/configsources/v1.4.21/internal/configsources/LICENSE.txt)                               | Apache-2.0   |
| [github.com/aws/aws-sdk-go-v2/internal/endpoints/v2](https://github.com/aws/aws-sdk-go-v2/blob/internal/endpoints/v2.7.21/internal/endpoints/v2/LICENSE.txt)                                     | Apache-2.0   |
| [github.com/aws/aws-sdk-go-v2/internal/sync/singleflight](https://github.com/aws/aws-sdk-go-v2/blob/v1.41.5/internal/sync/singleflight/LICENSE)                                                  | BSD-3-Clause |
| [github.com/aws/aws-sdk-go-v2/internal/v4a](https://github.com/aws/aws-sdk-go-v2/blob/internal/v4a/v1.4.22/internal/v4a/LICENSE.txt)                                                             | Apache-2.0   |
| [github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding](https://github.com/aws/aws-sdk-go-v2/blob/service/internal/accept-encoding/v1.13.7/service/internal/accept-encoding/LICENSE.txt) | Apache-2.0   |
| [github.com/aws/aws-sdk-go-v2/service/internal/checksum](https://github.com/aws/aws-sdk-go-v2/blob/service/internal/checksum/v1.9.13/service/internal/checksum/LICENSE.txt)                      | Apache-2.0   |
| [github.com/aws/aws-sdk-go-v2/service/internal/presigned-url](https://github.com/aws/aws-sdk-go-v2/blob/service/internal/presigned-url/v1.13.21/service/internal/presigned-url/LICENSE.txt)      | Apache-2.0   |
| [github.com/aws/aws-sdk-go-v2/service/internal/s3shared](https://github.com/aws/aws-sdk-go-v2/blob/service/internal/s3shared/v1.19.21/service/internal/s3shared/LICENSE.txt)                     | Apache-2.0   |
| [github.com/aws/aws-sdk-go-v2/service/s3](https://github.com/aws/aws-sdk-go-v2/blob/service/s3/v1.97.3/service/s3/LICENSE.txt)                                                                   | Apache-2.0   |
| [github.com/aws/smithy-go](https://github.com/aws/smithy-go/blob/v1.24.2/LICENSE)                                                                                                                | Apache-2.0   |
| [github.com/aws/smithy-go/internal/sync/singleflight](https://github.com/aws/smithy-go/blob/v1.24.2/internal/sync/singleflight/LICENSE)                                                          | BSD-3-Clause |
| [github.com/bytedance/gopkg/util](https://github.com/bytedance/gopkg/blob/v0.1.3/LICENSE)                                                                                                        | Apache-2.0   |
| [github.com/cespare/xxhash/v2](https://github.com/cespare/xxhash/blob/v2.3.0/LICENSE.txt)                                                                                                        | MIT          |
| [github.com/coreos/go-oidc/v3/oidc](https://github.com/coreos/go-oidc/blob/v3.19.0/LICENSE)                                                                                                      | Apache-2.0   |
| [github.com/dgryski/go-rendezvous](https://github.com/dgryski/go-rendezvous/blob/9f7001d12a5f/LICENSE)                                                                                           | MIT          |
| [github.com/dlclark/regexp2](https://github.com/dlclark/regexp2/blob/v1.11.5/LICENSE)                                                                                                            | MIT          |
| [github.com/dustin/go-humanize](https://github.com/dustin/go-humanize/blob/v1.0.1/LICENSE)                                                                                                       | MIT          |
| [github.com/gabriel-vasile/mimetype](https://github.com/gabriel-vasile/mimetype/blob/v1.4.3/LICENSE)                                                                                             | MIT          |
| [github.com/gin-contrib/cors](https://github.com/gin-contrib/cors/blob/v1.7.2/LICENSE)                                                                                                           | MIT          |
| [github.com/gin-contrib/gzip](https://github.com/gin-contrib/gzip/blob/v0.0.6/LICENSE)                                                                                                           | MIT          |
| [github.com/gin-contrib/sessions](https://github.com/gin-contrib/sessions/blob/v0.0.5/LICENSE)                                                                                                   | MIT          |
| [github.com/gin-contrib/sse](https://github.com/gin-contrib/sse/blob/v0.1.0/LICENSE)                                                                                                             | MIT          |
| [github.com/gin-gonic/gin](https://github.com/gin-gonic/gin/blob/v1.9.1/LICENSE)                                                                                                                 | MIT          |
| [github.com/glebarez/go-sqlite](https://github.com/glebarez/go-sqlite/blob/v1.21.2/LICENSE)                                                                                                      | BSD-3-Clause |
| [github.com/glebarez/sqlite](https://github.com/glebarez/sqlite/blob/v1.11.0/License)                                                                                                            | MIT          |
| [github.com/go-jose/go-jose/v4](https://github.com/go-jose/go-jose/blob/v4.1.4/LICENSE)                                                                                                          | Apache-2.0   |
| [github.com/go-jose/go-jose/v4/json](https://github.com/go-jose/go-jose/blob/v4.1.4/json/LICENSE)                                                                                                | BSD-3-Clause |
| [github.com/go-playground/locales](https://github.com/go-playground/locales/blob/v0.14.1/LICENSE)                                                                                                | MIT          |
| [github.com/go-playground/universal-translator](https://github.com/go-playground/universal-translator/blob/v0.18.1/LICENSE)                                                                      | MIT          |
| [github.com/go-playground/validator/v10](https://github.com/go-playground/validator/blob/v10.20.0/LICENSE)                                                                                       | MIT          |
| [github.com/go-redis/redis/v8](https://github.com/go-redis/redis/blob/v8.11.5/LICENSE)                                                                                                           | BSD-2-Clause |
| [github.com/google/uuid](https://github.com/google/uuid/blob/v1.6.0/LICENSE)                                                                                                                     | BSD-3-Clause |
| [github.com/gorilla/context](https://github.com/gorilla/context/blob/v1.1.1/LICENSE)                                                                                                             | BSD-3-Clause |
| [github.com/gorilla/securecookie](https://github.com/gorilla/securecookie/blob/v1.1.1/LICENSE)                                                                                                   | BSD-3-Clause |
| [github.com/gorilla/sessions](https://github.com/gorilla/sessions/blob/v1.2.1/LICENSE)                                                                                                           | BSD-3-Clause |
| [github.com/gorilla/websocket](https://github.com/gorilla/websocket/blob/v1.5.3/LICENSE)                                                                                                         | BSD-2-Clause |
| [github.com/jackc/pgpassfile](https://github.com/jackc/pgpassfile/blob/v1.0.0/LICENSE)                                                                                                           | MIT          |
| [github.com/jackc/pgservicefile](https://github.com/jackc/pgservicefile/blob/5a60cdf6a761/LICENSE)                                                                                               | MIT          |
| [github.com/jackc/pgx/v5](https://github.com/jackc/pgx/blob/v5.9.2/LICENSE)                                                                                                                      | MIT          |
| [github.com/jackc/puddle/v2](https://github.com/jackc/puddle/blob/v2.2.2/LICENSE)                                                                                                                | MIT          |
| [github.com/jinzhu/copier](https://github.com/jinzhu/copier/blob/v0.4.0/License)                                                                                                                 | MIT          |
| [github.com/jinzhu/inflection](https://github.com/jinzhu/inflection/blob/v1.0.0/LICENSE)                                                                                                         | MIT          |
| [github.com/jinzhu/now](https://github.com/jinzhu/now/blob/v1.1.5/License)                                                                                                                       | MIT          |
| [github.com/joho/godotenv](https://github.com/joho/godotenv/blob/v1.5.1/LICENCE)                                                                                                                 | MIT          |
| [github.com/leodido/go-urn](https://github.com/leodido/go-urn/blob/v1.4.0/LICENSE)                                                                                                               | MIT          |
| [github.com/mattn/go-isatty](https://github.com/mattn/go-isatty/blob/v0.0.20/LICENSE)                                                                                                            | MIT          |
| [github.com/oklog/ulid/v2](https://github.com/oklog/ulid/blob/v2.1.1/LICENSE)                                                                                                                    | Apache-2.0   |
| [github.com/pelletier/go-toml/v2](https://github.com/pelletier/go-toml/blob/v2.2.1/LICENSE)                                                                                                      | MIT          |
| [github.com/pkg/errors](https://github.com/pkg/errors/blob/v0.9.1/LICENSE)                                                                                                                       | BSD-2-Clause |
| [github.com/redis/go-redis/v9](https://github.com/redis/go-redis/blob/v9.16.0/LICENSE)                                                                                                           | BSD-2-Clause |
| [github.com/remyoudompheng/bigfft](https://github.com/remyoudompheng/bigfft/blob/24d4a6f8daec/LICENSE)                                                                                           | BSD-3-Clause |
| [github.com/resend/resend-go/v3](https://github.com/resend/resend-go/blob/v3.0.0/LICENSE.md)                                                                                                     | MIT          |
| [github.com/richardlehane/mscfb](https://github.com/richardlehane/mscfb/blob/v1.0.8/LICENSE.txt)                                                                                                 | Apache-2.0   |
| [github.com/richardlehane/msoleps/types](https://github.com/richardlehane/msoleps/blob/v1.0.6/LICENSE.txt)                                                                                       | Apache-2.0   |
| [github.com/robfig/cron/v3](https://github.com/robfig/cron/blob/v3.0.1/LICENSE)                                                                                                                  | MIT          |
| [github.com/samber/lo](https://github.com/samber/lo/blob/v1.39.0/LICENSE)                                                                                                                        | MIT          |
| [github.com/shirou/gopsutil](https://github.com/shirou/gopsutil/blob/v3.21.11/LICENSE)                                                                                                           | BSD-3-Clause |
| [github.com/shopspring/decimal](https://github.com/shopspring/decimal/blob/v1.4.0/LICENSE)                                                                                                       | MIT          |
| [github.com/stripe/stripe-go/v81](https://github.com/stripe/stripe-go/blob/v81.4.0/LICENSE)                                                                                                      | MIT          |
| [github.com/tidwall/gjson](https://github.com/tidwall/gjson/blob/v1.18.0/LICENSE)                                                                                                                | MIT          |
| [github.com/tidwall/match](https://github.com/tidwall/match/blob/v1.1.1/LICENSE)                                                                                                                 | MIT          |
| [github.com/tidwall/pretty](https://github.com/tidwall/pretty/blob/v1.2.0/LICENSE)                                                                                                               | MIT          |
| [github.com/tidwall/sjson](https://github.com/tidwall/sjson/blob/v1.2.5/LICENSE)                                                                                                                 | MIT          |
| [github.com/tiendc/go-deepcopy](https://github.com/tiendc/go-deepcopy/blob/v1.7.2/LICENSE)                                                                                                       | MIT          |
| [github.com/tiktoken-go/tokenizer](https://github.com/tiktoken-go/tokenizer/blob/v0.6.2/LICENSE)                                                                                                 | MIT          |
| [github.com/tklauser/go-sysconf](https://github.com/tklauser/go-sysconf/blob/v0.3.12/LICENSE)                                                                                                    | BSD-3-Clause |
| [github.com/ugorji/go/codec](https://github.com/ugorji/go/blob/codec/v1.2.12/codec/LICENSE)                                                                                                      | MIT          |
| [github.com/xuri/efp](https://github.com/xuri/efp/blob/v0.0.2/LICENSE)                                                                                                                           | BSD-3-Clause |
| [github.com/xuri/excelize/v2](https://github.com/xuri/excelize/blob/6258dcebc4e2/LICENSE)                                                                                                        | BSD-3-Clause |
| [github.com/xuri/nfp](https://github.com/xuri/nfp/blob/2ddeb826f9a9/LICENSE)                                                                                                                     | BSD-3-Clause |
| [github.com/yuin/goldmark](https://github.com/yuin/goldmark/blob/v1.7.17/LICENSE)                                                                                                                | MIT          |
| [golang.org/x/crypto](https://cs.opensource.google/go/x/crypto/+/v0.57.0:LICENSE)                                                                                                                | BSD-3-Clause |
| [golang.org/x/exp/constraints](https://cs.opensource.google/go/x/exp/+/c0f41cb1:LICENSE)                                                                                                         | BSD-3-Clause |
| [golang.org/x/image](https://cs.opensource.google/go/x/image/+/v0.45.0:LICENSE)                                                                                                                  | BSD-3-Clause |
| [golang.org/x/net](https://cs.opensource.google/go/x/net/+/v0.60.0:LICENSE)                                                                                                                      | BSD-3-Clause |
| [golang.org/x/oauth2](https://cs.opensource.google/go/x/oauth2/+/v0.36.0:LICENSE)                                                                                                                | BSD-3-Clause |
| [golang.org/x/sync](https://cs.opensource.google/go/x/sync/+/v0.23.0:LICENSE)                                                                                                                    | BSD-3-Clause |
| [golang.org/x/sys](https://cs.opensource.google/go/x/sys/+/v0.48.0:LICENSE)                                                                                                                      | BSD-3-Clause |
| [golang.org/x/text](https://cs.opensource.google/go/x/text/+/v0.42.0:LICENSE)                                                                                                                    | BSD-3-Clause |
| [google.golang.org/protobuf](https://github.com/protocolbuffers/protobuf-go/blob/v1.35.2/LICENSE)                                                                                                | BSD-3-Clause |
| [gopkg.in/yaml.v3](https://github.com/go-yaml/yaml/blob/v3.0.1/LICENSE)                                                                                                                          | MIT          |
| [gorm.io/driver/postgres](https://github.com/go-gorm/postgres/blob/v1.5.2/License)                                                                                                               | MIT          |
| [gorm.io/gorm](https://github.com/go-gorm/gorm/blob/v1.25.7/LICENSE)                                                                                                                             | MIT          |
| [modernc.org/libc](https://gitlab.com/cznic/libc/-/blob/v1.22.5/LICENSE)                                                                                                                         | BSD-3-Clause |
| [modernc.org/libc/honnef.co/go/netdb](https://gitlab.com/cznic/libc/-/blob/v1.22.5/honnef.co/go/netdb/LICENSE)                                                                                   | MIT          |
| [modernc.org/mathutil](https://gitlab.com/cznic/mathutil/-/blob/v1.5.0/LICENSE)                                                                                                                  | BSD-3-Clause |
| [modernc.org/memory](https://gitlab.com/cznic/memory/-/blob/v1.5.0/LICENSE)                                                                                                                      | BSD-3-Clause |
| [modernc.org/sqlite/lib](https://gitlab.com/cznic/sqlite/-/blob/v1.23.1/LICENSE)                                                                                                                 | BSD-3-Clause |
| [rsc.io/qr](https://github.com/rsc/qr/blob/v0.2.0/LICENSE)                                                                                                                                       | BSD-3-Clause |

## JavaScript packages

Direct dependencies of the web console (`apps/web/package.json`) and the docs
site (`apps/docs/package.json`). Their own dependencies are pinned in
`apps/web/bun.lock` and `apps/docs/package-lock.json`.

| App  | Package                            | Version  | License                 |
| ---- | ---------------------------------- | -------- | ----------------------- |
| web  | `@base-ui/react`                   | 1.6.0    | MIT                     |
| web  | `@fontsource-variable/geist`       | 5.2.9    | OFL-1.1                 |
| web  | `@fontsource/ibm-plex-mono`        | 5.2.7    | OFL-1.1                 |
| web  | `@hookform/resolvers`              | 5.2.2    | MIT                     |
| web  | `@tanstack/react-query`            | 5.100.11 | MIT                     |
| web  | `@tanstack/react-query-devtools`   | 5.100.11 | MIT                     |
| web  | `@tanstack/react-table`            | 8.21.3   | MIT                     |
| web  | `@visactor/react-vchart`           | 1.8.11   | MIT                     |
| web  | `@visactor/vchart`                 | 1.8.11   | MIT                     |
| web  | `axios`                            | 1.20.0   | MIT                     |
| web  | `class-variance-authority`         | 0.7.1    | Apache-2.0              |
| web  | `clsx`                             | 2.1.1    | MIT                     |
| web  | `dayjs`                            | 1.11.20  | MIT                     |
| web  | `dompurify`                        | 3.4.5    | (MPL-2.0 OR Apache-2.0) |
| web  | `html-to-image`                    | 1.11.13  | MIT                     |
| web  | `i18next`                          | 26.2.0   | MIT                     |
| web  | `i18next-browser-languagedetector` | 7.2.2    | MIT                     |
| web  | `katex`                            | 0.16.47  | MIT                     |
| web  | `lucide-react`                     | 0.511.0  | ISC                     |
| web  | `marked`                           | 4.3.0    | MIT                     |
| web  | `mdast-util-from-markdown`         | 2.0.3    | MIT                     |
| web  | `mermaid`                          | 11.15.0  | MIT                     |
| web  | `react`                            | 18.3.1   | MIT                     |
| web  | `react-day-picker`                 | 9.14.0   | MIT                     |
| web  | `react-dom`                        | 18.3.1   | MIT                     |
| web  | `react-dropzone`                   | 14.4.1   | MIT                     |
| web  | `react-hook-form`                  | 7.76.0   | MIT                     |
| web  | `react-i18next`                    | 13.5.0   | MIT                     |
| web  | `react-markdown`                   | 10.1.0   | MIT                     |
| web  | `react-router-dom`                 | 6.30.3   | MIT                     |
| web  | `rehype-highlight`                 | 7.0.2    | MIT                     |
| web  | `rehype-katex`                     | 7.0.1    | MIT                     |
| web  | `remark-breaks`                    | 4.0.0    | MIT                     |
| web  | `remark-gfm`                       | 4.0.1    | MIT                     |
| web  | `remark-math`                      | 6.0.0    | MIT                     |
| web  | `sonner`                           | 2.0.7    | MIT                     |
| web  | `tailwind-merge`                   | 3.6.0    | MIT                     |
| web  | `unist-util-visit`                 | 5.1.0    | MIT                     |
| web  | `use-debounce`                     | 10.1.1   | MIT                     |
| web  | `zod`                              | 4.4.3    | MIT                     |
| docs | `@fontsource-variable/geist`       | 5.2.9    | OFL-1.1                 |
| docs | `@fontsource/ibm-plex-mono`        | 5.2.7    | OFL-1.1                 |
| docs | `@scalar/api-reference-react`      | 0.8.70   | MIT                     |
| docs | `lucide-react`                     | 0.511.0  | ISC                     |
| docs | `react`                            | 19.2.6   | MIT                     |
| docs | `react-dom`                        | 19.2.6   | MIT                     |
| docs | `react-router`                     | 7.15.1   | MIT                     |
| docs | `vocs`                             | 1.4.1    | MIT                     |
| docs | `yaml`                             | 2.9.0    | ISC                     |

## Carried in this repository

| Path                                             | What                                                                                                           | License                                           |
| ------------------------------------------------ | -------------------------------------------------------------------------------------------------------------- | ------------------------------------------------- |
| `brand/assets/fonts/`, `apps/docs/public/fonts/` | Instrument Serif, Geist, IBM Plex Mono                                                                         | SIL OFL 1.1 (`brand/assets/fonts/OFL.txt`)        |
| `internal/service/sharefonts/`                   | Fonts drawn into share images                                                                                  | SIL OFL 1.1 (license files alongside)             |
| `apps/web/vendor/braces/`                        | Patched copy of `braces`                                                                                       | MIT (`LICENSE` alongside)                         |
| `apps/web/src/brand/assets/providers/`           | Provider icons from lobe-icons                                                                                 | MIT (`LICENSE.lobe-icons.txt` alongside)          |
| `.claude/skills/`, `.agents/skills/`             | Design skills from [emilkowalski/skills](https://github.com/emilkowalski/skills), pinned in `skills-lock.json` | MIT (`LICENSE.emilkowalski-skills.txt` alongside) |
