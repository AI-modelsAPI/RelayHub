#!/usr/bin/env python3
"""Emit the tail of a log file as GitHub Actions annotations.

A failed run's raw log is only downloadable as an archive, while the checks
API (and the PR checks tab) shows annotations directly. The macOS CI job
uses this so a red run can be understood without the archive.

usage: ci-annotate.py <error|warning|notice> <title> <log> [max-lines]
"""
import sys


def escape(text):
    return text.replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")


def main():
    level, title, path = sys.argv[1], sys.argv[2], sys.argv[3]
    max_lines = int(sys.argv[4]) if len(sys.argv) > 4 else 160
    try:
        with open(path, errors="replace") as f:
            lines = f.read().splitlines()
    except OSError:
        return
    tail = lines[-max_lines:]
    chunks = [tail[i:i + 25] for i in range(0, len(tail), 25)][:8]
    for n, chunk in enumerate(chunks, 1):
        print(f"::{level} title={title} ({n}/{len(chunks)})::{escape(chr(10).join(chunk))}")


if __name__ == "__main__":
    main()
