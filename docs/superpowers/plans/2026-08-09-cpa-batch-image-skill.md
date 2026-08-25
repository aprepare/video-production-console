# CPA Batch Image Skill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Install a user-level `cpa-batch-image` Skill that turns prompt lists or manuscripts into real `gpt-image-2` CPA requests, runs fixed waves of at most 10 requests, waits 3 seconds between completed waves, and saves auditable image artifacts.

**Architecture:** Keep the agent-facing workflow in a concise `SKILL.md` and put deterministic behavior in one standard-library Python runner. The runner validates UTF-8 input before any network call, holds a cross-process lock for the whole batch, executes independent `n=1` requests in fixed waves, checkpoints every result, and supports safe resume. Tests use a local fake CPA server and never consume image quota; one final live smoke test is allowed after all local checks pass.

**Tech Stack:** Codex Skills, Python 3 standard library (`argparse`, `urllib`, `concurrent.futures`, `unittest`, `http.server`, `msvcrt`/`fcntl`), OpenAI-compatible `/v1/images/generations` JSON API.

---

## File map

- Create: `C:\Users\prepare\.codex\skills\cpa-batch-image\SKILL.md` — trigger conditions and agent workflow.
- Create: `C:\Users\prepare\.codex\skills\cpa-batch-image\agents\openai.yaml` — UI metadata.
- Create: `C:\Users\prepare\.codex\skills\cpa-batch-image\references\input-schema.md` — prompt input and manifest contracts.
- Create: `C:\Users\prepare\.codex\skills\cpa-batch-image\scripts\batch_generate.py` — validation, HTTP execution, waves, locking, checkpointing, resume, and CLI.
- Create: `C:\Users\prepare\.codex\skills\cpa-batch-image\tests\fake_cpa.py` — deterministic local fake CPA server.
- Create: `C:\Users\prepare\.codex\skills\cpa-batch-image\tests\test_batch_generate.py` — standard-library regression suite.

`C:\Users\prepare\.codex\skills` is not a Git repository. Do not initialize an unrelated repository there. After each implementation task, run the focused tests; before final delivery, create a SHA-256 inventory of the installed Skill. The design and this plan are tracked in the workspace repository.

### Task 1: Capture baseline behavior without the Skill

**Files:**
- No persistent files.

- [ ] **Step 1: Run a no-Skill pressure scenario**

Dispatch a read-only validation agent without giving it the new Skill or intended implementation. Use this exact scenario:

```text
You need to batch-generate 23 images through an OpenAI-compatible gpt-image-2 endpoint. Explain the exact request, concurrency, timeout, retry, encoding, output, and waiting behavior you would use. Do not call any live API and do not modify files.
```

- [ ] **Step 2: Record baseline violations in the active task notes**

Check explicitly whether the baseline omits or violates any of these invariants:

```text
n=1 per request
fixed waves of 10
wait for the whole wave before the next wave
3-second delay only between non-final waves
180-second per-request timeout
no retry for context canceled or client timeout
UTF-8 mojibake rejection before network
cross-process global batch lock
per-image checkpoint and resume
```

Expected: at least one invariant is missing or underspecified. Use the observed gaps to tighten `SKILL.md`; do not add unrelated rules.

### Task 2: Initialize the Skill and define the input contract test-first

**Files:**
- Create: `C:\Users\prepare\.codex\skills\cpa-batch-image\SKILL.md`
- Create: `C:\Users\prepare\.codex\skills\cpa-batch-image\agents\openai.yaml`
- Create: `C:\Users\prepare\.codex\skills\cpa-batch-image\references\input-schema.md`
- Create: `C:\Users\prepare\.codex\skills\cpa-batch-image\tests\test_batch_generate.py`
- Create later in this task: `C:\Users\prepare\.codex\skills\cpa-batch-image\scripts\batch_generate.py`

- [ ] **Step 1: Initialize the generated Skill skeleton**

Run:

```powershell
python C:\Users\prepare\.codex\skills\.system\skill-creator\scripts\init_skill.py `
  cpa-batch-image `
  --path C:\Users\prepare\.codex\skills `
  --resources scripts,references `
  --interface 'display_name=CPA 批量生图' `
  --interface 'short_description=通过 CPA 分批生成并保存多张 GPT Image 2 图片' `
  --interface 'default_prompt=Use $cpa-batch-image to generate images from these prompts or this manuscript.'
```

Expected: `C:\Users\prepare\.codex\skills\cpa-batch-image` exists with `SKILL.md`, `agents/openai.yaml`, `scripts/`, and `references/`.

