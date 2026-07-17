# Agent req/resp 消息流程与「一发多收」演进方案

> 状态:§1–2 为现状梳理;§3 方案已实施(见各小节「实施备注」)。
> 范围:从 channel 收到用户消息,到 agent driver 执行,再到回复送回 channel 的完整链路;以及支持 `req1 → resp1, resp2, resp3…`(driver 主动/定时推送)所需的改动分析。

## 1. 当前全链路(req/resp 严格成对)

```
飞书 / 微信 / HTTP 平台
  │ 平台消息
  ▼
channel runtime(feishu ws / wechat / httpchan)
  │ 封装成 channel.RuntimeEvent          internal/channel/provider.go:67
  ▼ RuntimeCallbacks.OnEvent 回调
BotConnectionManager                     internal/app/bot/connection_manager.go:96
  │ orchestrator.HandleEvent             internal/bootstrap/bootstrap.go:129
  ▼
BotMessageOrchestrator.HandleMessage     internal/app/bot/message_orchestrator.go:159
  │ admitMessage:按 bot:message_id 去重(TTL 10min)、入 per-bot 队列
  │ ensureWorker:每个 bot 两个常驻 goroutine(runAccept / runDeliver),
  │              由无缓冲 handoff chan 连接,StopBot 关 stopCh 一起退出
  │
  ├─► runAccept:  queue → accept():Resolve spec → ack/进度卡片 → handoff
  │                                              message_orchestrator.go:276
  └─► runDeliver: handoff → executeAndDeliver()  message_orchestrator.go:337
        │ 构造 agent.Request{Prompt, OnProgress}
        ▼
      agent.Manager.Send                 internal/agent/manager.go:20
        │ sessionFor:per-bot Session 缓存,spec 变化/broken 时重建
        ▼
      agent.Session.Send                 internal/agent/session.go:35
        │ 全程持锁,state Busy → runtime.Run(ctx, req) 阻塞直到本轮结束
        ▼
      driver(以 claude-acp 为例)        internal/agent/claude/driver_acp.go
        │ Run:向常驻进程 stdin 写一行 user JSON,等待 turnCh
        │ readLoop(进程级常驻 goroutine):逐行解析 stdout
        │   ├─ system/init          → 捕获 CLI sessionID
        │   ├─ assistant/tool_use   → dispatchProgress → OnProgress(进度卡片 Step)
        │   └─ result               → dispatchTurnEvent → Run 返回 agent.Response
        ▼
      executeAndDeliver 拿到 resp
        │ 持久化 CLI sessionID → sess.Done(进度卡片收尾)→ replyWithTimeout
        ▼
      MultiReplyGateway.Reply(target, resp)   internal/channel/reply_gateway.go:10
        │ 按 ReplyTarget.ChannelType 路由到具体渠道
        ▼
      平台把回复发给用户
```

### 1.1 各层职责

| 层 | 位置 | 职责 |
|---|---|---|
| channel runtime | `internal/channel/{feishu,wechat,httpchan}` | 平台协议 → `RuntimeEvent`;无业务逻辑 |
| BotConnectionManager | `internal/app/bot/connection_manager.go` | bot 生命周期;把 OnEvent 接到 orchestrator,bot 停止时调 `StopBot` |
| BotMessageOrchestrator | `internal/app/bot/message_orchestrator.go` | 去重、排队、每 bot 串行执行、ack、进度会话、超时、回复、CLI session 持久化 |
| agent.Manager / Session | `internal/agent/{manager,session}.go` | per-bot 会话缓存与互斥;spec 变化时换 runtime |
| driver | `internal/agent/{claude,codex,opencode}` | 与 CLI 进程交互。`claude-acp`/`codex-acp`/`opencode-acp` 为常驻进程多轮复用;`codex-exec` 每轮起一个进程(`driver_exec.go:87`) |
| replyGateway / ProgressReporter | `internal/channel/{reply_gateway,progress}.go` | 出站消息与进度卡片,按 ChannelType 多路复用 |

### 1.2 关键数据结构

