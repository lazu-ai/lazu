# Lazu Brand

`brand/` 是 Lazu 产品界面与品牌约束的唯一来源。这里保存当前规则和必要的决策历史，不保存一次性迁移账本、生成预览或本地设计产物。

## 阅读顺序

1. `BRAND.md`：当前锁定方向、覆盖关系与决议索引。
2. `foundations/yunmu-console.md`：Console 当前的云母视觉与信息架构。
3. 与任务直接相关的其他 `foundations/*.md`。
4. `tokens/`：机器可读的颜色、排版、圆角、阴影与动效约束。

若旧 foundation 与 `BRAND.md` 冲突，以 `BRAND.md` 中最新的锁定或覆盖声明为准。

## 目录边界

- `assets/`：Logo、厂商标志及其可复现生成脚本。
- `foundations/`：仍被当前产品引用的正式规则。
- `tokens/`：生产代码同步使用的 canonical tokens。
- `lazu-ops:brand/explorations/`：决策历史；只有被 `BRAND.md` 或 locked foundation 明确选中的方向可以实现。
- `styles.css`：Brand specimen 的样式入口。
- `SKILL.md`：供设计任务读取本目录的最小入口。

生产组件实现位于 `apps/web/src/components/ui/`，生成后的 Web token 位于 `apps/web/src/styles/brand.generated.css`。二者是 Brand 的消费者，不是第二套设计规范。

## 维护规则

- 保留被锁定决议或生产代码明确引用的最小设计证据及其 CSS、JS、图片依赖。
- 未被选中的一次性方向板、重复预览、设计工具 manifest/bundle 与截图不长期保留；删除前核对引用，历史版本由 Git 保存。
- 本地审查产物统一放到 `.local-results/`，任务结束后删除。
- 已完成的迁移计划不长期保留；可复用结论应写回 `BRAND.md` 或对应 foundation。
- 删除 Brand 文件前先搜索代码、文档和 locked foundation 的引用。
