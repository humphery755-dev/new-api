# changes-todo.md — v1alpha0.1 定制功能登记簿

> **用途**：本分支（v1alpha0.1）在 upstream（QuantumNous/new-api）main 之上的定制功能登记簿。
> **AI 必读**：每次合并 upstream 或修改任何定制功能之前，先通读本文档，确认定制点分布，避免误删/漏合。
> 最后更新：2026-10-26（新增 Claude server-tool-use 修复：GLM 编码端点产生的 server_tool_use + 内嵌 tool_result 历史在 MiniMax 等严格上游报 400 "tool id not found (2013)"，列入白名单的渠道改写为标准配对）

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
| `relay/channel/claude/adaptor.go` | missing-thinking patch 的**接线点**（import + `ConvertClaudeRequest` 内 4 行调用判断）所在文件；patch 实现已外移到 `adaptor_missing_thinking.go`。upstream 也常改此文件（billing），冲突时保留接线点与 `setting.PatchMissingThinkingChannels` |
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
- **文件**（定制实现已全部外移到新文件，upstream 已有文件仅留接线）：
  - 后端接线：`service/rankings.go`（+6：`Users` 字段 + 数据获取 + 响应组装）
  - 后端新文件：`service/rankings_users.go`（RankedUser/buildRankedUsers/常量）、`service/rankings_test.go`（+59）、`model/usedata_rankings_users.go`（RankingUserTotal/GetRankingUserTotals；原 `model/usedata_rankings.go` 已还原 upstream）
  - 前端：`web/src/features/rankings/`（users-section.tsx 新文件 +98；index.tsx/types.ts/components/index.ts 各 1-10 行接线）
  - i18n：7 locale 各有排行榜 keys（**清理 locale 时保留这部分**）
- **合并注意**：与 upstream 的 rankings 改动冲突时保留功能侧。

### 2. Claude missing-thinking patch

- **状态**：✅ 完成（配置并入 custom-config.yaml，见下）
- **行为**：会校验"思考链"的 Anthropic 兼容上游（DeepSeek `/anthropic` 端点、GLM 风格端点）在 **thinking 开启 + 请求以 `tool_result` 结尾 + 发起 tool_use 的 assistant 回合缺 thinking 块** 时返回 400 `The content[].thinking in the thinking mode must be passed back to the API`。同一会话被负载均衡轮换到不产生 thinking 块的上游（如 MiniMax adaptive 模式下的 tool_use 回合）时，历史中会残留缺 thinking 的 tool_use 回合，下一次落到严格上游且以 tool_result 结尾的请求即命中；**必须修复历史中全部此类回合**（实测只修一处仍 400，全部修复才 200）。列入 `claude_missing_thinking_patch.channel_ids` 的渠道，为历史中每个"含 tool_use 但无 thinking"的 assistant 回合插入占位 thinking 块。**官方 Anthropic 渠道禁止列入**（占位块无 signature 会被拒）。临时 workaround，上游不再校验思考链透传后移除。
- **文件**：
  - 接线：`relay/channel/claude/adaptor.go`（+8：import + `ConvertClaudeRequest` 内调用判断）
  - 新文件：`relay/channel/claude/adaptor_missing_thinking.go`（`patchMissingThinkingBlock` / `contentHasThinkingBlock` / `contentHasToolResultBlock` / `contentHasToolUseBlock`）、`relay/channel/claude/adaptor_missing_thinking_test.go`（测试；原 `adaptor_test.go` 已还原 upstream）
- **配置**：`custom-config.yaml` → `claude_missing_thinking_patch.channel_ids`（默认空 = 不 patch；本机现为 `[5, 10, 14]`）
- **合并注意**：upstream 常改 adaptor.go（billing），冲突时保留接线点与 patch 函数。

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
  - `model/channel_cache.go`：`GetRandomSatisfiedChannel` 增加可选 variadic 参数 `pollingClientKey ...string` + 轮询分支（+10/-2；**不再有 WithClient wrapper**，upstream 函数体保持原位）
  - `service/channel_select.go`：`pollingClientKey` 获取与传入（+3）
  - `service/channel_select_polling.go`（新）：`GetPollingClientKey` 外移至此（与 channel_select_polling_test.go 配对）
- **配置**：`custom-config.yaml` → `channel_polling.{enabled,scope}`，默认 enabled=false
- **合并注意**：`model/channel_cache.go` 的 `GetRandomSatisfiedChannel` 是 upstream 热点函数，冲突时保留 variadic 参数与轮询分支。

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

### 7. Claude server-tool-use 修复（server_tool_use + 内嵌 tool_result 归一化）

- **状态**：✅ 完成（配置并入 custom-config.yaml）
- **行为**：GLM 编码端点（bigmodel glm-coding-plan）会把客户端工具调用回成 assistant 消息内的 `server_tool_use` 块，并把服务端执行结果以 `tool_result` 块**内嵌在同一 assistant 回合**（而非标准 user 回合）。Claude Code 原样回放该历史；严格校验 tool id 的上游拒绝它——实测三种拒绝方式：MiniMax 400 `invalid params, tool result's tool id(...) not found (2013)`（只从 `tool_use` 块登记 id，且含内嵌 `tool_result` 的 assistant 消息整条不登记）；DeepSeek 400 `` `tool_use` ids were found without `tool_result` blocks immediately after ``（要求每个 assistant 回合以 `tool_use` 结尾、其 `tool_result` 在紧随其后的消息）；tydic-glm（172.168.5.101:9088，TYDIC-LLM 平台）400 Pydantic 校验 `Input tag 'server_tool_use' found using 'type' does not match any of the expected tags`（不认识该块类型）。列入 `claude_server_tool_use_patch.channel_ids` 的渠道改写：`server_tool_use`→`tool_use`，内嵌 `tool_result` 按 tool_use_id 配对提取，并把该 assistant 回合按每个 `tool_use` 切分为「`tool_use` 结尾 + 紧随 user 消息携带其 `tool_result`」的标准配对（连续 assistant 回合合并，避免角色连排）。实测改写后 MiniMax、DeepSeek、tydic 在 thinking+stream 真实条件下均 200。**产生该形状的 GLM 渠道不要列入**（其自身接受该形状，改写反伤 prompt cache）。防御性 patch：任何上游产生该形状都会毒化 MiniMax/DeepSeek/tydic 等严格上游。
- **文件**：
  - 接线：`relay/channel/claude/adaptor.go`（+8：import 已复用 `setting`，`ConvertClaudeRequest` 内调用判断）
  - 新文件：`relay/channel/claude/adaptor_server_tool_use.go`（`patchServerToolUseHistory`）、`relay/channel/claude/adaptor_server_tool_use_test.go`（测试）
  - 配置结构：`setting/custom_config.go`（`ClaudeServerToolUsePatch` + `PatchServerToolUseChannels`）
- **配置**：`custom-config.yaml` → `claude_server_tool_use_patch.channel_ids`（默认空 = 不 patch；本机现为 `[5, 10, 14]`）
- **合并注意**：与 missing-thinking patch 同处 `relay/channel/claude/adaptor.go`，冲突时保留两个接线点。

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
- [x] 2026-10-26：新增 Claude server-tool-use 修复（见第二节 7）
- [ ] missing-thinking patch 为临时 workaround：上游不再校验思考链透传后移除（adaptor.go 内 grep "GLM"）
- [ ] server-tool-use 修复为防御性 patch：若 GLM 编码端点不再内联 tool_result / MiniMax 不再校验该形状，可复测后考虑移除
- [ ] 排行榜区块的 admin 权限细化（如需对非 root 隐藏，当前跟随 rankings 页权限）