- [ ] **Step 2: Write failing input-contract tests**

Create `tests\test_batch_generate.py` with imports through the script path and these tests:

```python
import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path

SKILL_DIR = Path(__file__).resolve().parents[1]
SCRIPT = SKILL_DIR / "scripts" / "batch_generate.py"


def load_module():
    spec = importlib.util.spec_from_file_location("cpa_batch_generate", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    # dataclasses resolves postponed annotations through sys.modules.
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class InputContractTests(unittest.TestCase):
    def test_loads_utf8_prompt_without_mutation(self):
        mod = load_module()
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "prompts.json"
            payload = {"tasks": [{"id": "001", "prompt": "富过三代，需要守住资产。"}]}
            path.write_text(json.dumps(payload, ensure_ascii=False), encoding="utf-8")
            tasks = mod.load_tasks(path)
        self.assertEqual(tasks[0].prompt, "富过三代，需要守住资产。")

    def test_rejects_empty_prompt_before_network(self):
        mod = load_module()
        with self.assertRaisesRegex(mod.PreflightError, "empty prompt"):
            mod.validate_tasks([mod.PromptTask(id="001", prompt="  ")])

    def test_rejects_known_mojibake_before_network(self):
        mod = load_module()
        broken = "鈥滃瘜杩囦笁浠ｂ€濆拰50涓囪祫浜"
        with self.assertRaisesRegex(mod.PreflightError, "mojibake"):
            mod.validate_tasks([mod.PromptTask(id="001", prompt=broken)])

    def test_encodes_json_as_utf8_and_forces_n_one(self):
        mod = load_module()
        body = mod.encode_request_body(
            mod.PromptTask(id="001", prompt="银行账户"),
            mod.RequestConfig(size="1024x1024", quality="low", output_format="png"),
        )
        decoded = json.loads(body.decode("utf-8"))
        self.assertEqual(decoded["prompt"], "银行账户")
        self.assertEqual(decoded["n"], 1)
        self.assertEqual(decoded["model"], "gpt-image-2")
```

- [ ] **Step 3: Run the tests and verify RED**

Run:

```powershell
python -m unittest C:\Users\prepare\.codex\skills\cpa-batch-image\tests\test_batch_generate.py -v
```

Expected: FAIL because `scripts\batch_generate.py` or the requested symbols do not exist. A syntax or import-path failure must be fixed until the failure is specifically about missing production behavior.

- [ ] **Step 4: Add the minimal input model and validation implementation**

Create `scripts\batch_generate.py` with these public contracts:

```python
from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path

MODEL = "gpt-image-2"
DEFAULT_REQUEST_TIMEOUT = 180.0
MAX_CONCURRENCY = 10
DEFAULT_BATCH_DELAY = 3.0
MOJIBAKE_MARKERS = ("鈥", "锟斤拷", "�", "â€™", "â€œ", "â€")


class PreflightError(ValueError):
    pass


@dataclass(frozen=True)
class PromptTask:
    id: str
    prompt: str


@dataclass(frozen=True)
class RequestConfig:
    size: str = "1024x1024"
    quality: str = "low"
    output_format: str = "png"
    timeout: float = DEFAULT_REQUEST_TIMEOUT


def load_tasks(path: Path) -> list[PromptTask]:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise PreflightError(f"invalid UTF-8 prompts JSON: {exc}") from exc
    if not isinstance(data, dict):
        raise PreflightError("input must be a JSON object")
    raw_tasks = data.get("tasks")
    if not isinstance(raw_tasks, list) or not raw_tasks:
        raise PreflightError("tasks must be a non-empty list")
    tasks = []
    for index, item in enumerate(raw_tasks, start=1):
        if not isinstance(item, dict) or "id" not in item or "prompt" not in item:
            raise PreflightError(f"task {index}: id and prompt are required")
        if not isinstance(item["id"], str) or not item["id"].strip():
            raise PreflightError(f"task {index}: id must be a non-empty string")
        if not isinstance(item["prompt"], str):
            raise PreflightError(f"task {item['id']}: prompt must be a string")
        tasks.append(PromptTask(id=item["id"], prompt=item["prompt"]))
    validate_tasks(tasks)
    return tasks


def validate_tasks(tasks: list[PromptTask], allow_suspicious_text: bool = False) -> None:
    seen: set[str] = set()
    for task in tasks:
        if not task.prompt.strip():
            raise PreflightError(f"task {task.id}: empty prompt")
        if task.id in seen:
            raise PreflightError(f"duplicate task id: {task.id}")
        seen.add(task.id)
        if not allow_suspicious_text and any(marker in task.prompt for marker in MOJIBAKE_MARKERS):
            raise PreflightError(f"task {task.id}: suspected mojibake")


def encode_request_body(task: PromptTask, config: RequestConfig) -> bytes:
    payload = {
        "model": MODEL,
        "prompt": task.prompt,
        "n": 1,
        "size": config.size,
        "quality": config.quality,
        "output_format": config.output_format,
    }
    return json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
```

