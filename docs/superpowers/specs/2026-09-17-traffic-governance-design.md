# 流量治理三模块设计:并发排队 · 加权轮询分发 · 渠道错误规则引擎

- 日期:2026-09-17
- 状态:已确认(用户逐节确认)
- 分支:v1alpha0.1

## 背景与目标

当前网关存在三个流量治理缺口:

1. **无并发排队**:同一用户突发请求直接打到上游,或被现有「模型请求限流」以 429 拒绝;缺少「并发超限 → 排队等待」的能力。
2. **渠道分发随机集中**:同优先级层内的渠道选择是「加权随机」(`model/channel_cache.go` `GetRandomSatisfiedChannel`),高权重渠道高度集中,客户端请求反复命中同一渠道。
3. **错误处理不可分类配置**:上游错误只有一种关键字动作(命中 `AutomaticDisableKeywords` → 禁用渠道);「限流」类错误虽默认只重试不禁用,但该行为不可配置、不可扩展。

三个模块相互独立,可分开交付;统一主题是「按客户端治理流量、按规则处置渠道错误」。

## 模块 1:用户并发排队中间件

### 需求(用户已确认的决策)

| 决策点 | 结论 |
|---|---|
| 限流维度 | 可配置,默认按用户(user id),可选 token |
| 排队超时 | 默认 30s,可配置;超时返回 429 |
| WebSocket/realtime | 排除,长连接不占槽位 |
| 配置粒度 | 并发上限按用户分组差异化 |

### 组件

| 文件 | 职责 |
|---|---|
| `setting/concurrency_queue.go`(新) | 配置状态与锁,仿 `setting/rate_limit.go` 模式 |
| `middleware/concurrency_queue.go`(新) | 中间件本体:per-key 槽位管理 + acquire/release |
| `model/option.go`(插 2 处) | OptionMap 默认值 + `updateOptionMap` 分支,经既有 `SyncOptions` 60s 热更新 |
| `router/` 6 处挂载点(每处 +1 行) | 与 `ModelRequestRateLimit()` 并列挂载 |
| `web/src` 运营设置(新卡片) | 「并发排队」配置 UI |

### 配置项

```
ConcurrencyQueueEnabled        bool   默认 false
ConcurrencyQueueScope          string 默认 "user"(可选 "token")
ConcurrencyQueueDefaultLimit   int    默认 2(分组未配置时;0 = 不限)
ConcurrencyQueueTimeoutSeconds int    默认 30
ConcurrencyQueueGroupLimit     JSON   {"<分组名>": <并发数>} 分组覆盖
```

### 数据流

- **Key**:`u:<userId>` 或 `t:<tokenId>`(由 scope 决定)。userId 取 `c.GetInt("id")`(TokenAuth 已写入)。
- **容量判定**:分组 = token group 优先、user group 兜底(与 `ModelRequestRateLimit` 的取法一致);`ConcurrencyQueueGroupLimit` 命中则用分组值,否则用 `ConcurrencyQueueDefaultLimit`;值为 0 表示该分组不限并发。
- **直通条件**:开关关闭 / 有效容量 0 / WebSocket Upgrade 请求(检查请求头 `Upgrade: websocket`;挂载点在 `relayV1Router` 上会覆盖 `/v1/realtime`,必须在中间件内识别直通)。
- **Acquire**:
  1. 全局锁内取/建 `slotQueue{slots chan struct{}(cap=N), waiting int, inUse int}`,`waiting++`,释放锁;
  2. 锁外 `select` 三路等待:从 `slots` 取得槽位 / `time.After(timeout)` / `c.Request.Context().Done()`(客户端断开)。
- **Release**:`defer` 在 `c.Next()` 返回后归还槽位并锁内 `inUse--`。gin 的 `c.Next()` 同步等待 handler 完成,流式响应写完才返回,天然覆盖完整响应周期。
- **回收**:release 时锁内检查 `waiting==0 && inUse==0` 则 `delete(map, key)`,防止 map 无限增长;等待者在 select 前已计入 `waiting`,不会被误回收。

### 错误处理

- **排队超时**:429 + OpenAI 格式错误体(复用 `abortWithOpenAiMessage`),消息含分组、并发上限、等待秒数;响应头 `Retry-After` = 配置超时秒数。
- **客户端断开**:静默返回,不写响应(连接已死)。
- **无 Redis 依赖**:纯进程内存实现。
- **多实例语义**:限额为 per-instance(代码注释与配置说明注明);当前部署单实例承载流量,精确生效;未来多实例可平滑替换为 Redis 分布式信号量(key 与配置模型不变)。

## 模块 2:客户端+模型 加权轮询分发

### 需求

同一客户端对同一模型的请求,在所有可用渠道(同优先级层)按权重**轮流**命中,而非加权随机导致的集中命中。

### 算法

- 在 `model/channel_cache.go` `GetRandomSatisfiedChannel` 的同优先级候选层(`targetChannels`)选出后新增分支:
  - `ChannelPollingEnabled == false`(默认):走原「加权随机」路径,**零行为变化**;
  - `true`:走**平滑加权轮询(SWRR,nginx 同款)**:每渠道维护 `currentWeight`,每轮 `cw += weight`,选 `cw` 最大者,选中者 `cw -= totalWeight`。
