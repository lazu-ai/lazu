# Payment e-mails — 付款邮件（2026-09-23）

> **Locked:** 2026-09-23 by owner approval；**2026-09-27 修订为一封邮件**（owner 选定方案 A）
> **Selected direction:** 方案二 · 墨头 (ink masthead)
> **Visual reference:** `lazu-ops:brand/explorations/payment-emails-2026-09/index.html`（`python3 build.py` 重建）；
> 设计画布 `lazu-ops:brand/canvases.md`（两封版，历史）；
> 一封版画布 `lazu-ops:brand/canvases.md`（A · 一个按钮 + 一个链接）
> **Inherits:** `yunmu-console.md`（墨/纸反转、纸面）、`logo-system.md`（Seal 与字标）、
> `team-projects-2026-09.md` § Invoices（Invoice 模式、DUBRIO LLC、账单状态互相独立）
> **Part of:** `emails.md`（2026-09-30 邮件体系，墨头层）
> **Supersedes:** `team-projects-2026-09.md` 中「Our e-mail becomes a balance notification without a PDF」一句

每笔付款成功的充值，收件人只收到**一封 Lazu 邮件**，由 `noreply@lazu.ai` 发出。Invoice 和 Receipt
两份单据由 Stripe 生成；Lazu 负责把它们可靠地交到用户手上，不自建单据版式。

---

## 1. 一封邮件

对照海外同类产品（OpenAI、Cursor 不发；Anthropic、OpenRouter、Replicate、Vercel 只发一封 Stripe
收据），没有一家在付款后发两封。Lazu 自己发这一封，因为预付额度的读者要知道「到账了、余额多少」，
而 Stripe 收据不说这件事。

| | 付款成功 Payment received |
| --- | --- |
| 眉行 | `PAYMENT RECEIVED` |
| 主视觉 | 到账额度 `$5.00`（≥ $1,000 用紧凑字号） |
| 副文 | `API credit added to your Lazu account.` |
| 事实（四格） | 付款时间 · 支付方式 · 钱包余额 · Invoice 编号 |
| 动作 | 主按钮 **Open wallet** + 一个链接 **View invoice →** |
| 标题 | `$5.00 added to your Lazu account` |

- **Open wallet** 落到 `/console/wallet?top_up_id=…`，钱包顶部钉住这一笔的付款记录卡。
- **View invoice** 落到 Stripe hosted Invoice 页：写明「账单已付」，同时提供 Invoice 与 Receipt
  下载，所以邮件里不再单独放 View receipt（见 § 6）。
- **发送时机**：额度入账时，若 Invoice 与 Receipt 已齐，立刻发完整版；否则排一封 60 秒后才发的
  降级版，并按 outbox 的 `available_at` 安排唤醒，不再等下一轮分钟扫描；进程重启或发送失败仍由
  持久 outbox 与 worker 补偿。60 秒内单据齐了，就用完整版替换那封尚未发出的邮件并立刻发。降级版没有 View invoice，
  第四格写充值编号。**一笔充值每位收件人只发一次**，降级版发出后单据再到也不补发，单据留在钱包
  记录卡与 Stripe 页面里。
- 状态互相独立、幂等：webhook 可乱序、可重复；Invoice 迟到或失败不得延误到账通知。
- 补偿：所有事件都没排上邮件时，对账任务在单据齐后补发一次；2026-09-27 之前已发过旧「单据」
  邮件的收件人视为已发。
- 实付为 $0（优惠码全额折扣）时 Stripe 不生成 Receipt，单据齐的判断不等待它。

### 收件人与措辞

- **不区分个人与团队**：钱只进一个钱包（项目所有者的钱包），所以永远写「your Lazu account」，
  余额永远叫 Wallet balance，按钮永远是 Open wallet；邮件里不出现项目名，默认项目名（如
  `panda-1`）更不能出现。
- **别人替所有者付款时**：所有者与实际付款人都收到同一封，第一格事实由付款时间换成 **Paid by**。
- 支付方式写实际方式：`Visa ···· 4242`、`WeChat Pay`、`Alipay`。

### 刻意不写的内容

以下句子已评审删除，不得加回邮件：

- 「Invoice 和收据会另发一封邮件」——不会另发，也不需要预告。
- 「这不是中国增值税发票」——属于钱包账单区与 Docs 账单 FAQ 的固定说明（`team-projects-2026-09.md` § Invoices），不属于每封邮件。
- 「系统自动发送，请勿回复」——发件人已是 noreply，页脚的 Contact support 承担此职能。

## 2. 版式

| 度量 | 桌面 | 手机 |
| --- | --- | --- |
| 邮件宽 | 640px | 视口宽，16px 外边距 |
| 墨头内边距 | 56 / 64 / 72px（上 / 左右 / 下） | 36 / 28 / 44px |
| Logo | Primary Paper，高 30px | 高 26px |
| Logo → 主视觉 | 112px | 72px |
| 主视觉字号 | 金额 120px（≥ $1,000 为 88px） | 88px |
| 正文内边距 | 56 / 64 / 64px，区块间距 48px | 36 / 28 / 40px，间距 32px |
| 事实 | 3–4 栏网格，标签 12px，值 15px | 逐行，发丝线分隔，行高 14px 上下内边距 |
| 主按钮 | 墨底纸字，高 48px，圆角 10px | 通栏 |

