# Add support for ${UNIX} domain sockets

## Overview

Add support for `${UNIX}` which works like `${PORT}` but is a macro with a path
to an automatically generated unix domain socket.

Configuration example:

```yaml
models:
  my_model: llama-server -host ${UNIX} -m mymodel.gguf
```

This will:

    set ${UNIX} to a unique, safe /path/to/my_model.sock to use.
    set models.my_model.proxy to /path/to/my_model.sock

## Design rules

- add a Config.SocketPath (`socketPath`) , which has a sensible cross platform default that works on POSIX systems (OSX, Linux, BSDs, etc)
- a model can not use ${PORT} and ${UNIX} at the same time. This is because the automatic ModelConfig.Proxy value has to be set in proxy/config/config.go. If it uses both a configuration error should be returned.
- only works in POSIX systems that support unix domain sockets. On Windows the configuration should output an error
  - The windows config tests, proxy/config/config_windows_test.go, should test that an error occurs
- a domain socket path and file name should:
  - always end in .sock
  - be created in Config.SocketPath
  - be based on the model name, eg: "my_model" becomes "Config.SocketPath/my_model.sock". Model names can contain invalid path characters. These should be replaced with safe characters like "-". Example: "//my/model////" turns into "my-model"
- example and documentation added to config.example.yaml for ${UNIX} macro and the new top level `socketPath` setting
- tests should be added for socket name path creation with unsafe character substitution
