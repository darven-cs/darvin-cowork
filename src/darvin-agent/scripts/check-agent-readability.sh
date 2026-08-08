#!/usr/bin/env bash
# 用法:从 src/darvin-agent 运行 `bash scripts/check-agent-readability.sh`
# 任何一项检查失败即非零退出。
set -euo pipefail

fail=0

# 1. 单文件软上限 800 行(非测试)
over=$(find internal -name "*.go" ! -name "*_test.go" -exec wc -l {} \; | awk '$1 > 800 { print }')
[ -z "$over" ] || { echo "Files over 800 lines:"; echo "$over"; fail=1; }

# 2. 注释/代码比 ≤ 0.30(微型文件豁免;超密度打印供 review,不阻塞)
for f in $(find internal -name "*.go" ! -name "*_test.go"); do
  total=$(wc -l < "$f")
  [ "$total" -lt 60 ] && continue
  comments=$(grep -c "^[[:space:]]*//" "$f" || true)
  blank=$(grep -c "^$" "$f" || true)
  code=$((total - comments - blank))
  if (( code <= 0 )); then continue; fi
  ratio=$(awk "BEGIN{printf \"%.3f\", $comments/$code}")
  if (( $(awk "BEGIN{print ($ratio > 0.30)}") )); then
    echo "$f ratio=$ratio (code=$code comments=$comments)"
  fi
done

# 3. 违规注释模式
for pat in 'Phase [0-9]' 'FR-[0-9]' 'Reasonix'; do
  hits=$(grep -rnE "$pat" --include="*.go" . | wc -l || true)
  [ "$hits" -eq 0 ] || { echo "Pattern '$pat' still appears $hits times"; fail=1; }
done

# 4. 格式硬门
gofmt -l . | grep -q . && { echo "gofmt diff detected"; fail=1; } || true
goimports -l . | grep -q . && { echo "goimports diff detected"; fail=1; } || true
go vet ./... || fail=1

# 5. F3 文件级 package comment(缺失数应为 0)
for f in $(find internal cmd -name "*.go"); do
  first=$(grep -m1 -n "^package " "$f" | cut -d: -f1 || true)
  [ -z "$first" ] && continue
  if ! awk -v line="$first" 'NR<line && /^\/\//' "$f" | grep -q .; then
    echo "F3 missing file-level comment: $f"
    fail=1
  fi
done

# 6. ST10xx 注释 / 命名硬约束(全包 0 告警)
if ! staticcheck -checks 'ST10*' ./... > /tmp/st10-out.txt 2>&1; then
  echo "staticcheck ST10* violations:"
  cat /tmp/st10-out.txt
  fail=1
fi

# 7. 聚合 lint(baseline 比对:不允许新增告警)
if [ -f .golangci-baseline.txt ]; then
  golangci-lint run ./... > /tmp/current-lint.txt || true
  new=$(comm -23 <(sort /tmp/current-lint.txt) <(sort .golangci-baseline.txt) | wc -l)
  [ "$new" -eq 0 ] || { echo "New golangci-lint violations: $new"; fail=1; }
fi

exit $fail
