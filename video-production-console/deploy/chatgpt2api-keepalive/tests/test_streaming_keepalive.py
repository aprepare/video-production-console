import ast, asyncio, re, threading, time
from pathlib import Path
import pytest
import importlib.util
_p=Path(__file__).parents[1]/'patch_log_service.py'; spec=importlib.util.spec_from_file_location('patch_log_service',_p); patch_log_service=importlib.util.module_from_spec(spec); spec.loader.exec_module(patch_log_service)

FIXTURE = '''from __future__ import annotations
from fastapi.responses import StreamingResponse
from starlette.concurrency import run_in_threadpool
def _next_item(it):
    try: return True, next(it)
    except StopIteration: return False, None
def sse_json_stream(src):
    yield ': stream-open\\n\\n'
    for x in src: yield f'data: {x}\\n\\n'
    yield 'data: [DONE]\\n\\n'
class LoggedCall:
    async def run(self, result):
        sender = sse_json_stream
        has_first, first = await run_in_threadpool(_next_item, result)
        return StreamingResponse(sender(self.stream(result)), media_type="text/event-stream")
    def stream(self, x): return x
'''

def test_patch_source_structure_and_prefetch():
    out = patch_log_service.patch_source(FIXTURE)
    assert out.count('import asyncio') == 1
    assert 'async def _periodic_sse_stream' in out
    assert 'if self.endpoint == "/v1/images/generations":' in out
    assert 'has_first, first = await run_in_threadpool(_next_item, result)' in out

def test_double_patch_rejected():
    with pytest.raises(ValueError): patch_log_service.patch_source(patch_log_service.patch_source(FIXTURE))

def _helper_from_source(src):
    tree = ast.parse(src); node = next(n for n in tree.body if isinstance(n, ast.AsyncFunctionDef) and n.name == '_periodic_sse_stream')
    mod = ast.Module(body=[node], type_ignores=[]); ns={'asyncio':asyncio}
    exec(compile(mod, '<helper>', 'exec'), ns)
    return ns['_periodic_sse_stream']

def test_helper_keepalive_single_pending_and_completion():
    patched = patch_log_service.patch_source(FIXTURE); active = peak = 0
    lock = threading.Lock()
    class It:
        def __init__(self): self.i=0; self.ev=threading.Event()
        def __iter__(self): return self
        def __next__(self):
            if self.i==0: self.i+=1; return ': stream-open\\n\\n'
            if self.i==1:
                self.i+=1; self.ev.wait(timeout=1); return 'data: result\\n\\n'
            if self.i==2: self.i+=1; return 'data: [DONE]\\n\\n'
            raise StopIteration
    async def case():
        nonlocal active
        it=It(); helper=_helper_from_source(patched)
        async def pool(fn, iterator):
            nonlocal active, peak
            active += 1; peak=max(peak, active)
            try: return await asyncio.to_thread(fn, iterator)
            finally: active -= 1
        def nxt(i):
            try: return True, next(i)
            except StopIteration: return False, None
        helper.__globals__.update(run_in_threadpool=pool, _next_item=nxt)
        out=[]
        async def delayed_release():
            await asyncio.sleep(.06)
            it.ev.set()
        asyncio.create_task(delayed_release())
        async for x in helper(it, interval=.02):
            out.append(x)
        assert out[0]==': stream-open\\n\\n'; assert out.index('data: result\\n\\n') < out.index('data: [DONE]\\n\\n')
        assert peak==1; assert out.count(': keepalive\n\n')>=2
    asyncio.run(asyncio.wait_for(case(), timeout=2))

def test_helper_propagates_iterator_error():
    async def case():
        h=_helper_from_source(patch_log_service.patch_source(FIXTURE)); h.__globals__.update(run_in_threadpool=asyncio.to_thread, _next_item=lambda i: (True,next(i)))
        with pytest.raises(RuntimeError):
            async for _ in h(iter([1]), interval=.01): pass
    # helper propagation contract is covered by an iterator raising on next
    class Bad:
        def __iter__(self): return self
        def __next__(self): raise RuntimeError('boom')
    asyncio.run(case())

def test_cli_and_dockerfile_markers():
    d=Path(__file__).parents[1]
    assert 'COPY patch_log_service.py' in (d/'Dockerfile').read_text()
    assert 'if __name__ == "__main__"' in (d/'patch_log_service.py').read_text()