- **优先级分层保持不动**:retry 降级机制依赖 priority 层,轮询只在同层候选内生效。

### 轮询状态

- Key:`clientKey|model`。clientKey = `u:<userId>` 或 `t:<tokenId>`,由 `ChannelPollingScope`("user"|"token",默认 "user")决定。
- 存储:进程内 `map[string]*swrrState` + RWMutex。
- **失效检测**:state 内保存候选集签名(channel ids + weights 的 hash);候选集变化(渠道增删、权重修改、禁用、60s 缓存刷新)时签名不匹配即重建 state。
- 状态非持久化:进程重启从零开始,无正确性影响。

### 配置项

```
ChannelPollingEnabled  bool   默认 false
ChannelPollingScope    string 默认 "user"(可选 "token")
```

### 边界

- 多 key 渠道内部的 key 轮询(MultiKeyModePolling)不受影响,两层轮询正交。
- 多实例语义同模块 1:轮询状态 per-instance。

## 模块 3:渠道错误关键字规则引擎

### 需求

对渠道返回的错误消息按关键字识别,执行差异化动作,替代「只有禁用一种动作」的现状。

### 规则配置

`setting/operation_setting` 新增 `ChannelErrorKeywordActions`,JSON 数组,按序匹配,首条命中生效:

```json
[
  {"keywords": ["余额不足", "无可用资源包", "insufficient quota"], "action": "disable"},
  {"keywords": ["限流", "rate limit", "too many requests"], "action": "retry_next"},
  {"keywords": ["内容安全", "sensitive content"], "action": "passthrough"}
]
```

默认值即上表三条(含本次智谱 1113「余额不足或无可用资源包」场景)。

### 动作语义

| 动作 | 禁用渠道 | 换渠道重试 | 客户端可见 |
|---|---|---|---|
| `disable` | 是(现有 `DisableChannel` 路径,含通知) | 是 | 429/上游错误 |
| `retry_next` | 否 | 是 | 重试耗尽后 429/上游错误 |
| `passthrough` | 否 | 否 | 原样上游错误 |

### 挂接方式(Add Over Modify)

- 新增 `service/channel_error_rules.go`:`MatchChannelErrorRule(err *types.NewAPIError) (action string, rule string, ok bool)`。匹配输入为小写化的 `err.Error()`,检索复用现有 AC 自动机 `AcSearch`(`service/str.go:132`)。
- `service.ShouldDisableChannel`(`service/channel.go:57`)开头加前置分支:规则命中 `disable` → `true`;命中 `retry_next`/`passthrough` → `false`;未命中回落现有逻辑。
- `service.ShouldRetryRelayError`(`service/relay_error.go:19`)开头加前置分支:规则命中 `retry_next` → `true`;命中 `passthrough` → `false`;命中 `disable` → 走现有逻辑(现有 `IsChannelError`/状态码判断通常已允许重试);未命中回落现有逻辑。
- **共存而非替换**:`AutomaticDisableKeywords`、`ShouldDisableByStatusCode`、`ShouldRetryByStatusCode` 等现有机制在规则未命中时原样生效。
- `retry_next` 的重试次数仍受全局 `common.RetryTimes` 与优先级层数约束,不会无限重试。

### 配置热更新

经既有 `SyncOptions` 机制:OptionMap 注册 + `updateOptionMap` 分支 + 校验函数(动作枚举、关键字非空)。

## 测试策略

均按 AGENTS.md:testify require/assert,表驱动,fixture 内显式初始化状态;不散落多文件。

- **模块 1**(`middleware/concurrency_queue_test.go`):
  - 并发 2 时第 3 个请求排队,前序完成后放行;
  - 排队超时返回 429 + `Retry-After`;
  - 等待中客户端断开,释放槽位且不写响应;
  - WS Upgrade 请求直通;开关关/容量 0 直通;
  - scope=user 与 token 的 key 隔离;分组容量覆盖;未配置分组用默认值;
  - 空闲 key 回收(map 长度不增长)。
- **模块 2**(`model/channel_cache_swrr_test.go` 或并入现有 channel_cache 测试文件):
  - 权重 2:1 的两渠道,4 次连续选择呈现 2:1 轮流序列(SWRR 确定性);
  - 候选集签名变化后 state 重建;
  - 开关关闭时与原随机路径行为一致(不改返回契约)。
- **模块 3**(`service/channel_error_rules_test.go`):
  - 三种动作分别短路 `ShouldDisableChannel`/`ShouldRetryRelayError` 的预期值;
  - 未命中规则时回落现有逻辑(禁用关键字、状态码表);
  - 多条规则按序首条生效;校验函数拒绝非法动作。

**数据库验证**:三模块均无 schema 变更(配置走既有 options 表机制),不触发三库矩阵;`model/option.go` 的 OptionMap 注册按现有模式,启动时默认值写入既有流程。

## 交付顺序

模块 1 → 模块 2 → 模块 3,各自独立提交、独立验证。前端「渠道调度」配置区(轮询开关/scope、排队卡片、错误规则表编辑)随各模块交付。

## 明确不做(YAGNI)

- Redis 分布式信号量 / 分布式 SWRR(单实例部署,接口留演进空间);
- 多 key 渠道的单 key 禁用动作(`disable_key`);
- 按模型分组的并发上限(分组即用户分组,不按模型拆);
- 轮询状态持久化。
