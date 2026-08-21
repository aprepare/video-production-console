from __future__ import annotations

import re
from pathlib import Path

IMPORT_MARK = "import asyncio"
HELPER_MARK = "async def _periodic_sse_stream"
IMAGE_MARK = 'if self.endpoint == "/v1/images/generations":'

HELPER = '''async def _periodic_sse_stream(source, interval=10.0):
    iterator = iter(source)
    pending = None
    try:
        while True:
            if pending is None:
                pending = asyncio.create_task(run_in_threadpool(_next_item, iterator))
            try:
                has_item, item = await asyncio.wait_for(asyncio.shield(pending), timeout=interval)
            except asyncio.TimeoutError:
                yield ": keepalive\\n\\n"
                continue
            pending = None
            if not has_item:
                return
            yield item
    finally:
        if pending is not None and not pending.done():
            pending.cancel()

'''

def patch_source(source: str) -> str:
    if IMPORT_MARK in source or HELPER_MARK in source or IMAGE_MARK in source:
        raise ValueError("log_service.py already contains keepalive patch markers")
    if "def _next_item" not in source or "class LoggedCall" not in source:
        raise ValueError("required anchors not found")
    lines = source.splitlines(keepends=True)
    future = next((i for i, l in enumerate(lines) if l.startswith("from __future__ import")), None)
    lines.insert((future + 1) if future is not None else 0, "import asyncio\n")
    source = "".join(lines)
    m = re.search(r"(?ms)^def _next_item\b.*?(?=^\S)", source)
    if not m:
        raise ValueError("could not locate _next_item body")
    source = source[:m.end()] + "\n\n" + HELPER + source[m.end():]
    anchor = re.search(r"(?m)^(\s*)sender\s*=.*$", source)
    if not anchor:
        raise ValueError("sender assignment not found")
    indent = anchor.group(1)
    block = f'{indent}if self.endpoint == "/v1/images/generations":\n{indent}    return StreamingResponse(_periodic_sse_stream(sender(self.stream(result))), media_type="text/event-stream")\n'
    return source[:anchor.end()] + "\n" + block + source[anchor.end():]

def main() -> None:
    path = Path("/app/services/log_service.py")
    patched = patch_source(path.read_text())
    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_text(patched)
    tmp.replace(path)

if __name__ == "__main__":
    main()