- `agent.Request`(`internal/agent/types.go:39`):`Prompt` + `OnProgress func(ProgressEvent)`(轮内工具事件回调,可为 nil)。
- `agent.Response`(`types.go:51`):`Text/RuntimeType/ExitCode/Duration/SessionID`,**一轮恰好一个**。
- `channel.ReplyTarget`(`provider.go:54`):回复地址(渠道类型 + 收件人 + 元数据)。**只在本轮的 `deliveryItem` 里存活**,轮结束即丢弃。
- `channel.ProgressSession`(`progress.go:12`):`Ack/Step/Done/Fail`,飞书侧渲染为可刷新的进度卡片。

### 1.3 现在就存在的「一轮多条输出」——但都不是 resp

req/resp 成对之外,用户在一轮里其实已经能收到多条消息,但语义上都是过程性的:

1. **Ack**:orchestrator bot(`Spec.Orchestrator=true`)在 accept 阶段先回「收到,正在处理…」或渲染进度卡片(`message_orchestrator.go:295`)。
2. **进度卡片更新**:driver 每解析到一个 `tool_use` 就触发 `OnProgress → sess.Step`,飞书卡片节流刷新(`driver_acp.go:369`)。

它们的生命周期都被钉死在「一轮」内:`OnProgress` 回调只在 `Run` 执行期间非 nil(`driver_acp.go:178`),轮外 readLoop 收到的任何 stdout 事件都会被直接丢弃(`dispatchTurnEvent`/`dispatchProgress` 在 activeTurnCh/activeProgress 为 nil 时 return,`driver_acp.go:393-413`)。

### 1.4 另一个 req 入口:hook

`internal/hook/manager.go` 提供 HTTP webhook → `executor.Send` 的直通入口(`sendToAgent`),但 resp 沿 HTTP 响应原路返回,不进 channel,与本文的推送需求无关;它只说明 `executor.Send` 这条链路可以在 channel 消息之外被复用。

## 2. 「1 req = 1 resp」被写死在哪些地方

| # | 位置 | 约束 |
|---|---|---|
| 1 | `agent.SessionRuntime.Run(ctx, req) (Response, error)`(`driver.go:13`) | 接口本身是同步单返回值 |
| 2 | `agent.Session.Send`(`session.go:35`) | 全程持锁;一轮产出一个 resp,state Busy→Ready |
| 3 | `agent.Manager.Send`(`manager.go:20`) | 只是透传,同样阻塞单返回 |
| 4 | orchestrator 的 `executor` 接口(`message_orchestrator.go:46`) | 同上 |
| 5 | `executeAndDeliver`(`message_orchestrator.go:337`) | 只有 `Send` 返回后才 `reply` 一次;`ReplyTarget` 随 `deliveryItem` 一起在轮末丢弃 |
| 6 | driver `Run` / `readLoop`(`driver_acp.go:157,330`) | `Run` 等到 `result` 事件才返回;轮外 stdout 事件全部丢弃 |

**好消息**:出站方向没有任何 1:1 假设——`replyGateway.Reply(ctx, target, resp)` 是无状态的,只要手里有 `ReplyTarget`,想发几条就能发几条。缺的只是:(a) 轮外拿得到 `ReplyTarget`;(b) driver → orchestrator 的反向通道;(c) 产生「轮外 resp」的触发源。

## 3. 目标模式:req1 → resp1, resp2, resp3…

用例:用户说「每隔 1 分钟给我统计下 CPU 使用率」,此后 driver 每分钟主动推一条 resp,直到任务取消或 bot 停止。

两种语义,注意区分:

- **语义 A:轮内流式**——一轮不结束,把中间产出(assistant 文本)持续推给用户,最后一条是 result。改动小,但轮不结束意味着 `Session` 一直 Busy、后续消息被排队,且受 `processingTimeout`/`Spec.Timeout` 约束,**不适合长期定时任务**。
- **语义 B:跨轮主动推送**——每轮正常结束,推送与任何在途 req 解耦,由定时器(或 agent 侧事件)触发。这是本用例真正需要的。

