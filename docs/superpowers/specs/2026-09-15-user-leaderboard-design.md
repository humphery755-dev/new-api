# 排行榜页面 · 用户排行榜区块 设计文档

- 日期：2026-09-15
- 状态：已确认
- 分支：v1alpha0.1

## 背景与目标

`/rankings` 页面现有四个区块：Hero（周期切换）、模型排行（ModelsSection）、厂商份额（MarketShareSection）、涨跌榜（PulseSection）。所有数据来自 `GET /api/rankings` 的单一快照响应，按 `quota_data` 表聚合，带 5 分钟内存缓存。

本设计在页面最下方新增第五个区块：**用户排行榜（User Leaderboard）**——全站用户按 Token 用量排名，固定展示 top 20。

## 已确认的需求决策

| 决策点 | 结论 |
|---|---|
| 区块定义 | 用户排行榜（全站用户排名），非"我的使用排行" |
| 排名指标 | `sum(token_used)`，与模型/厂商排行一致 |
| 用户名展示 | 直接显示全名（站点为内部/信任环境，无脱敏需求） |
| 周期切换 | 跟随页面顶部 today/week/month/year |
| "我的排名" | 不做 |
| 榜单长度 | 固定 top 20，无分页/加载更多 |

## 方案选择

采用**扩展现有 RankingsSnapshot**（方案 A）而非独立端点（方案 B）：

- 页面架构本来就是"一个快照响应包含全部区块数据"，方案 A 完全复用 5 分钟缓存、period 校验、统一 loading/error、单请求周期切换。
- 对现有代码的修改仅为追加字段和追加函数，插入点集中，合并冲突风险低。
- 方案 B（独立 `/api/rankings/users`）会导致双请求、双加载态、与单快照架构割裂，被否决。
- 方案 C（一条 SQL 同时取模型+用户两个粒度）因 GROUP BY 粒度不同需硬凑 UNION，牺牲三库兼容性，被否决。

## 后端设计

### `model/usedata_rankings.go` — 新增查询

新增 `RankingUserTotal` 结构与 `GetRankingUserTotals`：

```sql
SELECT user_id, username, sum(token_used) AS total_tokens
FROM quota_data
WHERE username <> '' AND created_at >= ? AND created_at <= ?
GROUP BY user_id, username
HAVING sum(token_used) > 0
ORDER BY total_tokens DESC
LIMIT 20
```

要点：

- 复用现有 `applyRankingQuotaTimeRange` 处理时间边界。
- GORM `Limit()` 三库（SQLite / MySQL / PostgreSQL）兼容。
- 按 `user_id, username` 分组（与现有 `GetQuotaDataByUsername` 模式一致）；`user_id` 保证聚合唯一性，`username` 取记录值。
- `WHERE username <> ''` 过滤空用户名的历史脏数据。
- SQL 层直接 `LIMIT 20`，避免全量用户聚合进入内存。

### `service/rankings.go` — 追加字段与组装

- 常量：`rankingUserLeaderboardLimit = 20`。
- 新结构：

```go
type RankedUser struct {
    Rank        int     `json:"rank"`
    UserID      int     `json:"user_id"`
    Username    string  `json:"username"`
    TotalTokens int64   `json:"total_tokens"`
    Share       float64 `json:"share"`
}
```

- `RankingsResponse` 追加 `Users []RankedUser`（JSON 字段 `users`）。追加式变更，对现有消费者向后兼容。
- `buildRankingsSnapshot` 中新增一次 `GetRankingUserTotals(startTime, endTime, rankingUserLeaderboardLimit)` 查询（错误直接上抛，不吞异常），并新增 `buildRankedUsers(totals, totalTokens)` 组装排名与占比。
- `share` 分母复用现有 `sumRankingTokens(currentTotals)` 全量总和，保证占比口径与模型排行一致。

### 零改动部分

- `controller/rankings.go`、`router/api-router.go`：不动，复用 `GET /api/rankings` 与 `HeaderNavModuleAuth("rankings")`。
- 缓存：`Users` 随快照自动进入现有 `rankingCache`（5 分钟 TTL，按 period 分 key）。
- 第一版不做环比 growth（需求未要求；后续如需可复用 `hasPrevious` 机制扩展）。

## 前端设计（`web/src/features/rankings/`）

- `types.ts`：追加 `UserRanking` 类型（`rank`、`user_id`、`username`、`total_tokens`、`share`）；`RankingsSnapshot` 追加 `users: UserRanking[]`。
- 新文件 `components/users-section.tsx`：`UsersSection` 组件。
  - 卡片风格与 `PulseSection` 一致：`bg-card rounded-lg border` + header（标题 + 描述 + 图标）。
  - 图标：lucide `Users`；标题 `User leaderboard`；描述 `Top users by token usage in this period`。
  - 表格 4 列：排名（前三名带高亮徽章）、用户名、Token 用量（复用 `lib/format.ts` 的 `formatTokens`）、占比（百分比）。
  - 空态文案：`No user activity in this period`。
- `components/index.ts`：导出 `UsersSection`。
- `index.tsx`：在 `PulseSection` 之后挂载 `<UsersSection rows={snapshot.users} />`；加载与错误状态沿用页面现有快照级处理，组件自身无请求逻辑。
- i18n：新增英文 key 写入 `web/src/i18n/locales/en.json` 与 `zh.json`，执行 `bun run i18n:sync` 同步 zh-TW/fr/ru/ja/vi。

## 错误处理与边界

- 查询失败：沿现有链路上抛（`GetRankingsSnapshot` 返回 error → controller 返回 400），不吞异常。
- 无用户流量（新站/空周期）：返回空数组，前端渲染空态。
- 用户改名：按 `user_id` 聚合保证唯一；`username` 显示当前记录值。

## 测试策略

- 后端：`service/rankings_test.go` 新增 `buildRankedUsers` 表驱动测试，覆盖排名序号、share 计算（含 totalTokens 为 0 的边界）、空输入。model 层 SQL 沿用现有模式，不新增 DB 集成测试（与 `GetRankingQuotaTotals` 同等待遇）。
- 测试框架遵循项目规范：`testify/require` + `testify/assert`。
- 前端：`UsersSection` 为纯展示组件（数据转换已在后端完成），遵循 `web/AGENTS.md` 既有约定，不强制新增测试。

## 涉及文件清单

| 文件 | 变更类型 |
|---|---|
| `model/usedata_rankings.go` | 追加（新结构 + 新查询函数） |
| `service/rankings.go` | 追加（常量、结构、字段、组装函数） |
| `service/rankings_test.go` | 追加（表驱动测试） |
| `web/src/features/rankings/types.ts` | 追加（类型） |
| `web/src/features/rankings/components/users-section.tsx` | 新增 |
| `web/src/features/rankings/components/index.ts` | 追加（导出） |
| `web/src/features/rankings/index.tsx` | 追加（挂载组件，1 处插入） |
| `web/src/i18n/locales/{en,zh}.json` | 追加（新 key） |
| 其余 locale 文件 | `bun run i18n:sync` 自动生成 |
