"""Reproducible offline validation; all Go integration tests use test fixtures."""
import json
import pathlib
import re
import subprocess
import sys
import time

ROOT = pathlib.Path(__file__).resolve().parents[2]
OUT = pathlib.Path(__file__).resolve().parent
BASELINE = "c88ec86ce854049981f866e980df0efec1cca491"
COMMANDS = {
    "go-build": ["go", "build", "./..."],
    "go-vet": ["go", "vet", "./..."],
    "go-test": ["go", "test", "./...", "-count=1"],
    "media-integration": ["go", "test", "./internal/replicate", "-run", "TestStitchRealMedia", "-count=1", "-v"],
    "typescript": ["node", "C:/Users/prepare/.codex/worktrees/视频号混剪/video-production-console/video-production-console/web/node_modules/typescript/bin/tsc", "--noEmit", "--strict", "--target", "ES2022", "--skipLibCheck", "web/src/replicate/replicateTypes.ts"],
}

def git(*args):
    return subprocess.check_output(["git", "-c", "core.quotepath=false", "-c", "core.safecrlf=false", *args], cwd=ROOT, stderr=subprocess.DEVNULL).decode("utf-8")

name = sys.argv[1]
if name == "scope":
    baseline = git("show", BASELINE + ":video-production-console/internal/store/migrations.go")
    current = (ROOT / "internal/store/migrations.go").read_text(encoding="utf-8")
    def migrations(source):
        body = source.split("var migrations = []string{", 1)[1].split("\n}\n", 1)[0]
        return re.findall(r"`([^`]*)`", body, re.S)
    old, new = migrations(baseline), migrations(current)
    changed = git("diff", "--name-only").splitlines()
    changed += git("ls-files", "--others", "--exclude-standard").splitlines()
    forbidden = [p for p in changed if ("/web/" in "/" + p and not p.endswith("web/src/replicate/replicateTypes.ts")) or "video-console-data/" in p]
    data = {"old_migrations": len(old), "new_migrations": len(new), "migrations_1_to_49_unchanged": old == new[:len(old)], "forbidden_changed_files": forbidden, "branch": git("branch", "--show-current").strip()}
    (OUT / "scope-check.json").write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(data, ensure_ascii=False))
    sys.exit(0 if len(old) == 49 and len(new) == 50 and old == new[:49] and not forbidden else 1)

command = COMMANDS[name]
start = time.monotonic()
result = subprocess.run(command, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
elapsed = round(time.monotonic() - start, 3)
text = result.stdout.decode("utf-8", errors="replace")
log = "$ " + " ".join(command) + "\n" + text + f"\nexit_code={result.returncode}\nelapsed_s={elapsed}\n"
(OUT / f"{name}.log").write_text(log, encoding="utf-8")
summary = {"command": command, "exit_code": result.returncode, "elapsed_s": elapsed, "log": f"{name}.log"}
(OUT / f"{name}.json").write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
print(json.dumps(summary))
if result.returncode:
    print(text[-12000:])
sys.exit(result.returncode)