### 3.1 设计决策:定时器是每个 runtime 的通用能力

CLI(claude/codex)不会在没有输入的情况下自己产出新 turn,所以「每分钟统计一次」必须有一个我方的定时触发源,每个 tick 在内部合成一次 req——**协议内部依然成对,用户视角是 req1 → resp1..N**。

定时器挂在 `agent.Session` 上(`internal/agent/session.go`),而不是写进某个 driver,也不上移到 orchestrator:

- 每个 driver runtime 启动时都会被 `Session` 包裹(`NewSession` 内部调用 `driver.Init` 拿到 runtime,session.go:16),能力挂在 Session 上即等价于「每个启动的 runtime 天生具备定时推送能力」;
- 四个 driver(claude-acp / codex-acp / codex-exec / opencode-acp)零改动,一份实现全部获得;
- 定时器生命周期与 runtime 严格一致:`Session.Close` 时定时器全部停止,不会出现 CLI 进程已死、定时器还在空转。

> 曾考虑过把定时器放 orchestrator 层(每 tick 合成一条内部消息走完整 accept/deliver 链路)。当时反对 driver 层方案的主要理由是「三个 driver 要各实现一份」;把能力上收到 Session 层后同样只有一份实现,该理由不再成立,且换来了定时器与 runtime 生命周期天然一致的优势,故采用本方案。代价是 tick 不经过 orchestrator 的进度卡片/回复兜底逻辑,需要单独接一个反向出口(见 ④)。

### 3.2 分层改动清单

**① `internal/agent/types.go` —— 新类型**

```go
// ScheduledTask 是注册在某个 bot 会话上的周期性任务。
type ScheduledTask struct {
	ID       string
	Interval time.Duration
	Prompt   string // 每个 tick 注入的合成 prompt
}

// PushResponse 是一次主动推送:某个定时 tick(或未来其他触发源)产出的 resp。
type PushResponse struct {
	Response
	TaskID string
}

// PushSink 由 orchestrator 注册,接收主动推送。实现必须快速返回、不 panic。
type PushSink func(PushResponse)
```

**② `internal/agent/session.go` —— Session 获得调度能力**

```go
type Session struct {
	mu      sync.Mutex
	state   SessionState
	runtime SessionRuntime
	spec    Spec
	sink    PushSink              // mu 保护
	tasks   map[string]*taskTimer // taskID → 定时器 goroutine
}

func (s *Session) SetPushSink(sink PushSink)
func (s *Session) Schedule(task ScheduledTask) (string, error) // 返回 taskID
func (s *Session) CancelTask(taskID string) bool
func (s *Session) Tasks() []ScheduledTask
```

tick 逻辑(每个 task 一个 goroutine + `time.Ticker`):

```go
for {
	select {
	case <-t.stopCh:
		return
	case <-ticker.C:
		if s.State() != SessionStateReady { // Busy/Broken → 跳过本 tick
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), s.spec.Timeout)
		resp, err := s.Send(ctx, Request{BotID: s.spec.BotID, Prompt: task.Prompt})
		cancel()
		if err != nil { /* 记日志;session Broken 时停止本任务 */ continue }
		if sink := s.pushSink(); sink != nil {
			sink(PushResponse{Response: resp, TaskID: task.ID})
		}
	}
}
```

要点:

- tick 与用户消息共用同一把 `s.mu`,天然互斥串行,「CLI 进程一次只处理一轮」的既有约束不被破坏;
- 上一轮(用户消息或上个 tick)还没结束时,本 tick **跳过**而不是排队,避免任务堆积;
- ctx 用 `context.Background()` + `spec.Timeout` 包装,不挂在任何 inbound ctx 上;
- `Session.Close` 第一步就停掉全部 taskTimer,并拒绝后续 Schedule。

