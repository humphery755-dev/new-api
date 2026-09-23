# 合并 upstream/main 到 v1alpha0.1 — 设计

**日期**: 2026-09-23
**目标分支**: v1alpha0.1
**上游**: upstream/main @ `c76452d22`（领先上次同步点 `69a500298` 共 **636 个提交**）

## 背景

v1alpha0.1 上次以 upstream/main `69a500298` 为基线重建，其上叠加了 14 个本地提交：
- **流量治理三模块**（6 个功能提交）：并发队列（concurrency queue）、SWRR 加权轮询渠道选择、渠道错误关键字规则引擎。见 `docs/superpowers/specs/2026-09-17-traffic-governance-design.md`。
- **用户排行榜**（8 个提交），见 `2026-09-15-user-leaderboard-design.md`。
- **workflow 还原提交**（1 个），用于解除 GitHub push 的 workflow-scope 限制。

本地定制集中在 36 个文件，约 4130 行新增。

## 目标

1. 将 upstream 最新代码（636 提交，458 文件变更）合并入 v1alpha0.1。
2. 保留全部本地定制特性（流量治理 + 排行榜），不被上游覆盖。
3. 遵循 workflow 推送约束（memory）——合并后还原 `.github/workflows` 为 origin 状态。
4. 推送策略：**先本地合并 + 构建通过，由用户审阅 diff 后再决定 force push**。

## 方案：merge 上游到 v1alpha0.1（已选定）

不做 rebase（会改写已 push 提交、逐个冲突、force push 风险高）。merge 保留本地定制历史，产生单个合并提交。

## 冲突预判

基于 merge-base `69a500298` 两边文件的差异分析：

| 文件 | 本地改动 | 上游改动 | 冲突性质 | 风险/处理 |
|---|---|---|---|---|
| `service/channel_select.go` | +19/-2 | +140 | 都改渠道迭代 | 中 — 确认本地轮询钩子与上游 SWRR 共存 |
| `service/relay_error.go` | +10 | +30/-16 | 都改错误分类 | 中 — 确认错误规则引擎与上游错误处理共存 |
| `model/option.go` | +24 | 未知 | 双方加 option key | 低 — 追加 switch 分支 |
| `router/relay-router.go` 等 | +2 等 | 未知 | 双方挂 middleware | 低 — 位置性 |
| 7 个 i18n locale JSON | 各 +17 | 大量 | 双方加 key | 低 — 纯追加但噪声大 |
| `web/.../section-registry.tsx` | +44 | 未知 | 双方注册 section | 低 — 追加 |

**关键事实**：上游**没有**自己的 ConcurrencyQueue / ChannelPolling / ErrorRule 配置项（唯一匹配 `relaykit/types/channel_error.go`，为与本项目无关的 DTO）。因此本地流量治理特性不会与上游同名功能冲突，仅在共享结构文件上产生可解的叠加冲突。

## 执行步骤

### 1. 预处理
- 已 fetch upstream（通过代理 `127.0.0.1:10086`）。
- 确认工作区干净（仅未跟踪文件）。

### 2. 执行合并
```
git merge upstream/main
```
- 对每个冲突文件，本地定制（流量治理/排行榜）优先保留，上游新增功能吸收。
- 高优先级手工确认：`service/channel_select.go`、`service/relay_error.go` 双向兼容。

### 3. workflow 推送约束还原（铁律）
```
git checkout origin/v1alpha0.1 -- .github/workflows
# 删除上游新增的 workflow 文件（若产生 add）
```
原因：GitHub OAuth 凭据缺 `workflow` scope，推送使 `.github/workflows/` 相对远端有 add/update 即被拒。参见 `[[github-push-workflow-scope]]`、`[[v1alpha0-1-rebuilt-upstream-plus-leaderboard]]`。

### 4. 验证
- `go build ./...` + `cd relaykit && GOWORK=off go build ./...`（relaykit 独立模块必须可构建，AGENTS.md 铁律）。
- `cd web && bun run build`。
- 冲突涉及 DB 访问路径时按 AGENTS.md 数据库铁律做三库（SQLite/MySQL/PostgreSQL）验证。
- 用户审阅合并结果后再决定推送。

## 成功标准

- [ ] merge 完成，本地 14 个提交 + 排行榜/流量治理特性代码完整保留。
- [ ] 全后端构建 + relaykit 独立构建 + 前端构建通过。
- [ ] `.github/workflows` 与 origin 一致（push-safe）。
- [ ] 用户审阅通过。