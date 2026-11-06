#!/usr/bin/env python3
"""把 events.jsonl 归一化成可比较的 JSON 行，用于 Go 版与 TS 版 golden 的结构化对拍。

用法：
    python scripts/normalize-events.py <events.jsonl> [--keep-all]

归一化规则（只抹掉"每次运行必然不同"或"等价但序列化形状不同"的部分）：
  - 删除：ts / taskId / callId / durationMs / sha256 / resultSha256 / outputSha256 /
          outputPath / path / args.outDir / args.idempotencyKey / response.timestamp
  - 绝对路径字符串 → "<path>"
  - 空数组与缺失字段视为等价（Go 的 omitempty 会省略空数组）
  - 对象键排序（TS 保插入序、Go 按字典序）

输出：每行一个归一化 JSON（键有序），可直接 diff。
退出码：0 正常；1 输入不可读。
"""
import json
import sys

DROP_KEYS = {
    "ts", "taskId", "callId", "durationMs",
    "sha256", "resultSha256", "outputSha256",
    "outputPath", "path",
}
DROP_PATHS = {
    ("args", "outDir"),
    ("args", "idempotencyKey"),
    ("response", "timestamp"),
}
# 这些字段在 TS 里可能写成 []，Go 的 omitempty 会省略；统一成"不出现"
EMPTY_OK = {"paragraphs", "bullets", "issues", "sections", "attachments", "checks", "deliverables"}


def is_abs_path(v: str) -> bool:
    return ":\\" in v or v.startswith("/") or v.startswith("\\\\")


def normalize(value, parent_key=None, drop_path=()):
    if isinstance(value, dict):
        out = {}
        for k, v in value.items():
            if k in DROP_KEYS:
                continue
            if drop_path + (k,) in DROP_PATHS:
                continue
            nv = normalize(v, k, drop_path + (k,))
            if nv is None and k in EMPTY_OK:
                continue
            out[k] = nv
        return dict(sorted(out.items()))
    if isinstance(value, list):
        return [normalize(v, parent_key, drop_path) for v in value]
    if isinstance(value, str) and is_abs_path(value):
        return "<path>"
    return value


def main() -> int:
    if len(sys.argv) < 2:
        print(__doc__, file=sys.stderr)
        return 2
    path = sys.argv[1]
    try:
        lines = [ln for ln in open(path, encoding="utf-8") if ln.strip()]
    except OSError as err:
        print(f"读不了 {path}: {err}", file=sys.stderr)
        return 1
    for line in lines:
        event = json.loads(line)
        print(json.dumps(normalize(event), ensure_ascii=False, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main())