**实施备注**:忙时跳过用的是 `s.mu.TryLock()` 而不是先查 `State()`——`Send` 在整轮期间持有 `s.mu`,而 `State()` 也要拿这把锁,轮内调用会阻塞到轮结束,起不到「探测忙」的作用;`TryLock` 失败即视为忙、直接跳过。Broken/closed 的判断在拿到锁之后做(`runTick`,internal/agent/session.go)。调度器自身状态(tasks 表、sink)用独立的 `schedMu`,避免注册/取消被在途轮阻塞。

**③ `internal/agent/manager.go` —— per-bot 透传与生命周期**

```go
func (m *Manager) SetPushSink(botID string, sink PushSink) // 存表 + 注入现有 session
func (m *Manager) Schedule(ctx context.Context, botID string, spec Spec, task ScheduledTask) (string, error)
func (m *Manager) CancelTask(botID, taskID string) bool
func (m *Manager) StopBot(botID string) // 新增:关闭并移除该 bot 的 session
```

- sink 按 botID 存在 Manager 上,`sessionFor` 每次新建/替换 session 时重新注入——spec 变化导致的会话重建不会丢 sink;
- ⚠️ sink/task 不进 `Spec`:`Session.Matches` 用 `reflect.DeepEqual`(session.go:53),func 字段永不相等,会导致每条消息都重建会话;
- 现状:bot 断开时 Manager 的 session 并不会被关闭(只有 spec 变化才替换)。定时器引入后这从「闲置进程」升级为「持续空跑的定时任务」,必须堵上:`orchestrator.StopBot` 联动 `Manager.StopBot` 关闭 session(CLI 进程也顺带释放,本来就该做)。

**④ `internal/app/bot/message_orchestrator.go` —— 反向出口**

- `botState` 增加 `lastReplyTarget channel.ReplyTarget`,在 `accept`(message_orchestrator.go:276)成功解析后更新;
- `ensureWorker` 首次拉起 goroutine 时向 executor 注册 sink:收到 `PushResponse` → 取 `lastReplyTarget` → 走现有 `replyWithTimeout`;target 为空(该 bot 从未收到过消息)则只记日志丢弃;
- `executor` 接口(message_orchestrator.go:46)扩展 `SetPushSink` / `StopBot`;
- `StopBot`(message_orchestrator.go:601)在现有清理之后调用 `executor.StopBot(botID)`。

**⑤ 注册入口 —— MCP 工具**

agent 理解「每隔 1 分钟统计下 CPU」后自己调用 MCP 工具完成注册,不做指令文本解析:

- `schedule_task(interval, prompt) → task_id`、`cancel_scheduled_task(task_id)`、`list_scheduled_tasks()`(与既有 dispatch 任务的 `cancel` 工具区分命名);
- 工具挂在现有 `orchestration.MCPService` 上(`SetScheduler(manager, resolver)` 接线,internal/app/orchestration/schedule_mcp.go),内部转调 `Manager.Schedule` 等;
- tick 的 spec 取当前 bot 的 resolver 结果(与普通消息一致),保证 tick 复用同一个 CLI 会话、共享多轮上下文。

**实施备注 —— 调用方 botID 识别**:cli_resolver 注入的 myclaw MCP URL 改为 per-bot:`<MCPURL>?bot_id=<botID>`(`perBotMCPURL`,cli_resolver.go);MCP handler 外层套 `botIDMiddleware` 把 query 里的 bot_id 放进请求 context,工具实现用 `BotIDFromContext(ctx)` 取回——go-sdk 的 streamable HTTP 会把每个请求的 context 透传给工具 handler(已有端到端测试覆盖:`TestMCPHandlerPropagatesBotIDFromQuery`)。interval 解析为 Go duration,最小 1s。