- [ ] **Step 5: Run the focused tests and verify GREEN**

Run the command from Step 3.

Expected: four input-contract tests PASS with no warnings.

### Task 3: Implement the HTTP request boundary and retry classification

**Files:**
- Create: `C:\Users\prepare\.codex\skills\cpa-batch-image\tests\fake_cpa.py`
- Modify: `C:\Users\prepare\.codex\skills\cpa-batch-image\tests\test_batch_generate.py`
- Modify: `C:\Users\prepare\.codex\skills\cpa-batch-image\scripts\batch_generate.py`

- [ ] **Step 1: Create a deterministic fake CPA server**

Create `tests\fake_cpa.py` with a `ThreadingHTTPServer` context manager. It must:

```python
from __future__ import annotations

import json
import queue
import threading
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class ScriptedHandler(BaseHTTPRequestHandler):
    responses: queue.Queue[tuple[int, dict[str, str], dict]]
    received: list[tuple[dict[str, str], dict]]

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        body = json.loads(self.rfile.read(length).decode("utf-8"))
        type(self).received.append((dict(self.headers), body))
        status, headers, payload = type(self).responses.get_nowait()
        encoded = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        for key, value in headers.items():
            self.send_header(key, value)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def log_message(self, format, *args):
        return


@contextmanager
def fake_cpa(script):
    ScriptedHandler.responses = queue.Queue()
    ScriptedHandler.received = []
    for response in script:
        ScriptedHandler.responses.put(response)
    server = ThreadingHTTPServer(("127.0.0.1", 0), ScriptedHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_port}/v1", ScriptedHandler
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)
```

- [ ] **Step 2: Add failing HTTP and retry tests**

Append tests that assert:

```python
PNG_1X1_B64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl2nkwAAAAASUVORK5CYII="

def test_success_sends_utf8_header_and_decodes_b64(self):
    from fake_cpa import fake_cpa
    mod = load_module()
    with tempfile.TemporaryDirectory() as tmp, fake_cpa([
        (200, {"X-CPA-TRACE-ID": "trace-1"}, {"data": [{"b64_json": PNG_1X1_B64}]})
    ]) as (base_url, handler):
        result = mod.execute_task(
            mod.PromptTask("001", "银行账户"),
            mod.RuntimeConfig(base_url=base_url, api_key="test-key"),
            Path(tmp),
            sleep=lambda _: None,
        )
    self.assertTrue(result.success)
    self.assertEqual(result.trace_id, "trace-1")
    self.assertEqual(handler.received[0][0]["Content-Type"], "application/json; charset=utf-8")
    self.assertEqual(handler.received[0][1]["prompt"], "银行账户")

def test_context_canceled_is_not_retried(self):
    from fake_cpa import fake_cpa
    mod = load_module()
    with tempfile.TemporaryDirectory() as tmp, fake_cpa([
        (500, {}, {"error": {"message": "context canceled"}}),
    ]) as (base_url, handler):
        result = mod.execute_task(
            mod.PromptTask("001", "test"),
            mod.RuntimeConfig(base_url=base_url, api_key="test-key"),
            Path(tmp),
            sleep=lambda _: None,
        )
    self.assertFalse(result.success)
    self.assertEqual(result.attempts, 1)
    self.assertEqual(len(handler.received), 1)

def test_503_retries_once_then_succeeds(self):
    from fake_cpa import fake_cpa
    mod = load_module()
    with tempfile.TemporaryDirectory() as tmp, fake_cpa([
        (503, {}, {"error": {"message": "busy"}}),
        (200, {}, {"data": [{"b64_json": PNG_1X1_B64}]})
    ]) as (base_url, handler):
        result = mod.execute_task(
            mod.PromptTask("001", "test"),
            mod.RuntimeConfig(base_url=base_url, api_key="test-key"),
            Path(tmp),
            sleep=lambda _: None,
        )
    self.assertTrue(result.success)
    self.assertEqual(result.attempts, 2)
    self.assertEqual(len(handler.received), 2)
```

