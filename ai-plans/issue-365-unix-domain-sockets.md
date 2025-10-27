# Add Support for ${UNIX} Domain Sockets

## Title

Add ${UNIX} macro for automatic Unix domain socket path generation in model configurations

## Overview

Add support for the `${UNIX}` macro which works similarly to `${PORT}` but provides a path to an automatically generated Unix domain socket instead of a TCP port. This enables models to communicate via Unix sockets for improved performance and security on POSIX systems.

The macro will generate unique socket paths based on model IDs and handle path sanitization automatically. Unix sockets are platform-specific and will only be supported on POSIX systems (Linux, macOS, BSD), with Windows configurations explicitly rejected.

### Goals

1. Enable Unix domain socket communication between llama-swap and model servers
2. Maintain consistency with existing `${PORT}` macro behavior and validation patterns
3. Provide automatic socket path generation with safe file naming
4. Ensure platform-appropriate error handling (Windows not supported)

### Example Configuration

```yaml
socketPath: /tmp/llama-swap-sockets # optional, has sensible default

models:
  my_model:
    cmd: llama-server --unix-socket ${UNIX} -m mymodel.gguf
    # proxy is automatically set to the unix socket path
```

This will:

- Set `${UNIX}` to a unique, safe path like `/tmp/llama-swap-sockets/my_model.sock`
- Automatically set `models.my_model.proxy` to `http+unix:///tmp/llama-swap-sockets/my_model.sock`

### Non-Goals

- Windows support (Unix sockets not available)
- Mixed usage of `${PORT}` and `${UNIX}` in same model (mutual exclusion enforced)

## Design Requirements

### 1. Configuration Structure Changes

#### 1.1 Add Config.SocketPath Field

**Location**: `proxy/config/config.go`

Add new field to `Config` struct:

```go
SocketPath string `yaml:"socketPath"` // Directory for Unix domain socket files
```

**Default value**: `/tmp/llama-swap-sockets`

- **POSIX** (Linux, macOS, BSD): `/tmp/llama-swap-sockets`
- **Windows**: Empty string (not used, will error if ${UNIX} attempted)
- Directory should be created if it doesn't exist (with appropriate permissions: 0755)

**Validation rules**:

- Must be an absolute path on POSIX systems
- Must be writable directory (validate during config load)
- Socket path length (including filename) must not exceed 96 chars for maximum portability

#### 1.2 ModelConfig.Proxy Default Value Update

**Location**: `proxy/config/model_config.go`

Current default:

```go
Proxy: "http://localhost:${PORT}",
```

**Resolution**: Keep the existing default unchanged. The macro substitution logic will handle setting the proxy appropriately:
- If proxy is blank/empty, set the appropriate default for either `${PORT}` or `${UNIX}` based on which macro is used in cmd
- If proxy is not blank, leave it as-is and perform normal macro substitution

### 2. Macro Substitution Logic

#### 2.1 Unix Socket Allocation Pass

**Location**: `proxy/config/config.go` in `LoadConfigFromReader()`

Add new substitution pass **after** user macros but alongside PORT allocation (around lines 282-312):

**Algorithm**:

```
For each model:
  1. Detect if ${UNIX} used in cmd OR proxy
  2. Detect if ${PORT} used in cmd OR proxy

  3. Mutual exclusion validation:
     IF both ${UNIX} and ${PORT} detected:
       RETURN ERROR: "model '{id}' cannot use both ${PORT} and ${UNIX} macros"

  4. Platform validation:
     IF ${UNIX} detected AND runtime.GOOS == "windows":
       RETURN ERROR: "model '{id}' uses ${UNIX} which is not supported on Windows"

  5. Proxy consistency validation:
     IF proxy contains ${UNIX} AND cmd does NOT contain ${UNIX}:
       RETURN ERROR: "model '{id}' proxy uses ${UNIX} but cmd does not - ${UNIX} is only available when used in cmd"

  6. Generate socket path with collision detection:
     sanitizedName = sanitizeModelIDForPath(modelID)
     socketPath = filepath.Join(config.SocketPath, sanitizedName + ".sock")

     // Handle collisions by appending -1, -2, etc.
     IF socketPath already allocated (exists in tracking map):
       counter = 1
       WHILE true:
         socketPath = filepath.Join(config.SocketPath, sanitizedName + "-" + counter + ".sock")
         IF socketPath NOT in tracking map:
           BREAK
         counter++

     // Track allocated path
     Add socketPath to tracking map

  7. Validate socket path length:
     IF len(socketPath) > 96:  // Very conservative limit for maximum portability
       RETURN ERROR: "model '{id}' socket path too long: {socketPath} (max 96 chars)"

  8. Create macro and substitute:
     macro = MacroEntry{Name: "UNIX", Value: socketPath}
     Substitute ${UNIX} in: cmd, cmdStop, proxy, checkEndpoint, filters.stripParams, metadata

  9. Auto-set proxy if needed:
     IF proxy is blank/empty:
       IF ${UNIX} was used in cmd:
         proxy = "unix://" + socketPath
       ELSE IF ${PORT} was used in cmd:
         proxy = "http://localhost:${PORT}"
     // If proxy is not blank, leave it as-is for normal macro substitution
```