**实施备注 —— schedule_task 必须由运行中的会话调用(重要,踩过坑)**:`Manager.Schedule` **不能**走 `sessionFor`。`sessionFor` 在持有 `m.mu` 期间调用 `session.Matches()`/`State()`,而这两个方法都要拿会话的轮次锁 `s.mu`;一个 turn 会全程持有 `s.mu`。agent 在**自己 turn 内**调用 `schedule_task` 时,turn 已持 `s.mu`,于是工具调用阻塞在 `s.mu` 上直到 turn 结束(还顺带占着 `m.mu`,把 list/cancel 一起拖死)——线上表现为 MCP 工具调用反复超时、任务直到那个长 turn 结束才注册上。修法:`Manager.Schedule(botID, task)` 直接按 botID 查现有会话(和 `CancelTask`/`Tasks` 一样只用 `m.mu`),**不创建、不校验 spec、不碰 `s.mu`**;bot 无活跃会话时直接返回错误。因此 schedule 语义是「挂到正在运行的 agent 上」,spec 参数不再需要(tick 复用现有 runtime 天然共享 CLI 会话)。回归测试:`TestManagerScheduleFromWithinTurnDoesNotDeadlock`。

**⑥ 可选:持久化**

v1 任务生命周期 = session 生命周期(进程重启、session 替换即丢失)。如需跨重启存活,加一张 `bot_scheduled_tasks` 表,orchestrator 在 bot 上线(`ensureWorker`)时重放注册;`lastReplyTarget` 也需一并持久化,否则重启后无处可推。

### 3.3 其余注意点

- **推送限流**:推送直接打到 IM 渠道,飞书/微信有频控;沿用 `replyWithTimeout` 的 5s 超时,必要时在 sink 侧加最小推送间隔;
- **去重**:tick 合成的 req 不经过 `admitMessage`(直接走 `Session.Send`),不占 worker queue,也天然绕开 §4 的空 MessageID 问题;tick 不重入由「Busy 即跳过」保证;
- **单轮失败**:tick 的 Send 失败只记日志、不终止任务;session Broken 时停止该 session 的全部任务(session 重建后由持久化层或用户重新注册);
- **进度卡片**:tick 不走 orchestrator 的 accept 流程,没有 ProgressSession——定时任务只推最终 resp,不渲染过程卡片(如需卡片,后续把 `beginProgress` 暴露给推送路径即可)。

## 4. 顺带发现并已修复的缺陷

1. **`admitMessage` 空 MessageID 循环入队**(已修):旧代码对 `MessageID == ""` 的消息会持续向 worker.queue 塞同一条消息直到队列满,`queueSize > 1` 时同一条消息被处理多次。改为只入队一次(满则拒),与有 id 的路径一致。回归:`TestAdmitMessageEmptyIDEnqueuesOnce`。

2. **`sessionFor` 持 `m.mu` 期间调 `Matches/State`**(已修):这两个方法要拿会话的轮次锁 `s.mu`,而一个 turn 会全程持 `s.mu`。若某 bot 正在跑 turn 时,又有第二个 `Send`(hook / a2a dispatch 与 orchestrator 并发)进入 `sessionFor`,它会持着 `m.mu` 阻塞在 `s.mu` 上——**冻结整个 Manager(所有 bot)** 直到该 turn 结束。改为先在 `m.mu` 下取会话指针,再在释放 `m.mu` 后评估 `Matches/State`。回归:`TestManagerSessionForDoesNotFreezeManagerDuringBusyTurn`。这也是 §3.2 调度死锁(`Manager.Schedule` 绕开 `sessionFor`)的同源问题,现已从 `sessionFor` 本体根除。

3. **codex 跳过 http MCP server**(已修):`codexMCPArgs` 旧代码对所有 http 类型 MCP server 一律跳过,导致 codex bot(如 wx)拿不到 myclaw 的任何工具(schedule_task / list_agents / dispatch 等)。codex ≥ 0.140(实测 0.144.5)支持 streamable HTTP MCP(`mcp_servers.<name>.url`),已改为注入 http server(带 per-bot `?bot_id=`)。回归:`TestResolveCodexInjectsHTTPServersAsURL`。注意:codex 0.144.5 有 `ToolSearchAlwaysDeferMcpTools`,MCP 工具对模型是「延迟/需搜索」暴露的(与 claude 直接可见不同),真实 `codex app-server` 路径下 wx 已能用 a2a 工具,故预期可用;`codex exec` 无头模式对延迟工具的暴露不同,不能作为判据。