Also assert `DEFAULT_REQUEST_TIMEOUT == 180.0` and that invalid Base64 produces a failed `TaskResult` without a file.

- [ ] **Step 3: Run the new tests and verify RED**

Run:

```powershell
$env:PYTHONPATH='C:\Users\prepare\.codex\skills\cpa-batch-image\tests'
python -m unittest C:\Users\prepare\.codex\skills\cpa-batch-image\tests\test_batch_generate.py -v
```

Expected: input tests PASS; HTTP tests FAIL because `RuntimeConfig`, `TaskResult`, and `execute_task` are missing.

- [ ] **Step 4: Implement the minimal HTTP executor**

Add exact public types and behavior:

```python
import base64
import random
import time
import urllib.error
import urllib.request
from dataclasses import asdict
from datetime import datetime, timezone
from typing import Callable

RETRYABLE_STATUS = {408, 429, 502, 503, 504}


@dataclass(frozen=True)
class RuntimeConfig:
    base_url: str
    api_key: str
    request: RequestConfig = RequestConfig()


@dataclass
class TaskResult:
    id: str
    success: bool
    attempts: int
    started_at: str
    finished_at: str
    elapsed_seconds: float
    status_code: int | None = None
    trace_id: str = ""
    output_path: str = ""
    error: str = ""


def _safe_error(text: str, limit: int = 500) -> str:
    return " ".join(text.split())[:limit]


def _should_retry(status: int, body: str) -> bool:
    if "context canceled" in body.lower():
        return False
    return status in RETRYABLE_STATUS or status == 500


def execute_task(
    task: PromptTask,
    runtime: RuntimeConfig,
    output_dir: Path,
    sleep: Callable[[float], None] = time.sleep,
) -> TaskResult:
    started_clock = time.monotonic()
    started_at = datetime.now(timezone.utc).isoformat()
    last_status = None
    last_error = ""
    trace_id = ""
    for attempt in (1, 2):
        request = urllib.request.Request(
            runtime.base_url.rstrip("/") + "/images/generations",
            data=encode_request_body(task, runtime.request),
            method="POST",
            headers={
                "Authorization": f"Bearer {runtime.api_key}",
                "Content-Type": "application/json; charset=utf-8",
                "Accept": "application/json",
            },
        )
        try:
            with urllib.request.urlopen(request, timeout=runtime.request.timeout) as response:
                payload = json.loads(response.read().decode("utf-8"))
                trace_id = response.headers.get("X-CPA-TRACE-ID", "")
                encoded = payload["data"][0]["b64_json"]
                image = base64.b64decode(encoded, validate=True)
                if not image:
                    raise ValueError("empty decoded image")
                output_path = output_dir / f"{task.id}.{runtime.request.output_format}"
                output_path.write_bytes(image)
                finished = datetime.now(timezone.utc).isoformat()
                return TaskResult(
                    id=task.id,
                    success=True,
                    attempts=attempt,
                    started_at=started_at,
                    finished_at=finished,
                    elapsed_seconds=round(time.monotonic() - started_clock, 3),
                    status_code=response.status,
                    trace_id=trace_id,
                    output_path=str(output_path),
                )
        except urllib.error.HTTPError as exc:
            last_status = exc.code
            trace_id = exc.headers.get("X-CPA-TRACE-ID", "")
            last_error = exc.read().decode("utf-8", errors="replace")
            if attempt == 1 and _should_retry(exc.code, last_error):
                retry_after = exc.headers.get("Retry-After")
                delay = float(retry_after) if retry_after and retry_after.isdigit() else (
                    random.uniform(10, 20) if exc.code == 429 else 3.0
                )
                sleep(delay)
                continue
            break
        except (TimeoutError, OSError, ValueError, KeyError, json.JSONDecodeError) as exc:
            last_error = str(exc)
            break
    return TaskResult(
        id=task.id,
        success=False,
        attempts=attempt,
        started_at=started_at,
        finished_at=datetime.now(timezone.utc).isoformat(),
        elapsed_seconds=round(time.monotonic() - started_clock, 3),
        status_code=last_status,
        trace_id=trace_id,
        error=_safe_error(last_error),
    )
```

Do not add retries for timeout, `OSError`, malformed response, authentication, or 4xx statuses other than 408/429.

- [ ] **Step 5: Run HTTP tests and verify GREEN**

Run the command from Step 3.

Expected: all input and HTTP boundary tests PASS.

### Task 4: Implement fixed waves and the 3-second inter-wave delay

