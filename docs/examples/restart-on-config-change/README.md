# Restart llama-swap on config change

Sometimes editing the configuration file can take a bit of trial and error to get a model configuration tuned just right. The `watch-and-restart.sh` script can be used to watch `config.yaml` for changes and send the `SIGHUP` signal to `llama-swap` to trigger a config file reload when it detects a change.

```bash
#!/bin/bash
#
# A simple watch and restart llama-swap when its configuration
# file changes. Useful for trying out configuration changes
# without manually restarting the server each time.

# For docker users, consider replacing:
# `kill -USR1 $PID` with `docker kill -s SIGHUP container_name`

if [ -z "$1" ]; then
    echo "Usage: $0 <path to config.yaml>"
    exit 1
fi

# Start the process once
./llama-swap-linux-amd64 -config $1 -listen :1867 &
PID=$!
echo "Started llama-swap with PID $PID"

while true; do
    # Wait for modifications in the specified directory or file
    if ! inotifywait -e modify "$1" 2>/dev/null; then
        echo "Error: Failed to monitor file changes"
        break
    fi

    # Check if process exists before sending signal
    if kill -0 $PID 2>/dev/null; then
        echo "Sending SIGHUP to $PID"
        kill -USR1 $PID
    else
        echo "Process $PID no longer exists"
        break
    fi
    sleep 1
done
```

## Usage and output example

```bash
$ ./watch-and-restart.sh config.yaml
Started llama-swap with PID 495455
Setting up watches.
Watches established.
llama-swap listening on :1867
...
Sending SIGHUP to 495455
Received SIGHUP. Reloading configuration...
Configuration Changed
Configuration Reloaded
...
```
