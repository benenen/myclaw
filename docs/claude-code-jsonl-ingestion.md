# Claude Code 会话 JSONL 实时摄取笔记

记录从 Claude Code 本地 transcript 文件实时拿到每条消息（user / assistant / tool_use / tool_result）的最小可行路径。整体逻辑是：定位文件 → 拿到 session id → tail → 解析 JSONL。

## 1. JSONL 文件在哪

Claude Code 把每个会话写到本地 JSONL：

```
~/.claude/projects/<cwd-encoded>/<session-uuid>.jsonl
```

- `cwd-encoded`：当前工作目录的全路径，把 `/` 全部替换成 `-`。
  - 例：cwd 是 `/root` → 目录是 `-root`。
  - 例：cwd 是 `/root/workspace/master/myclaw` → 目录是 `-root-workspace-master-myclaw`。
- `<session-uuid>.jsonl`：每个会话一个 UUID 命名的文件。

## 2. 拿到 session id 的两种姿势

### 稳：自己定 session id

启动 Claude Code 时显式传入：

```bash
claude --session-id <你生成的 uuid>
```

文件名就由你掌控，跟踪谁是谁不会错。适合编排器主动起 claude 的场景。

### 偷懒：让 claude 自己生成，再去捞最新的

```bash
# 拿当前 cwd 编码后的目录
dir="$HOME/.claude/projects/$(pwd | sed 's|/|-|g')"

# 最新一个 jsonl
ls -t "$dir" | head -1
```

或者用 inotify 等着新文件出现：

```bash
inotifywait -m -e create "$dir"
```

适合“已经有 claude 跑着了，我事后挂上去看”的场景。缺点是并发起多个 claude 时无法精确归位，要靠时间戳 + 启动顺序去猜。

## 3. tail 出每一条 block

JSONL 是按行追加的（大多数情况下，见下文 gotcha），所以最朴素的实时消费：

```bash
tail -F "$dir/<session-uuid>.jsonl" | jq .
```

`-F` 而不是 `-f`：文件被重命名 / rotate 也能继续跟。

每行是一条完整 JSON，关键字段：

| 字段 | 说明 |
|---|---|
| `type` | `user` / `assistant` / `system` 等 |
| `message.content` | 数组，元素可能是 `text` / `tool_use` / `tool_result` |
| `uuid` | 这一条 block 的 id |
| `parentUuid` | 上一条的 uuid，串成 DAG / 线 |
| `timestamp` | ISO8601 时间戳 |

`message.content` 数组里每个元素的 `type` 区分：

- `text`：纯文本。
- `tool_use`：模型发起的工具调用，含 `name` / `input`。
- `tool_result`：工具返回，含 `tool_use_id` 和 `content`。

按 `parentUuid` 链起来能复原完整的对话树（含分叉 / 重生）。

## 4. ⚠️ Gotcha：不是严格 append-only

Claude Code 在 session 内**会重写早期行**，最典型是 `/compact` 之后会把被压缩的历史替换成新版本。所以：

- **实时消费**通常没事——你只 care 新追加的行，旧行被改你也已经处理过了。
- **完整重建/对账**必须最后再整文件读一遍，不能只信 `tail` 累积出来的视图。

实现上常见做法：

1. 在线流式拿到 line → 解析 → 入下游（消息总线 / DB / UI）。
2. 在 session 结束 / compact 触发 / 周期性 checkpoint 时，整文件重读一次，按 `uuid` 做去重 / 更新，覆盖在线视图里被改写的那些行。

## 5. 最小端到端伪代码

```bash
#!/usr/bin/env bash
set -euo pipefail

session_id="$(uuidgen)"
cwd_enc="$(pwd | sed 's|/|-|g')"
file="$HOME/.claude/projects/$cwd_enc/$session_id.jsonl"

# 起 claude（实际启动你的命令）
claude --session-id "$session_id" &

# 等文件出现（claude 第一次写入时才创建）
until [ -f "$file" ]; do sleep 0.1; done

# 流式消费
tail -F "$file" | while IFS= read -r line; do
  echo "$line" | jq -c '{type, uuid, parentUuid, ts: .timestamp, content: .message.content}'
done
```

## 6. 集成到本仓的思路（可选）

如果想在 myclaw 里做一个 claude transcript 旁路：

- 选「自定 session id」路线：driver 起 claude 时就把 `--session-id` 注进去，文件路径完全已知。
- tail 协程：开一个 goroutine 跑 `tail -F` 或者 `os.Open + Seek(0, io.SeekEnd) + loop`，解析每行为 `map[string]any` 或者强类型 struct。
- compact 兜底：在 session Close 时再整文件 reparse 一次，按 `uuid` 去重覆盖。
- 注意 cwd 编码：driver 里如果切换了 `WorkDir`，编码的也是 claude 进程看到的 cwd，要跟启动命令的 `chdir` 对齐。