**Files:**
- Modify: `C:\Users\prepare\.codex\skills\cpa-batch-image\tests\test_batch_generate.py`
- Modify: `C:\Users\prepare\.codex\skills\cpa-batch-image\scripts\batch_generate.py`

- [ ] **Step 1: Write failing wave-scheduler tests**

Add a test with 23 tasks, an in-memory `run_one`, and a sleep spy. Assert:

```python
def test_23_tasks_run_as_10_10_3_with_two_three_second_delays(self):
    mod = load_module()
    tasks = [mod.PromptTask(f"{i:03d}", f"prompt {i}") for i in range(1, 24)]
    active = 0
    max_active = 0
    lock = threading.Lock()
    release = threading.Event()
    started_by_wave = []
    delays = []

    def run_one(task):
        nonlocal active, max_active
        with lock:
            active += 1
            max_active = max(max_active, active)
        time.sleep(0.01)
        with lock:
            active -= 1
        return mod.TaskResult(task.id, True, 1, "s", "f", 0.01)

    results = mod.run_waves(
        tasks,
        run_one=run_one,
        concurrency=10,
        batch_delay=3.0,
        sleep=delays.append,
        on_wave=lambda ids: started_by_wave.append(ids),
    )

    self.assertEqual([len(ids) for ids in started_by_wave], [10, 10, 3])
    self.assertEqual(delays, [3.0, 3.0])
    self.assertLessEqual(max_active, 10)
    self.assertEqual(len(results), 23)
```

Add separate tests that reject concurrency `0` and `11`, that a final single wave produces no delay, and that one unexpected worker exception becomes a failed `TaskResult` without preventing the rest of that wave from completing.

- [ ] **Step 2: Run the scheduler tests and verify RED**

Expected: FAIL because `run_waves` is missing.

- [ ] **Step 3: Implement the fixed-wave scheduler**

Add:

```python
from concurrent.futures import ThreadPoolExecutor, as_completed


def run_waves(
    tasks: list[PromptTask],
    run_one,
    concurrency: int = MAX_CONCURRENCY,
    batch_delay: float = DEFAULT_BATCH_DELAY,
    sleep=time.sleep,
    on_wave=lambda ids: None,
    on_result=lambda result: None,
) -> list[TaskResult]:
    if not 1 <= concurrency <= MAX_CONCURRENCY:
        raise PreflightError("concurrency must be between 1 and 10")
    results: list[TaskResult] = []
    for offset in range(0, len(tasks), concurrency):
        wave = tasks[offset:offset + concurrency]
        on_wave([task.id for task in wave])
        with ThreadPoolExecutor(max_workers=len(wave)) as pool:
            futures = {pool.submit(run_one, task): task for task in wave}
            wave_results = []
            for future in as_completed(futures):
                task = futures[future]
                try:
                    result = future.result()
                except Exception as exc:
                    now = datetime.now(timezone.utc).isoformat()
                    result = TaskResult(
                        id=task.id,
                        success=False,
                        attempts=1,
                        started_at=now,
                        finished_at=now,
                        elapsed_seconds=0.0,
                        error=_safe_error(f"unexpected worker error: {exc}"),
                    )
                wave_results.append(result)
                on_result(result)
        results.extend(sorted(wave_results, key=lambda item: item.id))
        if offset + concurrency < len(tasks):
            sleep(batch_delay)
    return results
```

The delay must occur after the executor has joined all tasks in the wave, never after dispatch and never after the final wave.

- [ ] **Step 4: Run the scheduler suite and verify GREEN**

Expected: wave sizes `[10, 10, 3]`, delays `[3.0, 3.0]`, maximum in-flight `<= 10`, all tests PASS.

### Task 5: Add atomic manifests, safe resume, and the global batch lock

**Files:**
- Modify: `C:\Users\prepare\.codex\skills\cpa-batch-image\tests\test_batch_generate.py`
- Modify: `C:\Users\prepare\.codex\skills\cpa-batch-image\scripts\batch_generate.py`

- [ ] **Step 1: Write failing checkpoint and resume tests**

Cover these behaviors independently:

```text
ManifestStore.record(result) atomically updates manifest.json.
The manifest contains prompt summary, prompt SHA-256, status, attempts, retry count, trace ID, output path, and error.
completed_task_ids only returns successes whose prompt hash still matches and whose output file exists.
Two processes using batch_lock cannot enter the critical section simultaneously.
The lock is released when the first process raises an exception.
Cancellation while waiting for the lock does not attempt to unlock a lock the process never acquired.
```

Use `tempfile.TemporaryDirectory`, `multiprocessing`, and events/queues. Do not fake the lock with a thread-only `threading.Lock`.