- 墨头 = `--lazu-action` `#202329`；主视觉纸色 `#fbfaf7`，副文 72% 纸色，眉行 62% 纸色。
- 正文 = `--lazu-surface-solid` `#fdfdfc`，发丝线 `--lazu-border`，次要链接 `--lazu-evidence-strong`。
- 字体：金额用 `--lazu-font-display`（Instrument Serif，均 ≥ 20px）；正文、标签、按钮用
  Geist / 系统 CJK；编号、充值 ID 用 IBM Plex Mono。
- 眉行是机器语域：mono 11px（手机 10px）、`.16em` 大写，**不翻译**（同 `paper-states.md`）。
- 页脚在卡片**外**：左 `Lazu.ai`，右 Contact support / 联系支持，12px。
- 不加付款印章、对勾徽章或其他装饰；Logo 自带的 Seal 是唯一的圆形。

### 深色模式

模板声明 light only，但 Gmail 等客户端可能强制深色。强制深色时应读作：墨头 `#2c2c2f`、正文
`#232325`、文字 `#f1f1f2` / `#a4a4a7`、主按钮纸底墨字（`#f1f1f2` / `#1a1a1b`）、链接 `#9fb6f7`。

## 3. 邮件实现约束

- 表格布局 + 行内样式；墨头同时写 `bgcolor` 属性，保证 Outlook 等客户端的底色。
- 网页字体多数客户端不加载：衬线回退 `Georgia, 'Times New Roman', serif`，正文回退
  `-apple-system, 'Segoe UI', 'PingFang SC', 'Microsoft YaHei', Arial, sans-serif`。
- Logo 用托管的 **PNG**（Gmail / Outlook 不渲染 SVG），由 `assets/logo/generate-assets.mjs` 从正式母版
  生成 Primary Paper 的 @2x 位图；不得截图或手绘。仓库目前只有 Graphite 版 Primary 位图。
- 按钮链接指向 Lazu 自己的登录后单据跳转地址。后端在每次打开时从 Stripe 读取当前的 hosted
  Invoice 页或 Receipt URL，再跳转；钱包的单据入口走同一地址。
- 模板共用一个付款邮件外壳，不再借用 `project_membership.html`；英文邮件不得带中文页脚。

## 4. Stripe 配置（后台，一次性）

- **Branding**：品牌色 `#F2F2EF`，强调色 `#202329`；图标
  `assets/logo/raster/graphite/lazu-graphite-512.png`，Logo
  `assets/logo/raster/lazu-primary-graphite-1280w.png`。选浅色品牌色是因为同一枚深色 Logo 也要印在白底
  Invoice PDF 上。
- **Business details**：公开名称 Lazu、网站、客服邮箱；卡账单描述 `LAZU`。
- **Customer emails**：关闭「成功的付款」（Stripe 收据邮件）；保留「退款」；扣款授权类邮件保持现状；
  自动发送已确定 Invoice 的开关保持关闭。否则一次付款会收到两到三封。
- **Invoice 默认支付方式**：关闭 Bank transfer。Checkout 生成的 Invoice 是 `send_invoice` 类型，开着时
  PDF 会印出 Stripe 虚拟收款账号与转账说明，对已付款的读者是误导。将来有企业客户要求对公电汇时，
  在那一张 Invoice 上单独打开。

## 5. 待定

- 邮件语言：统一发送英文。中文版探索稿保留为设计记录，不进入发送流程。
- 钱包余额：在入账事务中读取入账后的所有者钱包余额，存入订单 `balance_after_microusd`，邮件晚于
  入账渲染时也用这个快照。

## 6. 修订 2026-09-27 · Invoice 给页面，不给 PDF

Stripe 的 Invoice PDF 是「开票时的账单」：付款之后再下载，仍然印着 `Amount due` 与 `Pay online`。
2026-09-27 实测：

- 生产环境已付 Invoice `FJECXKT6-0002`（Checkout 生成，`send_invoice`）：PDF 每次请求都是新渲染，
  不是缓存，仍写 Amount due、Pay online，外加银行转账说明。
- 测试环境自建两张 `charge_automatically` Invoice，分别用「线下已付」和 `attach_payment` 挂上真实
  付款，状态均为 `paid`、`amount_remaining = 0`：PDF 依然写 Amount due、Pay online，只是没有了
  银行转账段落。

所以「自建 Invoice」不能解决问题，已放弃；证明已付款的文件是 Receipt。落地规则：

- 产品内不直接分发 `invoice_pdf`。邮件的 **View invoice** 与钱包的「在 Stripe 查看」都落到
  hosted Invoice 页，那一页写明「账单已付」并同时提供 Invoice 与 Receipt 下载。
- Receipt 是付款凭证，在钱包的单据区排在 Invoice 之前，作为主动作。
- 银行转账段落由 § 4 的 Stripe 设置去掉。
- 邮件与回跳都落到钱包里的同一张付款记录卡（`/console/wallet?top_up_id=…`），不再有独立的
  回跳页或付款详情页。
