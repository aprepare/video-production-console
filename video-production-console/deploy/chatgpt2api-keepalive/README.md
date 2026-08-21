# chatgpt2api 1.8.0 image SSE keepalive

Build candidate on host port 18320; production remains untouched.

```powershell
ssh cpa docker exec chatgpt2api sh -lc 'python -c "from pathlib import Path; print(Path(\"/app/services/log_service.py\").read_text())"' > log_service.py.tmp
python patch_log_service.py
python -m py_compile log_service.py.tmp
docker build -t chatgpt2api:1.8.0-keepalive .
docker run -d --name chatgpt2api-keepalive --restart unless-stopped -p 18320:80 -v /opt/chatgpt2api/config.json:/app/config.json:rw -v /opt/chatgpt2api/data:/app/data:rw chatgpt2api:1.8.0-keepalive
docker logs --tail 50 chatgpt2api-keepalive
curl http://127.0.0.1:18320/health
```

In the console, enable `image_stream` before testing image generation. After validation, stop the old container and switch ports/names deliberately. Roll back by stopping the candidate and restarting the previous container. The patch script is the sole injection mechanism; applying it twice fails by design.
