#!/usr/bin/env python3
"""Create a deterministic ZIP using the source commit time and Unix modes."""
from pathlib import Path
import stat
import sys
import time
import zipfile

source, output = map(Path, sys.argv[1:3])
timestamp = time.gmtime(max(int(sys.argv[3]), 315532800))[:6]
with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
    for path in sorted(source.iterdir()):
        entry = zipfile.ZipInfo(path.name, date_time=timestamp)
        entry.create_system = 3
        mode = 0o755 if path.name in {"checkin", "setup.sh"} else 0o644
        entry.external_attr = (stat.S_IFREG | mode) << 16
        archive.writestr(entry, path.read_bytes(), compress_type=zipfile.ZIP_DEFLATED, compresslevel=9)
