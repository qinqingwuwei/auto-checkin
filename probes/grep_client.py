#!/usr/bin/env python3
"""在客户端代码中定位指定字符串并打印上下文（只读，不打印任何凭据）。

用法：python3 grep_client.py "<needle1,needle2>" <file> [file...]
"""
import pathlib
import sys


def scan(path, needles, before=300, after=420, max_hits=2):
    text = pathlib.Path(path).read_text(errors="replace")
    for needle in needles:
        print("=" * 14, needle, "=" * 14)
        idx, hits = 0, 0
        while hits < max_hits:
            idx = text.find(needle, idx)
            if idx < 0:
                break
            print(text[max(0, idx - before): idx + after].replace("\n", "\\n"))
            print("-" * 60)
            idx += 1
            hits += 1
        if hits == 0:
            print("(no match)")


if __name__ == "__main__":
    needles = tuple(n for n in sys.argv[1].split(",") if n)
    for p in sys.argv[2:]:
        print("#" * 18, p)
        scan(p, needles)