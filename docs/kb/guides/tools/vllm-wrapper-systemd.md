---
title: Run vLLM under systemd with vllm-wrapper
summary: Start vLLM as a transient systemd user service from vllm-wrapper so it survives llama-swap stopping the model, and forward its journal into llama-swap's logs.
category: guides
tags: [vllm-wrapper, vllm, systemd, systemd-run, journal, linux, tools]
config_keys: [models.*.cmd, models.*.cmdStop]
updated: 2026-10-23
---

# Run vLLM under systemd with vllm-wrapper

With `vllm-wrapper serve`, vLLM is normally a child of the wrapper, inside the
process group llama-swap controls. Starting it with `systemd-run --user` puts it
under systemd instead, so it stays alive when llama-swap restarts or force-stops
the model. See `guides/tools/vllm-wrapper` for the basics.

This assumes llama-swap runs as a system service user named `llama`.

## Let the service user run systemd units

Enable the user manager and expose its D-Bus session to `llama.service`:

```bash
sudo loginctl enable-linger llama
uid=$(id -u llama)
sudo systemctl start "user@${uid}.service"

sudo mkdir -p /etc/systemd/system/llama.service.d
sudo tee /etc/systemd/system/llama.service.d/user-bus.conf >/dev/null <<EOF
[Unit]
Wants=user@${uid}.service
After=user@${uid}.service

[Service]
Environment=XDG_RUNTIME_DIR=/run/user/${uid}
Environment=DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/${uid}/bus
EOF

sudo systemctl daemon-reload
sudo systemctl restart llama.service
```

## Config

```yaml
models:
  qwen:
    cmd: |
      vllm-wrapper serve
      --vllm-url http://127.0.0.1:18000
      --listen :${PORT}
      --wait-timeout 5m
      --journal-unit vllm-qwen.service
      --
      systemd-run
      --user
      --unit=vllm-qwen
      --collect
      --property=Restart=no
      --property=StandardOutput=journal
      --property=StandardError=journal
      --setenv=HF_HOME
      --
      vllm serve /models/qwen
      --port 18000
      --enable-sleep-mode
    cmdStop: vllm-wrapper sleep --vllm-url http://127.0.0.1:18000 --stop-pid ${PID}
    proxy: http://127.0.0.1:${PORT}
```

`--journal-unit` copies new journal lines of that unit into the wrapper's output,
so vLLM's logs show up in llama-swap's log stream.

## What goes wrong

- **Environment variables are missing in vLLM.** The unit does not inherit the
  environment. Pass each variable with `--setenv=NAME`.
- **`Failed to connect to bus`.** The `llama.service` drop-in above is missing or
  `llama.service` was not restarted after adding it.
- **Unit name already exists.** Use a different `--unit` per model.
- **No vLLM logs in llama-swap.** `--journal-unit` must be the unit name including
  `.service`, and the unit must send output to the journal
  (`StandardOutput=journal`).

## Inspect the service

```bash
uid=$(id -u llama)
sudo -u llama env XDG_RUNTIME_DIR="/run/user/${uid}" \
  DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/${uid}/bus" \
  systemctl --user status vllm-qwen.service
```

Replace `systemctl --user status vllm-qwen.service` with
`journalctl --user -u vllm-qwen.service -f -o cat` to follow the logs.