- [ ] **Step 2: Run the new tests and verify RED**

Expected: FAIL because `ManifestStore`, `prompt_hash`, `completed_task_ids`, and `batch_lock` are missing.

- [ ] **Step 3: Implement atomic checkpointing**

Add:

```python
import hashlib
import os
import threading
from contextlib import contextmanager


def prompt_hash(prompt: str) -> str:
    return hashlib.sha256(prompt.encode("utf-8")).hexdigest()


class ManifestStore:
    def __init__(self, output_dir: Path, tasks: list[PromptTask]):
        self.output_dir = output_dir
        self.path = output_dir / "manifest.json"
        self.tasks = {task.id: task for task in tasks}
        self._lock = threading.Lock()
        self._entries = {}
        if self.path.exists():
            self._entries = json.loads(self.path.read_text(encoding="utf-8")).get("results", {})

    def record(self, result: TaskResult) -> None:
        task = self.tasks[result.id]
        entry = asdict(result)
        entry["prompt_summary"] = " ".join(task.prompt.split())[:120]
        entry["prompt_sha256"] = prompt_hash(task.prompt)
        entry["retry_count"] = max(result.attempts - 1, 0)
        with self._lock:
            self._entries[result.id] = entry
            payload = {"version": 1, "results": self._entries}
            temporary = self.path.with_suffix(".json.tmp")
            temporary.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
            os.replace(temporary, self.path)

    def completed_task_ids(self) -> set[str]:
        completed = set()
        for task_id, entry in self._entries.items():
            task = self.tasks.get(task_id)
            output = Path(entry.get("output_path", ""))
            if (
                task
                and entry.get("success") is True
                and entry.get("prompt_sha256") == prompt_hash(task.prompt)
                and output.is_file()
                and output.stat().st_size > 0
            ):
                completed.add(task_id)
        return completed
```

- [ ] **Step 4: Implement a cross-platform process lock**

Add a `batch_lock(lock_path: Path)` context manager that opens one lock file for the whole batch:

```python
@contextmanager
def batch_lock(
    lock_path: Path,
    poll_seconds: float = 1.0,
    on_wait=lambda: None,
):
    lock_path.parent.mkdir(parents=True, exist_ok=True)
    handle = lock_path.open("a+b")
    acquired = False
    handle.seek(0, os.SEEK_END)
    if handle.tell() == 0:
        handle.write(b"0")
        handle.flush()
    try:
        while True:
            try:
                handle.seek(0)
                if os.name == "nt":
                    import msvcrt
                    msvcrt.locking(handle.fileno(), msvcrt.LK_NBLCK, 1)
                else:
                    import fcntl
                    fcntl.flock(handle.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                acquired = True
                break
            except OSError:
                on_wait()
                time.sleep(poll_seconds)
        yield
    finally:
        try:
            if acquired:
                handle.seek(0)
                if os.name == "nt":
                    import msvcrt
                    msvcrt.locking(handle.fileno(), msvcrt.LK_UNLCK, 1)
                else:
                    import fcntl
                    fcntl.flock(handle.fileno(), fcntl.LOCK_UN)
        finally:
            handle.close()
```

Use an OS-owned lock, not PID-file deletion. OS locks release automatically if the process exits unexpectedly.

- [ ] **Step 5: Run checkpoint, resume, and process-lock tests**

Expected: all new tests PASS; the second process enters only after the first releases the lock.

### Task 6: Build the CLI and end-to-end dry run

**Files:**
- Modify: `C:\Users\prepare\.codex\skills\cpa-batch-image\tests\test_batch_generate.py`
- Modify: `C:\Users\prepare\.codex\skills\cpa-batch-image\scripts\batch_generate.py`

- [ ] **Step 1: Write failing CLI tests**

Use `subprocess.run` against the real script. Cover:

```text
--dry-run with 23 tasks reports three waves 10/10/3 and makes no network call.
Missing CPA_API_KEY fails before creating network tasks, except in --dry-run.
--concurrency 11 exits with a clear validation error.
--resume without an explicit --out-dir exits before any network call.
--resume skips successful matching files and runs only missing/failed tasks.
Partial failure exits 2 while retaining manifest.json and successful images.
```

- [ ] **Step 2: Run CLI tests and verify RED**

Expected: FAIL because `main()` and argument parsing are missing.

- [ ] **Step 3: Implement the CLI entry point**

The parser must expose exactly:

```text
--input PATH                 required prompts JSON
--out-dir PATH               optional; default output/cpa-batch-image/<timestamp>
--size VALUE                 default 1024x1024
--quality low|medium|high|auto
--output-format png|jpeg|webp
--concurrency INTEGER        default 10, range 1..10
--resume                     reuse manifest in --out-dir
--allow-suspicious-text      explicit mojibake-check override
--dry-run                    preflight and print wave plan without API key or network
```

Implement `main(argv=None) -> int` with this order:

```python
parse arguments
load and validate tasks
validate concurrency and that --resume has --out-dir
resolve the output directory to an absolute path
if dry-run: print wave plan and return 0 without API key, lock, files, or network
read CPA_API_KEY; fail if missing
read CPA_BASE_URL or use http://23.138.12.112:8317/v1
acquire ~/.codex/state/cpa-batch-image/batch.lock
inside the lock, create or reuse the output directory
write prompts.json with ensure_ascii=False
construct or reload ManifestStore inside the lock
remove completed IDs only when --resume is set, after acquiring the lock
run_waves(..., on_result=manifest.record)
print totals and output directory
return 0 if all selected tasks succeeded, otherwise 2
```

Wrap the entry point:

```python
if __name__ == "__main__":
    raise SystemExit(main())
```

Never print the API Key or Authorization header.

Catch preflight/configuration errors at the CLI boundary, print a concise message to stderr, and return exit code `1`. Keeping resume selection inside the global lock prevents a queued process from regenerating work completed by the process ahead of it.

Do not expose a CLI override for the inter-wave delay. The user-confirmed production rule is fixed at `DEFAULT_BATCH_DELAY = 3.0`; the `run_waves` argument remains injectable only for deterministic tests.

- [ ] **Step 4: Run the full local suite and a 23-task dry run**

Run:

```powershell
python -m unittest discover -s C:\Users\prepare\.codex\skills\cpa-batch-image\tests -v
python C:\Users\prepare\.codex\skills\cpa-batch-image\scripts\batch_generate.py `
  --input C:\path\to\23-prompts.json `
  --dry-run
```

Expected: all tests PASS; dry run prints `wave 1: 10`, `wave 2: 10`, `wave 3: 3`, and no HTTP request occurs.

### Task 7: Write the Skill instructions and interface metadata

**Files:**
- Modify: `C:\Users\prepare\.codex\skills\cpa-batch-image\SKILL.md`
- Modify: `C:\Users\prepare\.codex\skills\cpa-batch-image\agents\openai.yaml`
- Create: `C:\Users\prepare\.codex\skills\cpa-batch-image\references\input-schema.md`

- [ ] **Step 1: Replace the generated `SKILL.md`**

Use this frontmatter exactly:

```yaml
---
name: cpa-batch-image
description: Use when the user asks for 批量生图、批量生成图片、一次生成多张图片、根据文案生成配图, or supplies multiple prompts or a manuscript for image generation through the user's CPA gpt-image-2 endpoint.
---
```

The body must instruct the future agent to:

```text
Choose this CPA execution path for matching batch requests instead of built-in imagegen.
Accept a prompt list directly, or split a manuscript into the requested count; default manuscript count is 6.
Write UTF-8 prompts.json using the documented schema.
Show count, size, quality, concurrency, and output path before starting; do not re-ask already supplied values.
Run the bundled script, never reimplement the HTTP client ad hoc.
Use n=1 per prompt, concurrency <=10, complete-wave wait, 3-second non-final-wave delay, and 180-second request timeout.
Let the runner own the cross-process global lock, per-image manifest checkpoints, and `--resume`; do not launch a second runner to bypass a busy lock.
Wait for long-running shell execution and provide progress updates at least every 60 seconds.
Inspect manifest.json and report output path, successes, failures, and exact failure summaries.
Never claim all success when any item failed.
Never retry context canceled or a client timeout outside the script.
```

Keep the body under 250 lines and link once to `references/input-schema.md`.

- [ ] **Step 2: Write `references\input-schema.md`**

Document this exact input form:

```json
{
  "tasks": [
    {"id": "001", "prompt": "第一张图片的完整独立提示词"},
    {"id": "002", "prompt": "第二张图片的完整独立提示词"}
  ]
}
```

Document output names, manifest fields, `--resume`, exit codes `0/1/2`, and the rule that source JSON is UTF-8 without BOM or legacy encoding conversion.

- [ ] **Step 3: Regenerate `agents\openai.yaml` from the finished Skill**

Run:

