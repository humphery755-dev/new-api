# changes-todo.md — v1alpha0.1 定制功能登记簿

> **用途**：本分支（v1alpha0.1）在 upstream（QuantumNous/new-api）main 之上的定制功能登记簿。
> **AI 必读**：每次合并 upstream 或修改任何定制功能之前，先通读本文档，确认定制点分布，避免误删/漏合。
> 最后更新：2026-10-23（配置外置化重构已完成：5 项定制配置全部统一到 custom-config.yaml）

---

## 一、合并 upstream 标准流程

```bash
# 0. 全程在仓库根执行！子目录里跑 git pathspec/ls-tree/diff -- <path> 会静默假阴性
cd /home/humphery/workspace/proxy/new-api-dev

# 1. 保护未提交的定制修改（如 adaptor.go）
git stash push -m "WIP: pre-upstream-merge $(date +%F)"

# 2. 拉取并合并
git fetch upstream
git merge upstream/main --no-edit

# 3. 冲突处理原则（见下方"冲突高发点"）

# 4. 验证
go build ./...
go test ./router/... ./middleware/... ./service/... ./setting/... ./model/... ./relay/channel/claude/...
# locale JSON 有效性（若自动合并过 web/src/i18n/）
cd web && for f in src/i18n/locales/*.json; do python3 -m json.tool "$f" >/dev/null || echo "BAD: $f"; done && cd ..

# 5. 恢复 stash
git stash pop
```

### 冲突高发点与处理原则

| 冲突点 | 原则 |
|---|---|
| `.github/workflows/*` | **一律取 ours（HEAD）**。fork 凭据缺 workflow scope，workflows 内容相对 origin 有变更的推送会被拒。合并后 `git diff origin/v1alpha0.1 HEAD -- .github/` 必须为空 |
| `router/task-plugin-protocol-router.go` | 我们插入了 `ConcurrencyQueue()`；upstream 曾把 `PinTaskPluginEndpoint` 改为 `RouteTaskPluginEndpoint`（880211326）。合并时两者都要保留：upstream 新中间件名 + 我们的 ConcurrencyQueue |
| `relay/channel/claude/adaptor.go` | missing-thinking patch 所在文件，upstream 也常改（billing）。我们的改动集中在 `ConvertClaudeRequest` 调用 patch 判断 + 文件尾部 patch 函数 + `setting.PatchMissingThinkingChannels` |
| `service/channel.go` `ShouldDisableChannel` | 错误规则引擎在此短路 upstream 逻辑 |
| `web/src/i18n/locales/*.json` | 自动合并后必须验证 JSON 有效性；upstream 有 `chore(web): sort locale keys` 类提交 |

### 已知陷阱

- **git pathspec cwd 陷阱**：`cd web` 等操作后，子目录里带路径参数的 git 命令（diff/ls-tree/grep -- path）会静默返回空，造成"文件丢失"假象。git 命令前先回仓库根。
- **推送前检查**：`git diff origin/v1alpha0.1 HEAD --stat -- .github/` 为空才可推送。

---

## 二、定制功能登记

### 1. 用户排行榜（保留前端界面）

- **状态**：✅ 完成
- **行为**：rankings 页新增用户用量排行榜区块，users 表 JOIN 取当前用户名，排序稳定化 + 非空契约锁定。
- **文件**：
  - 后端：`service/rankings.go`（+43）、`service/rankings_test.go`（+59）、`model/usedata_rankings.go`（+32）
  - 前端：`web/src/features/rankings/`（users-section.tsx +98、index.tsx、types.ts、components/index.ts）
  - i18n：7 locale 各有排行榜 keys（**清理 locale 时保留这部分**）
- **合并注意**：与 upstream 的 rankings 改动冲突时保留功能侧。

### 2. Claude missing-thinking patch

- **状态**：✅ 完成（配置并入 custom-config.yaml，见下）
- **行为**：GLM 风格 Anthropic 兼容上游对"思考链断裂"的历史回复 400；列入 `claude_missing_thinking_patch.channel_ids` 的渠道，在最后一条 assistant 消息缺 thinking 块时插入占位块。**官方 Anthropic 渠道禁止列入**（占位块无 signature 会被拒）。临时 workaround，上游不再校验思考链透传后移除。
- **文件**：`relay/channel/claude/adaptor.go`（`patchMissingThinkingBlock` / `contentHasThinkingBlock` + `ConvertClaudeRequest` 内的判断）、`relay/channel/claude/adaptor_test.go`
- **配置**：`custom-config.yaml` → `claude_missing_thinking_patch.channel_ids`（默认空 = 不 patch）
- **合并注意**：upstream 常改 adaptor.go（billing），冲突时保留 patch 函数与判断调用。

### 3. 并发队列（per-client concurrency queue）

- **状态**：✅ 完成（配置并入 custom-config.yaml）
- **行为**：同一客户端（按 user 或 token 维度）并发超限时排队等待，超时返回超时错误；WebSocket 旁路。**per-instance（进程内存态），单实例部署精确生效**。
- **文件**：
  - `middleware/concurrency_queue.go`（139 行）+ `middleware/concurrency_queue_test.go`（280 行）
  - 挂载：`router/relay-router.go`（2 处）、`router/plugin-router.go`、`router/video-router.go`、`router/task-plugin-protocol-router.go`（2 处）
