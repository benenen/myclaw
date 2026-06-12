# 大脑-子 agent A2A 编排架构设计

- 日期: 2026-06-12
- 状态: 已确认设计,待实现计划

## 背景与目标

当前 myclaw 的消息链路是 **渠道 → Bot → 单个 agent 会话**:

```
渠道/hook → BotMessageOrchestrator.HandleEvent
  → 每个 bot 一个 worker 协程 → processMessage
    → resolver.Resolve(botID) → agent.Spec   (决定用哪个 CLI/模式/命令/工作目录)
    → executor.Send(botID, spec, req)         (executor = agent.Manager → Driver → Session.Run)
    → replyGateway.Reply(target, resp)
```

目标是引入一个**大脑 agent**,负责"理解、拆解任务、分发、监控、调度",其下挂多个**注册的子 agent**。大脑与子 agent 之间用 **A2A 协议**通信。渠道只跟大脑交互,子 agent 是大脑内部的执行编队。

核心设计原则:**编排智能(理解/拆解/调度/聚合)由大脑 agent 进程(LLM)自己完成,myclaw 不写编排逻辑代码**;myclaw 只负责提供子 agent 注册、A2A 派发通路、任务状态记账这些"基础设施"。

## 关键设计决策

下列决策是设计树自顶向下逐个确认的结果。

### D1. 大脑的位置:复用现有 executor
大脑就是一个**普通 Bot**(默认跑 `claude-acp`),经现有 `agent.Manager.Send → Session.Run` 拉起。**不为它新写 `BrainExecutor`**。拆解/调度等智能由大脑 LLM 进程自己干。

### D2. A2A 角色方向:大脑是 client,子 agent 是 server
A2A 里 "server" 指**被调用、接活执行**的一方,"client" 指**主动发起请求**的一方。大脑主动派活 → client;子 agent 被调用执行 → server。

- 任务的状态/进度/结果通过大脑发起的那条连接回流(A2A Task 状态机 + SSE),**大脑不需要当 server**。
- 仅当未来需要子 agent 脱离原连接异步主动回呼时,才需给大脑加 webhook 端点(可选高级特性,v1 不做)。

### D3. 子 agent A2A server 的托管:myclaw 内置
本地子 agent 是 myclaw 内的本地 CLI(codex/claude/opencode 经 `Manager` 跑),它们不会说 A2A。由 myclaw 在 `Manager` 外面提供 A2A 语义层。注册/发现也落在 myclaw。

### D4. 大脑调用 A2A 的"手":MCP 工具 + myclaw 桥接
大脑(Claude Code)原生支持 MCP。myclaw 提供一个 MCP server,工具:`list_agents()` / `dispatch(agent, subtask)` / `get_task(taskId)` / `cancel(taskId)`。

- **大脑侧 = MCP 工具**(理解/拆解/决定派给谁)
- **子 agent 侧 = A2A server**(执行)
- myclaw 在中间做 MCP→A2A 桥接 + 监控 + 任务记账
- A2A 仍是真协议结构(Task 生命周期、Agent Card),本地这一跳暂走进程内直调,留好以后换真 HTTP 远程的口子

### D5. 子 agent 实体(本地):复用 `Bot`
本地子 agent 复用 `Bot` 实体,新增 `type=subagent` 值(不绑渠道)+ 一个**技能描述字段**(喂 Agent Card,让大脑判断派给谁)。`dispatch` 按 `botID` 走现有 `Manager.Send`,独立 session、独立 workspace 都已具备。

### D6. 注册范围:v1 即做动态远程注册
v1 就支持外部独立进程经 `POST /a2a/register`(带 Agent Card + A2A 端点 + 鉴权)自助加入,含心跳保活、超时下线。

### D7. 注册表数据模型:独立的 `RegisteredAgent` + `kind` 分流
新建统一注册表 `RegisteredAgent`,字段含:名字、技能描述、`kind`(local/remote)、健康状态、最后心跳时间。

- `kind=local` → 引用一个 `Bot.ID`,派活走进程内 `Manager.Send`
- `kind=remote` → 存 A2A 端点 URL + 鉴权,派活走真 A2A HTTP client
- 大脑 `list_agents()` 读这张表;本地 Bot 子 agent 启动自动登记,远程经 `/a2a/register` 登记

