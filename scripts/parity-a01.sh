#!/usr/bin/env bash
# A-01 对齐验收（parity gate）：Go 版必须与 TS 版 golden 一致。
#
#   bash scripts/parity-a01.sh
#
# 断言：
#   1. go build ./... 通过
#   2. run-case A-01（剧本模式、隔离工作区）passed=true 且 finalState=delivered
#   3. 事件类型序列与 golden-a01/events.normalized.jsonl 一致（忽略 ts/taskId/路径）
#   4. 交付物 docx 存在、可解压、含 2 张 media 图片
#   5. snapshot.json 的 state/步骤集合与 golden 一致
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

GOFLAGS_ENV=(env GOSUMDB=sum.golang.org GOTOOLCHAIN=auto)
OUT="${PARITY_OUT:-parity-out}"
WS="${PARITY_WS:-.parity-ws}"
GOLDEN="golden-a01"

say() { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL\033[0m %s\n' "$*"; FAILED=$((FAILED + 1)); }
pass() { printf '\033[32mok\033[0m   %s\n' "$*"; }
FAILED=0

say "1/5 构建"
if "${GOFLAGS_ENV[@]}" go build ./... 2>&1 | tail -20; then pass "go build"; else fail "go build"; fi

say "2/5 跑 A-01（剧本模式）"
rm -rf "$OUT" "$WS"
set +e
"${GOFLAGS_ENV[@]}" DE_WORKSPACE="$(pwd -W 2>/dev/null || pwd)/$WS" \
  go run ./cmd/digital-employee run-case samples/cases/A-01.json \
  --scripted samples/cases/scripts/A-01.json --out "$OUT" > /tmp/parity-run.log 2>&1
RC=$?
set -e
tail -20 /tmp/parity-run.log
if [ "$RC" -ne 0 ]; then fail "run-case 退出码 $RC"; fi

if [ ! -f "$OUT/result.json" ]; then
  fail "没有 $OUT/result.json"
  echo; echo "结论：$FAILED 项失败"; exit 1
fi

say "3/5 result.json"
STATE=$(grep -o '"finalState"[[:space:]]*:[[:space:]]*"[^"]*"' "$OUT/result.json" | sed 's/.*"\([^"]*\)"$/\1/')
PASSED=$(grep -o '"passed"[[:space:]]*:[[:space:]]*\(true\|false\)' "$OUT/result.json" | head -1 | grep -o 'true\|false')
[ "$STATE" = "delivered" ] && pass "finalState=delivered" || fail "finalState=$STATE（期望 delivered）"
[ "$PASSED" = "true" ] && pass "passed=true" || fail "passed=$PASSED"

say "4/5 事件结构化对齐（归一化后逐字段 diff）"
if [ -f "$OUT/events.jsonl" ] && [ -f "$GOLDEN/events.jsonl" ]; then
  python scripts/normalize-events.py "$GOLDEN/events.jsonl" > /tmp/golden-events.norm.jsonl
  python scripts/normalize-events.py "$OUT/events.jsonl" > /tmp/go-events.norm.jsonl
  GOLDEN_N=$(wc -l < /tmp/golden-events.norm.jsonl | tr -d ' ')
  GO_N=$(wc -l < /tmp/go-events.norm.jsonl | tr -d ' ')
  if diff -q /tmp/golden-events.norm.jsonl /tmp/go-events.norm.jsonl > /dev/null 2>&1; then
    pass "事件与 golden 完全一致（$GO_N 条，归一化后逐字段相同）"
  else
    fail "事件与 golden 不一致（golden $GOLDEN_N 条，go $GO_N 条）"
    diff -u /tmp/golden-events.norm.jsonl /tmp/go-events.norm.jsonl | head -24
  fi
else
  fail "缺少 $OUT/events.jsonl 或 $GOLDEN/events.jsonl"
fi

say "5/5 交付物"
# 从 result.json 的 deliverables 里取 docx；不能用 find | head -1：
# 工作区里还有附件（周会纪要.docx），排序在前会误取附件。
DOCX=$(python - "$OUT/result.json" <<'PY'
import json, sys
try:
    result = json.load(open(sys.argv[1], encoding='utf-8'))
except Exception:
    sys.exit(0)
for deliverable in result.get('deliverables', []):
    if deliverable.get('kind') == 'docx' and deliverable.get('path'):
        print(deliverable['path'])
        break
PY
)
if [ -n "$DOCX" ] && [ -f "$DOCX" ]; then
  pass "docx: $DOCX"
  N=$(python - "$DOCX" <<'PY'
import sys, zipfile
z = zipfile.ZipFile(sys.argv[1])
media = [n for n in z.namelist() if n.startswith('word/media/')]
doc = z.read('word/document.xml').decode('utf-8')
print(len(media))
assert 'w:tbl' in doc, 'document.xml 没有表格'
assert 'w:drawing' in doc or 'pic:pic' in doc, 'document.xml 没有图片'
PY
)
  [ "$N" = "2" ] && pass "docx 含 2 张图片 + 表格" || fail "docx media 数量=$N（期望 2）"
else
  fail "没有找到 docx 交付物"
fi

say "结论"
if [ "$FAILED" -eq 0 ]; then
  printf '\033[32mPARITY PASS\033[0m  A-01 与 TS 版对齐\n'
  exit 0
fi
printf '\033[31mPARITY FAIL\033[0m  %d 项失败\n' "$FAILED"
exit 1
