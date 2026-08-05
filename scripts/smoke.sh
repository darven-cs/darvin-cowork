#!/usr/bin/env bash
# Headless smoke test：
#   1. 编译 darvin-agent 二进制（如果还没 build）
#   2. spawn → 等 stdout 端口行
#   3. 调 ws-smoke-client.js 跑协议断言
#   4. SIGTERM 子进程；< 5s 退出即合格
#
# 用法：
#   npm run smoke
#   或直接：bash scripts/smoke.sh
#
# 退出码：0 = pass, 非零 = fail。

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

# 1. 确保二进制就绪
BIN_PATH="bin/$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m)/darvin-agent"
# 实际命名：darvin-agent-linux-x64 等；列出 bin/ 找第一个匹配的。
if [ ! -x "bin/darvin-agent-$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m)" ]; then
  echo "[smoke] darvin-agent binary missing; running build:agent..."
  npm run build:agent --silent
fi

# 2. spawn 子进程
CONFIG_PATH="$REPO_ROOT/src/darvin-agent/config.yaml"
export DARVIN_CONFIG="$CONFIG_PATH"

# 1b. 用户级 config（如果不存在）：smoke 只需要 binary 起来即可，
#     placeholder api_key 让 llm.NewProvider 走通。真要测流式响应
#     走 playwright-cli（dev 手动，详见 AGENTS.md），那里把
#     ANTHROPIC_API_KEY 通过设置面板灌入。
USER_CONFIG_DIR=""
case "$(uname -s)" in
  Darwin) USER_CONFIG_DIR="$HOME/Library/Application Support/darvin-cowork" ;;
  Linux)  USER_CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/darvin-cowork" ;;
  *)      USER_CONFIG_DIR="$HOME/.config/darvin-cowork" ;;
esac
mkdir -p "$USER_CONFIG_DIR"
if [ ! -f "$USER_CONFIG_DIR/config.yaml" ]; then
  cat >"$USER_CONFIG_DIR/config.yaml" <<'YAML'
llm:
  provider: anthropic
  api_key: "smoke-placeholder-not-used-by-tests"
  base_url: ""
YAML
  echo "[smoke] wrote user-level placeholder to $USER_CONFIG_DIR/config.yaml"
fi

BIN="$(ls bin/darvin-agent-* 2>/dev/null | head -n 1)"
if [ -z "$BIN" ]; then
  echo "[smoke] no darvin-agent binary found in bin/" >&2
  exit 1
fi

LOG="$REPO_ROOT/.smoke.log"
PORT_LINE_TIMEOUT=10
echo "[smoke] spawning $BIN (logs -> $LOG)"
"$BIN" >"$LOG" 2>&1 &
SUBPROC_PID=$!

cleanup() {
  if kill -0 "$SUBPROC_PID" 2>/dev/null; then
    kill -TERM "$SUBPROC_PID" 2>/dev/null || true
    # 5s 兜底
    for _ in 1 2 3 4 5; do
      if ! kill -0 "$SUBPROC_PID" 2>/dev/null; then break; fi
      sleep 1
    done
    if kill -0 "$SUBPROC_PID" 2>/dev/null; then
      kill -KILL "$SUBPROC_PID" 2>/dev/null || true
    fi
  fi
}
trap cleanup EXIT

# 3. 等端口行
PORT=""
for _ in $(seq 1 "$PORT_LINE_TIMEOUT"); do
  if [ -s "$LOG" ]; then
    PORT_LINE="$(grep -m1 -E '<port>[0-9]+</port>' "$LOG" || true)"
    if [ -n "$PORT_LINE" ]; then
      PORT="$(echo "$PORT_LINE" | sed -E 's/.*<port>([0-9]+)<\/port>.*/\1/')"
      break
    fi
  fi
  sleep 1
done

if [ -z "$PORT" ]; then
  echo "[smoke] FAIL: did not see <port> in $PORT_LINE_TIMEOUT s" >&2
  echo "--- last 20 lines of $LOG ---" >&2
  tail -n 20 "$LOG" >&2 || true
  exit 1
fi
echo "[smoke] port = $PORT"

# 4. 跑 ws-smoke-client.js
if ! node "$REPO_ROOT/scripts/ws-smoke-client.js" "$PORT"; then
  echo "[smoke] FAIL: ws-smoke-client.js reported failure" >&2
  exit 1
fi

echo "[smoke] all checks passed"
exit 0