```powershell
python C:\Users\prepare\.codex\skills\.system\skill-creator\scripts\generate_openai_yaml.py `
  C:\Users\prepare\.codex\skills\cpa-batch-image `
  --interface 'display_name=CPA 批量生图' `
  --interface 'short_description=通过 CPA 分批生成并保存多张 GPT Image 2 图片' `
  --interface 'default_prompt=Use $cpa-batch-image to generate images from these prompts or this manuscript.'
```

Expected: quoted interface strings; `default_prompt` explicitly mentions `$cpa-batch-image`; no icons or MCP dependencies are invented.

### Task 8: Validate Skill behavior and close baseline loopholes

**Files:**
- Modify only if validation exposes a gap: `C:\Users\prepare\.codex\skills\cpa-batch-image\SKILL.md`
- Modify only if tests expose a bug: `C:\Users\prepare\.codex\skills\cpa-batch-image\scripts\batch_generate.py`

- [ ] **Step 1: Run official structural validation**

Run:

```powershell
python C:\Users\prepare\.codex\skills\.system\skill-creator\scripts\quick_validate.py `
  C:\Users\prepare\.codex\skills\cpa-batch-image
```

Expected: validation succeeds with no frontmatter, naming, or structure errors.

- [ ] **Step 2: Run the complete test suite**

Run:

```powershell
python -m unittest discover -s C:\Users\prepare\.codex\skills\cpa-batch-image\tests -v
```

Expected: all tests PASS with no network access outside localhost.

- [ ] **Step 3: Forward-test the installed Skill**

Dispatch a fresh read-only validation agent with this exact request and the installed Skill path:

```text
Use $cpa-batch-image at C:\Users\prepare\.codex\skills\cpa-batch-image. I have a manuscript and want 23 images. Explain the exact execution you will perform, but use dry-run and do not call a live API.
```

Verify the response includes `10 → 3 seconds → 10 → 3 seconds → 3`, `n=1`, `180 seconds`, UTF-8 preflight, no retry for `context canceled`, global lock, manifest, and partial-failure reporting.

- [ ] **Step 4: Tighten only observed gaps and re-run validation**

If the forward-test misses an invariant, add one concise imperative rule to `SKILL.md`, rerun Step 3, then rerun `quick_validate.py`. Do not add narrative history or hypothetical features.

### Task 9: Perform one real CPA smoke test and inventory the installation

**Files:**
- Create via the Skill: `C:\Users\prepare\Documents\视频号混剪\output\cpa-batch-image\<timestamp>\001.png`
- Create via the Skill: matching `prompts.json` and `manifest.json`

- [ ] **Step 1: Confirm the API key is available only as an environment variable**

Run:

```powershell
if (-not $env:CPA_API_KEY) { throw 'CPA_API_KEY is not set in this process' }
```

Do not print its value. Set `CPA_BASE_URL` to `http://23.138.12.112:8317/v1` only if it is absent. If the key is unavailable, stop before the live request, report the exact prerequisite, and do not substitute a key from chat history or write it into a command, file, or log.

- [ ] **Step 2: Create one harmless UTF-8 smoke prompt**

Use:

```json
{
  "tasks": [
    {
      "id": "001",
      "prompt": "A simple red circle centered on a plain white background, minimal flat design, no text."
    }
  ]
}
```

- [ ] **Step 3: Run exactly one live low-quality request**

Run the installed script with `--concurrency 1`, `size=1024x1024`, `quality=low`, and a dedicated output directory. Set the shell command runtime high enough to outlive the 180-second request timeout.

Expected: exit `0`; `001.png` is non-empty; manifest status is success; elapsed time and CPA trace ID are recorded when provided. This test consumes one low-quality image generation and must not be repeated unless it fails before reaching CPA.

- [ ] **Step 4: Verify artifacts and installed-file hashes**

Run:

```powershell
Get-ChildItem C:\Users\prepare\.codex\skills\cpa-batch-image -Recurse -File |
  Sort-Object FullName |
  Get-FileHash -Algorithm SHA256
```

Expected: every installed file has a SHA-256 value; no API key appears in `SKILL.md`, `openai.yaml`, scripts, references, tests, prompt files, or manifest.

Perform the secret check by reading `CPA_API_KEY` into memory and failing if any artifact contains that exact value; never print the value while checking. References to the environment-variable name `CPA_API_KEY` and the HTTP header name `Authorization` are expected and are not embedded secrets.

- [ ] **Step 5: Final verification summary**

Report:

```text
Skill path
trigger phrases
test count and status
quick_validate status
live smoke image path
wave rule: max 10, wait all, delay 3 seconds, next wave
timeout: 180 seconds
API key storage: environment only
```