`Bot` 退回去只管"本地 agent 怎么跑";注册中心状态(心跳/健康)不长在 Bot 上。

### D8. 派活模型:异步任务式
`dispatch(agent, subtask)` **立即返回 taskId**(A2A Task 状态机:submitted→working→completed/failed)。大脑随后 `get_task(taskId)` 轮询/取结果。支持一次扇出多个子任务、并行、监控、失败改派/重试。

引出:需要一个 **Task store**(taskId → 状态/结果/派给谁/时间),作为监控数据源与 `get_task` 的统一出处。v1 内存实现,后续可落 SQLite。

### D9. MCP server 托管:myclaw 内置 HTTP/SSE
myclaw 进程内挂一个 MCP 端点(如 `/mcp`),工具实现直接读写**同进程的注册表 + Task store + Manager**,零 IPC。拉起大脑时在 `claude-acp` 启动参数注入 `--mcp-config` 指向该端点。一个 myclaw 可服务多个大脑会话。

### D10. 大脑标记:`Bot.Role=orchestrator`(与 capability/mode 正交)
"是不是大脑"是个**角色**,与"用哪个 CLI/什么 mode"正交。任意 CLI(默认 claude-acp)都能当大脑。`cli_resolver` 在解析 `Role==orchestrator` 的 bot 时,自动:

- 把 MCP 配置注入启动参数
- 用 `--append-system-prompt` 注入**编排者系统提示**(教它拆解/调度/聚合循环 + 工具用法)
- 给一个**单独的大超时**(`OrchestratorTimeout`,分钟级)

编排逻辑写在 myclaw 注入的系统提示里 → 可复现、可版本化。

### D11. 本地 vs 远程派发路径
- **本地子 agent**:`kind=local` 用一个实现了同样 A2A Task 语义的**进程内适配器**直接 `Manager.Send`,不走 HTTP。
- **远程子 agent**:`kind=remote` 走真 A2A HTTP client(JSON-RPC + SSE);远程进程自己是 A2A server。
- **v1 myclaw 不对外暴露 A2A server**,只暴露 `/mcp`(给大脑)和 `/a2a/register`(给远程子 agent);对远程子 agent 它是 client。

### D12. 回复时机:先 ack 后异步推
渠道收到消息后:

1. **立即 ack**:myclaw 用入站 `ReplyTarget` 发一条 canned "收到,处理中…"
2. **派生独立 goroutine** 跑大脑(用 `OrchestratorTimeout`,脱离入站 worker 的 `processingTimeout`)
3. 大脑聚合出最终答案后,用**存下的 `ReplyTarget`** 异步 push 给用户

可行性依据:微信"回复"是独立出站 API 调用(`SendTextMessage`),完全由 `ReplyTarget`(recipient + base_url + token + wechat_uin + context_token)参数化,不绑 HTTP 应答周期。捕获后可随时再发。

去重:消息保持 `inProgress` 直到异步编排完成(由异步 goroutine 完成时 `finishMessage`),避免长任务期间重复入站消息触发重复编排。

## 架构拓扑

```
渠道(微信) ──消息──> 编排器(现有,几乎不动)
                       │
            ① 立即 ack "收到,处理中…"(用入站 ReplyTarget)
            ② 派生独立 goroutine 跑大脑(OrchestratorTimeout)
                       │
         大脑Bot(Role=orchestrator, 跑 claude-acp, 现有 Manager.Send)
            │  myclaw 注入: MCP配置(/mcp) + 编排者系统提示
            │
            └─MCP工具─> myclaw 桥接层 ──┐
               list_agents               │ 读注册表
               dispatch(agent,subtask)   │ 建 task, 按 kind 分流
               get_task(taskId)          │ 读 Task store
               cancel(taskId)            │
                                         ▼
                          ┌──────────────┴───────────────┐
                  kind=local                        kind=remote
              Manager.Send(子Bot)              真 A2A HTTP client
              (进程内直调,A2A语义适配器)        (JSON-RPC + SSE)
                                                       ▲
                                          远程子agent (自己是A2A server)
                                          经 POST /a2a/register 登记
            │
            ③ 大脑聚合出最终答案 → 用存下的 ReplyTarget 异步 push 给用户
```

## 组件清单(均在 myclaw 进程内)