#### 2.2 Path Sanitization Function

**Location**: `proxy/config/config.go` (new helper function)

Create `sanitizeModelIDForPath(modelID string) string`:

**Rules**:

- Replace invalid path characters with `-`
- Invalid characters (POSIX): `/`, `\x00` (null)
- Additional unsafe characters to replace: `\`, `:`, `*`, `?`, `"`, `<`, `>`, `|`, whitespace
- Collapse multiple consecutive `-` into single `-`
- Trim leading/trailing `-`
- If result is empty (all invalid chars), use `"default"`
- Preserve case (don't lowercase)

**Examples**:

- `"my_model"` → `"my_model"` (unchanged, underscores are safe)
- `"//my/model////"` → `"my-model"`
- `"model:v1"` → `"model-v1"`
- `"my model"` → `"my-model"`
- `"////"` → `"default"`

### 3. Proxy URL Format for Unix Sockets

#### 3.1 URL Scheme

**Location**: Config macro substitution and proxy URL handling

**Decision**: Use `unix://` + socketPath format.

Since llama-swap always connects using HTTP (to llama.cpp's HTTP API), we don't need to specify the protocol in the URL scheme. The Unix socket is just an alternative transport to TCP.

**Default proxy when ${UNIX} is used**:

```
unix://{socketPath}
```

When socketPath is an absolute path (e.g., `/tmp/llama-swap-sockets/my_model.sock`), the resulting URL will have a triple slash: `unix:///tmp/llama-swap-sockets/my_model.sock`. This is correct URL formatting where the scheme is `unix`, the host is empty, and the path is `/tmp/llama-swap-sockets/my_model.sock`.

**Example**:

```yaml
models:
  my_model:
    cmd: llama-server --unix-socket ${UNIX} -m model.gguf
    # Auto-set proxy becomes:
    # proxy: unix:///tmp/llama-swap-sockets/my_model.sock
```

**URL parsing notes**:

- Scheme: `unix`
- Host: empty (resulting in triple slash: `unix:///path` for absolute paths)
- Path: contains the socket file path
- Example: `unix:///tmp/llama-swap-sockets/my_model.sock`

**Note**: llama.cpp server doesn't parse this URL - it only needs to listen on the Unix socket. The `unix://` URL is used internally by llama-swap to determine it should connect via Unix socket instead of TCP.

#### 3.2 HTTP Client Changes

**Location**: `proxy/process.go` in `NewProcess()` function (around line 82-126)

The existing code at line 96 uses:

```go
reverseProxy = httputil.NewSingleHostReverseProxy(proxyURL)
```

For Unix sockets, `httputil.ReverseProxy` requires a custom `Transport` with a Unix socket dialer. The HTTP protocol is still used - we're just changing the transport from TCP to Unix socket.

**Implementation required**:

1. **Detect Unix socket URLs** - Check if proxy URL scheme is `unix`
2. **Create custom Transport** - Configure `http.Transport` with Unix socket dialer
3. **Set Transport on ReverseProxy** - Assign to `reverseProxy.Transport`

**Code changes**:

```go
// In NewProcess() after line 96
if proxyURL != nil {
    reverseProxy = httputil.NewSingleHostReverseProxy(proxyURL)

    // Configure Unix socket transport if needed
    if proxyURL.Scheme == "unix" {
        // Extract socket path from URL
        // For unix:///path/to/socket.sock, the path is in proxyURL.Path
        socketPath := proxyURL.Path
        if socketPath == "" {
            return nil, fmt.Errorf("unix socket URL missing path: %s", proxyURL)
        }

        reverseProxy.Transport = &http.Transport{
            DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
                // Ignore network and addr - always dial the Unix socket
                // HTTP protocol is still used, just over Unix socket instead of TCP
                return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
            },
        }
    }

    reverseProxy.ModifyResponse = func(resp *http.Response) error {
        // ... existing code
    }
}
```

**Implementation notes**:

- The URL scheme is `unix` (simple and clear)
- The socket path is extracted from `proxyURL.Path`
- `http.Transport` still handles HTTP protocol (requests, headers, chunked encoding, etc.)
- Only the transport layer changes from TCP to Unix socket
- The custom `DialContext` ignores the `network` and `addr` parameters and always connects to the Unix socket

**Example**:

- Socket path: `/tmp/llama-swap-sockets/my_model.sock`
- Proxy URL: `unix:///tmp/llama-swap-sockets/my_model.sock`

### 4. Reserved Macro Updates

#### 4.1 Update Reserved Macro List

**Location**: `proxy/config/config.go` validation code (around lines 206-210)

Add `UNIX` to reserved macro names alongside `PORT` and `MODEL_ID`:

- Users cannot define `${UNIX}` in their macros section
- Error message: "UNIX is a reserved macro name and cannot be redefined"

### 5. Configuration Validation and Initialization

#### 5.1 SocketPath Initialization and Validation

**Location**: `proxy/config/config.go` in `LoadConfigFromReader()`

After loading the YAML config, initialize and validate socketPath:

```go
// Set default socketPath on POSIX systems if not specified
if runtime.GOOS != "windows" && config.SocketPath == "" {
    config.SocketPath = "/tmp/llama-swap-sockets"
}

// Validate socketPath if any model uses ${UNIX}
// (this check happens after scanning all models for ${UNIX} usage)
if anyModelUsesUnix && config.SocketPath != "" {
    // Validate absolute path
    if !filepath.IsAbs(config.SocketPath) {
        return nil, fmt.Errorf("socketPath must be an absolute path, got: %s", config.SocketPath)
    }

    // Create directory if it doesn't exist
    if err := os.MkdirAll(config.SocketPath, 0755); err != nil {
        return nil, fmt.Errorf("failed to create socket directory '%s': %w", config.SocketPath, err)
    }

    // Verify directory is writable by attempting to create a test file
    testFile := filepath.Join(config.SocketPath, ".write-test")
    if err := os.WriteFile(testFile, []byte{}, 0644); err != nil {
        return nil, fmt.Errorf("socketPath '%s' is not writable: %w", config.SocketPath, err)
    }
    os.Remove(testFile) // Clean up test file
}
```

**Notes**:
- socketPath can be set on Windows without error (it's simply ignored unless ${UNIX} is used)
- Validation only occurs if at least one model uses ${UNIX}
- Relative paths are rejected with a clear error message
- Directory creation and write permission are verified during config load

#### 5.2 Platform-Specific Error Detection

**Location**: `proxy/config/config.go` in LoadConfigFromReader()

Add platform check during UNIX macro detection:

```go
if runtime.GOOS == "windows" && (cmdHasUnix || proxyHasUnix) {
    return nil, fmt.Errorf("model '%s' uses ${UNIX} which is not supported on Windows", id)
}
```

**Note**: This only errors when ${UNIX} is actually used on Windows, not when socketPath is merely configured.

### 6. Socket File Lifecycle Management

#### 6.1 Socket File Cleanup

**Location**: `proxy/process.go`

Unix domain socket files must be cleaned up to avoid leaving stale socket files on the filesystem.

**Implementation locations**:

1. **Before Process.Start()** - Remove stale socket file before starting the model server
2. **In Process.Stop()** - Remove socket file after stopping the model server

**Code changes for cleanup before start**:

In `Process.Start()` method (before starting the command):

```go
// Clean up stale Unix socket file if using Unix socket
if p.proxyURL != nil && p.proxyURL.Scheme == "unix" {
    socketPath := p.proxyURL.Path
    if socketPath != "" {
        // Remove socket file if it exists (ignore errors if it doesn't exist)
        os.Remove(socketPath)
    }
}
```

**Code changes for cleanup after stop**:

In `Process.Stop()` method (after stopping the command):

```go
// Clean up Unix socket file if using Unix socket
if p.proxyURL != nil && p.proxyURL.Scheme == "unix" {
    socketPath := p.proxyURL.Path
    if socketPath != "" {
        // Remove socket file (ignore errors if it doesn't exist)
        if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
            // Log warning but don't fail the stop operation
            log.Printf("Warning: failed to remove socket file '%s': %v", socketPath, err)
        }
    }
}
```

**Notes**:
- Cleanup before start handles cases where previous process crashed without cleanup
- Cleanup after stop ensures no stale socket files remain
- Errors during cleanup are logged but don't fail the operations
- `os.IsNotExist` check prevents logging when file already doesn't exist

### 7. Testing Tool Updates

#### 7.1 Update simple-responder for Unix Socket Support

**Location**: `cmd/simple-responder/simple-responder.go`

The `simple-responder` tool is used for testing. It should support Unix sockets to enable testing of the `${UNIX}` macro functionality.

**Implementation required**:

1. **Add `-unix` flag** - Accept Unix socket path as command-line argument
2. **Override behavior** - If `-unix` is specified, it overrides `-port` (no error, no mutual exclusion check)
3. **Unix socket listener** - Use `net.Listen("unix", socketPath)` when `-unix` is provided
4. **Socket cleanup** - Remove socket file on shutdown

**Code changes**:

```go
// Add new flag after line 21
unixSocket := flag.String("unix", "", "unix socket path to listen on (overrides -port)")

// After flag.Parse() (after line 31), no validation needed
// -unix will override -port if specified

// Replace the server startup section (lines 272-296) with:
var listener net.Listener
var err error
var address string

if *unixSocket != "" {
    // Unix socket mode (overrides -port)
    address = *unixSocket

    // Remove existing socket file if it exists
    os.Remove(*unixSocket)

    listener, err = net.Listen("unix", *unixSocket)
    if err != nil {
        log.Fatalf("Failed to listen on Unix socket %s: %v", *unixSocket, err)
    }

    // Ensure socket file is cleaned up on exit
    defer os.Remove(*unixSocket)

} else {
    // TCP mode (default)
    address = "127.0.0.1:" + *port
    listener, err = net.Listen("tcp", address)
    if err != nil {
        log.Fatalf("Failed to listen on TCP %s: %v", address, err)
    }
}

srv := &http.Server{
    Handler: r.Handler(),
}

// ... rest of the code (logging, goroutine) ...

go func() {
    log.Printf("simple-responder listening on %s\n", address)
    if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
        log.Fatalf("simple-responder err: %s\n", err)
    }
}()
```

**Usage examples**:

```bash
# TCP mode (existing behavior)
simple-responder -port 8080

# Unix socket mode (new, overrides default port)
simple-responder -unix /tmp/test.sock

# Unix socket overrides explicit port
simple-responder -port 8080 -unix /tmp/test.sock
# Listens on /tmp/test.sock (not port 8080)
```

**Testing notes**:

- This enables testing `${UNIX}` macro with simple-responder in integration tests
- Socket file is automatically cleaned up on graceful shutdown
- Compatible with existing TCP-based tests (no breaking changes)

### 8. Documentation Updates

#### 8.1 config.example.yaml Updates

**Location**: `config.example.yaml`

Add documentation in two places:

**A. Top-level socketPath setting** (after `startPort` around line 36):

```yaml
# socketPath: directory path where Unix domain socket files are created
# - optional, default: /tmp/llama-swap-sockets (POSIX) or empty (Windows)
# - only applicable for models using the ${UNIX} macro
# - the ${UNIX} macro can be used in model.cmd and model.proxy settings
# - automatically generates unique socket paths like: {socketPath}/{model-id}.sock
# - only supported on POSIX systems (Linux, macOS, BSD) - not available on Windows
# - useful for improved performance and security over TCP sockets
socketPath: /tmp/llama-swap-sockets
```

**B. Model example using ${UNIX}** (add new model example after line 195):

```yaml
# Unix domain socket example (POSIX only):
# Unix sockets provide better performance and security than TCP for local communication
"llama-unix":
  cmd: |
    llama-server --unix-socket ${UNIX}
    --model /path/to/model.gguf
    --ctx-size 4096

  # proxy is automatically set to the unix socket path
  # you can also explicitly set it if needed:
  # proxy: unix:///tmp/llama-swap-sockets/llama-unix.sock

  # Note: ${UNIX} and ${PORT} cannot be used together in the same model
```

**C. Update macros section** (around line 45):
Update the reserved macro list:

```yaml
# - macro names must not be a reserved name: PORT, UNIX, or MODEL_ID
```

## Testing Plan

### Test Structure

Tests should follow existing patterns in:

- `proxy/config/config_test.go` - Cross-platform tests
- `proxy/config/config_posix_test.go` - POSIX-specific tests
- `proxy/config/config_windows_test.go` - Windows-specific tests

### 9.1 POSIX-Specific Tests (config_posix_test.go)

#### Test: Basic Unix socket allocation

```go
func TestUnixSocketAllocation(t *testing.T)
```

- Model with `${UNIX}` in cmd gets socket path allocated
- Socket path follows pattern: `{socketPath}/{model-id}.sock`
- Proxy automatically set to unix socket URL

#### Test: Path sanitization

```go
func TestUnixSocketPathSanitization(t *testing.T)
```

Test cases:

- `"my_model"` → `my_model.sock` (underscores preserved)
- `"//my/model////"` → `my-model.sock`
- `"model:v1"` → `model-v1.sock`
- `"my model"` → `my-model.sock`
- `"////"` → `default.sock`
- Model with special chars: `"test<>?*\"model"` → `test-model.sock`

#### Test: Mutual exclusion PORT/UNIX

```go
func TestUnixAndPortMutualExclusion(t *testing.T)
```

- Model using both `${PORT}` and `${UNIX}` returns error
- Error message: "cannot use both ${PORT} and ${UNIX} macros"

#### Test: Unix in proxy but not in cmd

```go
func TestUnixProxyWithoutCmd(t *testing.T)
```

- Similar to PORT validation test
- Proxy has `${UNIX}`, cmd doesn't → error
- Error: "proxy uses ${UNIX} but cmd does not"

#### Test: Socket path collision detection

```go
func TestUnixSocketPathCollisions(t *testing.T)
```

- Two models with IDs that sanitize to the same name
- Example: `"my/model"` and `"my-model"` both sanitize to `"my-model"`
- One model gets `my-model.sock`, the other gets `my-model-1.sock`
- A third collision would get `my-model-2.sock`, etc.
- Test should verify all models get unique socket paths (exact allocation order doesn't matter)

#### Test: Unix socket path too long

```go
func TestUnixSocketPathTooLong(t *testing.T)
```

- Model ID that creates >96 char socket path returns error
- Error includes the actual path and limit (96 chars)

#### Test: Multiple models get unique socket paths

```go
func TestMultipleUnixSocketModels(t *testing.T)
```

- Three models all using `${UNIX}` each get unique paths
- Paths include sanitized model IDs

#### Test: Unix macro in metadata

```go
func TestUnixMacroInMetadata(t *testing.T)
```

- `${UNIX}` substitution works in metadata (remains string type)
- Follows existing metadata substitution patterns

### 7.2 Windows-Specific Tests (config_windows_test.go)

#### Test: Unix macro rejected on Windows

```go
func TestUnixSocketNotSupportedOnWindows(t *testing.T)
```

- Model with `${UNIX}` in cmd returns error
- Model with `${UNIX}` in proxy returns error
- Error message: "uses ${UNIX} which is not supported on Windows"

### 9.3 Cross-Platform Tests (config_test.go)

#### Test: Unix is reserved macro name

```go
func TestUnixReservedMacroName(t *testing.T)
```

- User-defined macro named "UNIX" returns error
- Error: "UNIX is a reserved macro name"

#### Test: Unix macro with custom socketPath

```go
func TestCustomSocketPath(t *testing.T)
```

- Config with `socketPath: /custom/path` uses that path
- Generated socket: `/custom/path/{model-id}.sock`

#### Test: Unix macro without socketPath uses default

```go
func TestDefaultSocketPath(t *testing.T)
```

- Config without `socketPath` uses platform default
- Verify default is `/tmp/llama-swap-sockets` on POSIX

#### Test: Relative socketPath rejected

```go
func TestRelativeSocketPathRejected(t *testing.T)
```

- Config with relative socketPath (e.g., `./sockets`) returns error during initialization
- Error: "socketPath must be an absolute path, got: ./sockets"
- Test only when model uses `${UNIX}` (relative path ignored if ${UNIX} not used)

#### Test: socketPath not writable

```go
func TestSocketPathNotWritable(t *testing.T)
```

- Config with socketPath pointing to read-only directory returns error
- Error includes "is not writable"
- Test only when model uses `${UNIX}`

#### Test: socketPath set on Windows without ${UNIX} usage

```go
func TestSocketPathOnWindowsWithoutUnixMacro(t *testing.T)
```

- Config with `socketPath` set on Windows but no models using `${UNIX}` loads successfully
- socketPath is ignored when not used

### 9.4 Integration Tests

#### Test: Socket directory creation

```go
func TestSocketDirectoryCreation(t *testing.T)
```

- Loading config with `${UNIX}` macro creates socketPath directory
- Directory has correct permissions (0755)
- Handles already existing directory gracefully

#### Test: Unknown macro detection still works

```go
func TestUnknownMacroWithUnix(t *testing.T)
```

- Model with `${UNIX}` and `${UNKNOWN}` still catches unknown macro
- Ensures new UNIX logic doesn't break existing validation

#### Test: End-to-end Unix socket communication (POSIX only)

```go
func TestUnixSocketEndToEnd(t *testing.T)
```

- Start simple-responder with `-unix /tmp/test.sock`
- Configure llama-swap with model using `${UNIX}` macro
- Send HTTP request through llama-swap
- Verify request reaches simple-responder via Unix socket
- Verify response is returned correctly
- Clean up socket files

## Checklist

### Configuration Structure

- [ ] Add `SocketPath string` field to `Config` struct in `proxy/config/config.go`
- [ ] Add `socketPath` to YAML struct tags
- [ ] Set default value for `SocketPath` in `LoadConfigFromReader()` (hard-coded to `/tmp/llama-swap-sockets` on POSIX, empty on Windows)

### Path Sanitization

- [ ] Implement `sanitizeModelIDForPath(modelID string) string` function in `proxy/config/config.go`
- [ ] Handle invalid path characters: `/`, `\`, `:`, `*`, `?`, `"`, `<`, `>`, `|`, null, whitespace
- [ ] Collapse consecutive dashes and trim leading/trailing dashes
- [ ] Return `"default"` for empty results
- [ ] Write unit tests for sanitization function (various test cases)

### Macro Substitution Logic

- [ ] Add UNIX macro detection logic in `LoadConfigFromReader()` (check cmd and proxy)
- [ ] Create tracking map for allocated socket paths (to detect collisions)
- [ ] Implement mutual exclusion check (error if both `${PORT}` and `${UNIX}` used)
- [ ] Implement platform check (error if `${UNIX}` used on Windows)
- [ ] Implement proxy consistency check (error if `${UNIX}` in proxy but not in cmd)
- [ ] Implement socket path generation using `filepath.Join(config.SocketPath, sanitizedName + ".sock")`
- [ ] Implement collision detection: if path exists in tracking map, append `-1`, `-2`, etc.
- [ ] Add generated socket path to tracking map after allocation
- [ ] Implement socket path length validation (max 96 chars for portability)
- [ ] Create `MacroEntry{Name: "UNIX", Value: socketPath}` and substitute in all fields
- [ ] Auto-set proxy logic: if proxy is blank/empty, set to `unix://{socketPath}` for ${UNIX} or `http://localhost:${PORT}` for ${PORT}
- [ ] If proxy is not blank, leave it as-is for normal macro substitution

### Reserved Macro Updates

- [ ] Add "UNIX" to reserved macro names validation
- [ ] Update error message to include UNIX in reserved names list

### Configuration Validation and Initialization

- [ ] Implement default socketPath setting: `/tmp/llama-swap-sockets` on POSIX, empty on Windows (hard-coded in `LoadConfigFromReader()`)
- [ ] Implement socketPath absolute path validation (error if relative path when ${UNIX} is used)
- [ ] Implement socket directory creation with `os.MkdirAll(config.SocketPath, 0755)` (only when ${UNIX} is used)
- [ ] Implement socketPath writability check using test file creation (only when ${UNIX} is used)
- [ ] Add error handling if directory creation or write test fails
- [ ] Ensure validation only runs when at least one model uses ${UNIX}

### Testing Tool Updates (simple-responder)

- [ ] Add `-unix` flag to `cmd/simple-responder/simple-responder.go` for Unix socket path
- [ ] Implement override behavior: `-unix` overrides `-port` (no mutual exclusion error needed)
- [ ] Implement Unix socket listener using `net.Listen("unix", socketPath)`
- [ ] Remove existing socket file before listening (handle if doesn't exist)
- [ ] Add `defer os.Remove(socketPath)` to clean up socket file on shutdown
- [ ] Update server to use `srv.Serve(listener)` instead of `srv.ListenAndServe()`
- [ ] Test simple-responder with `-unix /tmp/test.sock` flag
- [ ] Verify `-unix` overrides `-port` when both are specified

### HTTP Client Unix Socket Support

- [ ] Implement Unix socket detection in `NewProcess()` (check if `proxyURL.Scheme == "unix"`)
- [ ] Extract socket path from URL (`socketPath := proxyURL.Path`)
- [ ] Validate socket path is not empty (return error if empty)
- [ ] Create custom `http.Transport` with Unix socket `DialContext` function
- [ ] Set `reverseProxy.Transport` to the custom transport for Unix socket URLs
- [ ] Test HTTP requests over Unix sockets work correctly with reverse proxy

### Socket File Lifecycle Management

- [ ] Implement socket file cleanup before start in `Process.Start()` in `proxy/process.go`
- [ ] Extract socket path from proxy URL when `proxyURL.Scheme == "unix"`
- [ ] Use `os.Remove(socketPath)` to delete stale socket file before starting (ignore errors if doesn't exist)
- [ ] Implement socket file cleanup after stop in `Process.Stop()` in `proxy/process.go`
- [ ] Use `os.Remove(socketPath)` to delete the socket file after stopping
- [ ] Handle cases where socket file doesn't exist gracefully (check `os.IsNotExist`)
- [ ] Log warnings for cleanup failures but don't fail the stop operation

### POSIX Tests (config_posix_test.go)

- [ ] Test: Basic Unix socket allocation (`TestUnixSocketAllocation`)
- [ ] Test: Path sanitization with various inputs (`TestUnixSocketPathSanitization`)
- [ ] Test: Mutual exclusion PORT/UNIX (`TestUnixAndPortMutualExclusion`)
- [ ] Test: Unix in proxy but not in cmd (`TestUnixProxyWithoutCmd`)
- [ ] Test: Socket path collision detection (`TestUnixSocketPathCollisions`)
- [ ] Test: Unix socket path too long - max 96 chars (`TestUnixSocketPathTooLong`)
- [ ] Test: Multiple models get unique socket paths (`TestMultipleUnixSocketModels`)
- [ ] Test: Unix macro in metadata (`TestUnixMacroInMetadata`)

### Windows Tests (config_windows_test.go)

- [ ] Test: Unix macro rejected on Windows (`TestUnixSocketNotSupportedOnWindows`)
- [ ] Test: Error when `${UNIX}` in cmd on Windows
- [ ] Test: Error when `${UNIX}` in proxy on Windows

### Cross-Platform Tests (config_test.go)

- [ ] Test: Unix is reserved macro name (`TestUnixReservedMacroName`)
- [ ] Test: Unix macro with custom socketPath (`TestCustomSocketPath`)
- [ ] Test: Unix macro without socketPath uses default (`TestDefaultSocketPath`)
- [ ] Test: Relative socketPath rejected (`TestRelativeSocketPathRejected`)
- [ ] Test: socketPath not writable (`TestSocketPathNotWritable`)
- [ ] Test: socketPath set on Windows without ${UNIX} usage (`TestSocketPathOnWindowsWithoutUnixMacro`)
- [ ] Test: Socket directory creation (`TestSocketDirectoryCreation`)
- [ ] Test: Unknown macro detection still works (`TestUnknownMacroWithUnix`)

### Integration Tests (POSIX only)

- [ ] Test: End-to-end Unix socket communication (`TestUnixSocketEndToEnd`)
- [ ] Use simple-responder with `-unix` flag in test
- [ ] Verify HTTP requests work through Unix socket via llama-swap reverse proxy
- [ ] Verify socket file cleanup after test

### Documentation

- [ ] Add `socketPath` documentation to `config.example.yaml` (after `startPort` section)
- [ ] Add Unix domain socket model example to `config.example.yaml`
- [ ] Update macros section to include UNIX in reserved names list
- [ ] Add usage notes about POSIX-only support and PORT/UNIX mutual exclusion

### Final Testing & Validation

- [ ] Run `make test-dev` and fix any static checking errors
- [ ] Run `make test-all` to verify all tests pass including long-running tests
