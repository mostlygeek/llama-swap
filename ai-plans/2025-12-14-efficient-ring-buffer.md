# Replace ring.Ring with Efficient Circular Byte Buffer

## Overview

Replace the inefficient `container/ring.Ring` implementation in `logMonitor.go` with a simple circular byte buffer that uses a single contiguous `[]byte` slice. This eliminates per-write allocations, improves cache locality, and correctly implements a 10KB buffer.

## Current Issues

1. `ring.New(10 * 1024)` creates 10,240 ring **elements**, not 10KB of storage
2. Every `Write()` call allocates a new `[]byte` slice inside the lock
3. `GetHistory()` iterates all 10,240 elements and appends repeatedly (geometric reallocs)
4. Linked list structure has poor cache locality and pointer overhead

## Design Requirements

### New CircularBuffer Type

Create a simple circular byte buffer with:
- Single pre-allocated `[]byte` of fixed capacity (10KB)
- `head` and `size` integers to track write position and data length
- No per-write allocations

### API Requirements

The new buffer must support:
1. **Write(p []byte)** - Append bytes, overwriting oldest data when full
2. **GetHistory() []byte** - Return all buffered data in correct order (oldest to newest)

### Implementation Details

```go
type circularBuffer struct {
    data []byte  // pre-allocated capacity
    head int     // next write position
    size int     // current number of bytes stored (0 to cap)
}
```

**Write logic:**
- If `len(p) >= capacity`: just keep the last `capacity` bytes
- Otherwise: write bytes at `head`, wrapping around if needed
- Update `head` and `size` accordingly
- Data is copied into the internal buffer (not stored by reference)

**GetHistory logic:**
- Calculate start position: `(head - size + cap) % cap`
- If not wrapped: single slice copy
- If wrapped: two copies (end of buffer + beginning)
- Returns a **new slice** (copy), not a view into internal buffer

### Immutability Guarantees (must preserve)

Per existing tests:
1. Modifying input `[]byte` after `Write()` must not affect stored data
2. `GetHistory()` returns independent copy - modifications don't affect buffer

## Files to Modify

- `proxy/logMonitor.go` - Replace `buffer *ring.Ring` with new circular buffer

## Testing Plan

Existing tests in `logMonitor_test.go` should continue to pass:
- `TestLogMonitor` - Basic write/read and subscriber notification
- `TestWrite_ImmutableBuffer` - Verify writes don't affect returned history
- `TestWrite_LogTimeFormat` - Timestamp formatting

Add new tests:
- Test buffer wrap-around behavior
- Test large writes that exceed buffer capacity
- Test exact capacity boundary conditions

## Checklist

- [ ] Create `circularBuffer` struct in `logMonitor.go`
- [ ] Implement `Write()` method for circular buffer
- [ ] Implement `GetHistory()` method for circular buffer
- [ ] Update `LogMonitor` struct to use new buffer
- [ ] Update `NewLogMonitorWriter()` to initialize new buffer
- [ ] Update `LogMonitor.Write()` to use new buffer
- [ ] Update `LogMonitor.GetHistory()` to use new buffer
- [ ] Remove `"container/ring"` import
- [ ] Run `make test-dev` to verify existing tests pass
- [ ] Add wrap-around test case
- [ ] Run `make test-all` for final validation