| 组件 | 职责 | 改动类型 |
|---|---|---|
| `RegisteredAgent` 注册表 | 统一本地/远程子 agent,`kind`+健康+心跳 | 新增(实体+repo+表) |
| 注册中心 HTTP | `POST /a2a/register` + 心跳/下线 | 新增 |
| Task store | taskId→状态/结果/派给谁/时间,监控数据源 | 新增(v1 内存,后续可落 SQLite) |
| MCP server | `/mcp`,工具 list/dispatch/get/cancel | 新增 |
| A2A 派发器 | `kind` 分流:local→Manager.Send;remote→A2A HTTP client | 新增 |
| `Bot.Role` + 技能描述字段 | 标记 orchestrator;`type=subagent` 本地子 agent | 改 entity/schema |
| `cli_resolver` | Role=orchestrator 时注入 MCP配置+系统提示+大超时 | 改 |
| 编排器 `processMessage` | orchestrator 路径:先 ack→派生 goroutine→完成后 push | 改 |
| 编排者系统提示 | 教大脑拆解/调度/聚合循环 + 工具用法 | 新增(prompt 资产) |

## 数据流(端到端)

1. 用户在微信发消息 → `RuntimeEvent` → 编排器 `HandleEvent`。
2. 编排器 `processMessage` 解析出 orchestrator spec(含 MCP 配置、系统提示、大超时)。
3. 立即用入站 `ReplyTarget` 发 ack;把 `ReplyTarget` 存入异步上下文。
4. 派生独立 goroutine 调 `Manager.Send(brainBotID, spec, req)`,大脑 claude-acp 会话开始一轮。
5. 大脑 LLM 在这一轮内:`list_agents` → 拆解 → 多次 `dispatch` → 循环 `get_task` 监控 →(失败可改派/`cancel`)→ 聚合。
6. 每个 `dispatch` 在 myclaw 建一个 Task,按 `kind` 分流:local 进程内 `Manager.Send` 子 agent Bot;remote 走 A2A HTTP。
7. 大脑吐出最终答案,goroutine 用存下的 `ReplyTarget` 异步 push 给用户,并 `finishMessage`。

## 错误处理

- **子任务失败**:Task 置 `failed`,`get_task` 暴露失败态;大脑(LLM)自行决定重试/改派(myclaw 不写重试逻辑,只提供 `dispatch`/`cancel`/状态)。
- **大脑整轮超时**:`OrchestratorTimeout` 到期取消 goroutine,push 一条失败兜底文案。
- **远程子 agent 不可达/心跳超时**:注册表标记 unhealthy,`list_agents` 过滤掉;进行中的 task 置 failed。
- **push 失败(如 context_token 过期)**:记日志 + 有限重试;长任务下 token 过期为已知风险,v1 降级处理。

## 测试策略

- 注册表 repo:本地自动登记 + 远程 `/a2a/register` + 心跳超时下线的单测(in-memory SQLite)。
- Task store:状态机流转、并发 dispatch、cancel 的单测。
- A2A 派发器:local 分流到 fake Manager、remote 分流到 fake HTTP server 的单测。
- MCP 工具:list/dispatch/get/cancel 行为契约测试。
- `cli_resolver`:Role=orchestrator 注入 MCP 配置/系统提示/大超时的断言。
- 编排器 ack+异步 push 路径:用 fake replyGateway 断言"先发 ack、后发最终结果、去重不重复编排"。
- 端到端 happy path:微信 fake provider → 大脑(可用 stub 驱动)→ 本地子 agent → 异步 push。

## v1 范围

**包含:**
- 本地子 agent(Bot)启动自动登记 + 远程子 agent 动态自助注册(含心跳/下线)
- 异步:先 ack 后 push
- 大脑内部监控(get_task)+ Task store

**暂不做(留接口):**
- 渠道侧中间进度流式推送(只推最终结果)
- myclaw 自身作为 A2A server 被外部大脑调用
- Task 持久化(先内存)、监控看板 UI
- 远程注册鉴权可先做简单 token,完整鉴权后置

## 已知风险

- `context_token` 长任务过期 → 异步 push 失败(降级处理)。
- 单个大脑 Bot = 单个 claude-acp 会话,跨用户共享上下文(沿用现有"一 bot 一 session"特性,本设计不改变;多用户隔离后置)。
- 进程内并发编排数量受 Manager/session 串行化约束,需在实现中明确 brain session 的并发语义。