- **配置**：`custom-config.yaml` → `concurrency_queue.{enabled,scope,default_limit,timeout_seconds,group_limits}`，默认 enabled=false
- **合并注意**：upstream 改这些 router 文件的中间件链时，保留 `ConcurrencyQueue()` 插入。

### 4. SWRR 加权轮询渠道选择（per-client weighted polling）

- **状态**：✅ 完成（配置并入 custom-config.yaml）
- **行为**：开启后同一客户端对同一模型在同优先级候选渠道内按权重平滑轮询（SWRR），关闭时保持 upstream 加权随机。内存缓存关闭（DB 直查）路径不参与轮询。
- **文件**：
  - `model/channel_cache_swrr.go`（82 行）+ 测试（91 行）
  - `model/channel_cache.go`：`GetRandomSatisfiedChannelWithClient`（+19）
  - `service/channel_select.go`：`GetPollingClientKey` + 调用切换（+21）、`service/channel_select_polling_test.go`（47 行）
- **配置**：`custom-config.yaml` → `channel_polling.{enabled,scope}`，默认 enabled=false
- **合并注意**：`model/channel_cache.go` 的 `GetRandomSatisfiedChannel` 是 upstream 热点函数，冲突时保留 WithClient 包装与轮询分支。

### 5. 渠道错误关键词规则引擎（channel error keyword rules）

- **状态**：✅ 完成（配置并入 custom-config.yaml）
- **行为**：按序匹配错误消息关键词，首条命中执行动作：`disable`（禁用渠道）/ `retry_next`（换渠道重试）/ `passthrough`（原样返回）。命中即短路 `ShouldDisableChannel` 原生逻辑；**未命中回落 upstream 原生行为**。**独立于 `AutomaticDisableChannelEnabled` 全局开关**——`retry_next`/`passthrough` 在全局自动禁用关闭时仍生效。
- **默认规则**（配置缺失/未配置 rules 时使用）：余额类（"余额不足"、"无可用资源包"、"insufficient quota"）→ disable；限流类（"限流"、"rate limit"、"too many requests"）→ retry_next；内容安全类（"内容安全"、"sensitive content"）→ passthrough。
- **文件**：
  - `setting/operation_setting/channel_error_actions.go`（类型 + 默认规则）
  - `service/channel_error_rules.go`（MatchChannelErrorAction，复用 AcSearch）+ 测试（110 行）
  - `service/channel.go` `ShouldDisableChannel` 接入（+8）
- **配置**：`custom-config.yaml` → `channel_error_rules.rules[]`（keywords + action）

### 6. DisableTokenGroupOverride（令牌分组不覆盖账户分组）

- **状态**：✅ 完成（配置并入 custom-config.yaml）
- **行为**：开启后 API token 上设置的分组不再覆盖用户账户分组，渠道分发强制按账户分组。默认 false 保持 upstream 行为。
- **文件**：`setting/token_group_override.go`、`middleware/auth.go`（TokenAuth 内 1 处判断）
- **配置**：`custom-config.yaml` → `token_group_override.disable`

---

## 三、统一配置文件

全部定制功能共用**一个**配置文件（用户明确要求不分散）：

- 路径：env `CUSTOM_CONFIG_PATH` 覆盖，默认 `./custom-config.yaml`
- 加载：`main.go` `InitResources()`（godotenv 之后）→ `setting.LoadCustomConfig`（`setting/custom_config.go`）
- 语义：**文件不存在 → 各功能内置默认值（除错误规则引擎用默认规则外全部关闭）；文件存在但解析/校验失败 → 启动 FatalLog 退出**（禁止静默降级）
- 修改配置需**重启**进程生效（不支持热重载）
- 示例：`custom-config.example.yaml`（仓库根）

### 从旧 DB option 迁移

配置外置化前，功能 3/4/5/6 走 DB 系统设置（option keys：`ConcurrencyQueue*`、`ChannelPolling*`、`ChannelErrorKeywordActions`、`DisableTokenGroupOverride`）。这些 key 已从 `model/option.go` 移除，DB 中的旧值被忽略。迁移：从管理界面或 `options` 表抄当前值到 `custom-config.yaml` 对应区块。missing-thinking patch 原独立文件 `patch_missing_thinking.yaml`（顶层 `channel_ids`）已并入 `claude_missing_thinking_patch.channel_ids`。

---

## 四、待办（TODO）

- [x] 2026-10-23：配置外置化重构（本文件第二节所列 5 项配置全部并入 custom-config.yaml；删除前端 3 个设置区块、恢复 model/option.go 与 web/src/features/system-settings/ 为 upstream 原样）
- [ ] missing-thinking patch 为临时 workaround：上游不再校验思考链透传后移除（adaptor.go 内 grep "GLM"）
- [ ] 排行榜区块的 admin 权限细化（如需对非 root 隐藏，当前跟随 rankings 页权限）
