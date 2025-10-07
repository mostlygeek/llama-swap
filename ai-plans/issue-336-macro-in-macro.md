# Improve macro-in-macro support

**Status: COMPLETED ✅**

## Title

Fix macro substitution ordering by preserving definition order using ordered YAML parsing

## Overview

The current macro implementation uses `map[string]any` which does not preserve insertion order. This causes issues when macros reference other macros - if macro `B` contains `${A}` but `B` is processed before `A`, the reference won't be substituted, leading to "unknown macro" errors.

**Goal:** Ensure macros are substituted in definition order (LIFO - last in, first out) to allow macros to reliably reference previously-defined macros.

**Outcomes:**
- Macros can reference other macros defined earlier in the config
- Macro substitution is deterministic and order-dependent
- Single-pass substitution prevents circular dependencies
- Use `yaml.Node` from `gopkg.in/yaml.v3` to preserve macro definition order
- All existing tests pass
- New tests validate substitution order and self-reference detection

## Design Requirements

### 1. YAML Parsing Strategy
- **Continue using:** `gopkg.in/yaml.v3` (current library)
- **Use:** `yaml.Node` for ordered parsing of macros
- **Reason:** `yaml.Node` preserves document structure and order, avoiding need for migration

### 2. Data Structure Changes

#### Current Implementation (config.go:19)
```go
type MacroList map[string]any
```

#### New Implementation
```go
type MacroList []MacroEntry

type MacroEntry struct {
    Name  string
    Value any
}
```

**Implementation Note:** Parse macros using `yaml.Node` to extract key-value pairs in document order, then construct the ordered `MacroList`.

### 3. Macro Substitution Order Rules

The substitution must follow this hierarchy (from most specific to least):

1. **Reserved macros** (last): `PORT`, `MODEL_ID` - substituted last, highest priority
2. **Model-level macros** (middle): Defined in specific model config, overrides global
3. **Global macros** (first): Defined at config root level

Within each level, macros are substituted in **reverse definition order** (LIFO):
- The last macro defined is substituted first
- This allows later macros to reference earlier ones
- Single-pass substitution prevents circular dependencies

### 4. Macro Reference Rules

**Allowed:**
- Macro can reference any macro defined **before** it (earlier in the file)
- Model macros can reference global macros
- Macros can reference reserved macros (`${PORT}`, `${MODEL_ID}`)

**Prohibited:**
- Macro cannot reference itself (e.g., `foo: "value ${foo}"`)
- Macro cannot reference macros defined **after** it
- No circular references (prevented by single-pass, ordered substitution)

### 5. Validation Requirements

Add validation to detect:
- **Self-references:** Macro value contains reference to its own name
- **Unknown macros:** After substitution, any remaining `${...}` references

Error messages should be clear:
```
macro 'foo' contains self-reference
unknown macro '${bar}' in model.cmd
```

### 6. Implementation Changes

#### Files to Modify

1. **[proxy/config/config.go](proxy/config/config.go)**
   - Line 19: Change `MacroList` type definition
   - Line 69: Update `Macros MacroList` field
   - Line 153-157: Update macro validation loop to work with ordered structure
   - Line 175-188: Update model-level macro validation
   - Line 181-188: **NEW** Implement proper macro merging respecting order
   - Line 193-202: **NEW** Implement ordered macro substitution in LIFO order
   - Line 389-415: Update `validateMacro` to detect self-references
   - Line 420-475: Update `substituteMetadataMacros` to accept ordered MacroList

2. **[proxy/config/model_config.go](proxy/config/model_config.go)**
   - Line 33: Update `Macros MacroList` field type

3. **All test files**
   - Update test fixtures to use ordered macro definitions
   - Ensure tests specify macro order explicitly

#### Core Algorithm

Replace the macro substitution logic in [config.go:181-252](proxy/config/config.go#L181-L252) with:

```go
// Merge global config and model macros. Model macros take precedence
mergedMacros := make(MacroList, 0, len(config.Macros)+len(modelConfig.Macros)+2)

// Add global macros first
for _, entry := range config.Macros {
	mergedMacros = append(mergedMacros, entry)
}

// Add model macros (can override global)
for _, entry := range modelConfig.Macros {
	// Remove any existing global macro with same name
	found := false
	for i, existing := range mergedMacros {
		if existing.Name == entry.Name {
			mergedMacros[i] = entry // Override
			found = true
			break
		}
	}
	if !found {
		mergedMacros = append(mergedMacros, entry)
	}
}

// Add reserved MODEL_ID macro at the end
mergedMacros = append(mergedMacros, MacroEntry{Name: "MODEL_ID", Value: modelId})

// Check if PORT macro is needed
if strings.Contains(modelConfig.Cmd, "${PORT}") || strings.Contains(modelConfig.Proxy, "${PORT}") || strings.Contains(modelConfig.CmdStop, "${PORT}") {
	// enforce ${PORT} used in both cmd and proxy
	if !strings.Contains(modelConfig.Cmd, "${PORT}") && strings.Contains(modelConfig.Proxy, "${PORT}") {
		return Config{}, fmt.Errorf("model %s: proxy uses ${PORT} but cmd does not - ${PORT} is only available when used in cmd", modelId)
	}

	// Add PORT macro to the end (highest priority)
	mergedMacros = append(mergedMacros, MacroEntry{Name: "PORT", Value: nextPort})
	nextPort++
}

// Single-pass substitution: Substitute all macros in LIFO order (last defined first)
// This allows later macros to reference earlier ones
for i := len(mergedMacros) - 1; i >= 0; i-- {
	entry := mergedMacros[i]
	macroSlug := fmt.Sprintf("${%s}", entry.Name)
	macroStr := fmt.Sprintf("%v", entry.Value)

	// Substitute in command fields
	modelConfig.Cmd = strings.ReplaceAll(modelConfig.Cmd, macroSlug, macroStr)
	modelConfig.CmdStop = strings.ReplaceAll(modelConfig.CmdStop, macroSlug, macroStr)
	modelConfig.Proxy = strings.ReplaceAll(modelConfig.Proxy, macroSlug, macroStr)
	modelConfig.CheckEndpoint = strings.ReplaceAll(modelConfig.CheckEndpoint, macroSlug, macroStr)
	modelConfig.Filters.StripParams = strings.ReplaceAll(modelConfig.Filters.StripParams, macroSlug, macroStr)

	// Substitute in metadata (recursive)
	if len(modelConfig.Metadata) > 0 {
		var err error
		modelConfig.Metadata, err = substituteMacroInValue(modelConfig.Metadata, entry.Name, entry.Value)
		if err != nil {
			return Config{}, fmt.Errorf("model %s metadata: %s", modelId, err.Error())
		}
	}
}
```

Add this new helper function to replace `substituteMetadataMacros`:

```go
// substituteMacroInValue recursively substitutes a single macro in a value structure
// This is called once per macro, allowing LIFO substitution order
func substituteMacroInValue(value any, macroName string, macroValue any) (any, error) {
	macroSlug := fmt.Sprintf("${%s}", macroName)
	macroStr := fmt.Sprintf("%v", macroValue)

	switch v := value.(type) {
	case string:
		// Check if this is a direct macro substitution
		if v == macroSlug {
			return macroValue, nil
		}
		// Handle string interpolation
		if strings.Contains(v, macroSlug) {
			return strings.ReplaceAll(v, macroSlug, macroStr), nil
		}
		return v, nil

	case map[string]any:
		// Recursively process map values
		newMap := make(map[string]any)
		for key, val := range v {
			newVal, err := substituteMacroInValue(val, macroName, macroValue)
			if err != nil {
				return nil, err
			}
			newMap[key] = newVal
		}
		return newMap, nil

	case []any:
		// Recursively process slice elements
		newSlice := make([]any, len(v))
		for i, val := range v {
			newVal, err := substituteMacroInValue(val, macroName, macroValue)
			if err != nil {
				return nil, err
			}
			newSlice[i] = newVal
		}
		return newSlice, nil

	default:
		// Return scalar types as-is
		return value, nil
	}
}
```

### 7. Self-Reference Detection

Add to `validateMacro` function:

```go
func validateMacro(name string, value any) error {
    // ... existing validation ...

    // Check for self-reference
    if str, ok := value.(string); ok {
        macroSlug := fmt.Sprintf("${%s}", name)
        if strings.Contains(str, macroSlug) {
            return fmt.Errorf("macro '%s' contains self-reference", name)
        }
    }

    return nil
}
```

## Testing Plan

### 1. Migration Tests
- **Test:** All existing macro tests still pass after YAML library migration
- **Files:** All `*_test.go` files with macro tests

### 2. Macro Order Tests

#### Test: Macro-in-macro substitution order
```yaml
macros:
  "A": "value-A"
  "B": "prefix-${A}-suffix"

models:
  test:
    cmd: "echo ${B}"
```
**Expected:** `cmd` becomes `"echo prefix-value-A-suffix"`

#### Test: LIFO substitution order
```yaml
macros:
  "base": "/models"
  "path": "${base}/llama"
  "full": "${path}/model.gguf"

models:
  test:
    cmd: "load ${full}"
```
**Expected:** `cmd` becomes `"load /models/llama/model.gguf"`

#### Test: Model macro overrides global
```yaml
macros:
  "tag": "global"
  "msg": "value-${tag}"

models:
  test:
    macros:
      "tag": "model-level"
    cmd: "echo ${msg}"
```
**Expected:** `cmd` becomes `"echo value-model-level"` (model macro overrides global)

### 3. Reserved Macro Tests

#### Test: MODEL_ID substituted in macro
```yaml
macros:
  "podman-llama": "podman run --name ${MODEL_ID} ghcr.io/ggml-org/llama.cpp:server-cuda"

models:
  my-model:
    cmd: "${podman-llama} -m model.gguf"
```
**Expected:** `cmd` becomes `"podman run --name my-model ghcr.io/ggml-org/llama.cpp:server-cuda -m model.gguf"`

### 4. Error Detection Tests

#### Test: Self-reference detection
```yaml
macros:
  "recursive": "value-${recursive}"
```
**Expected:** Error: `macro 'recursive' contains self-reference`

#### Test: Undefined macro reference
```yaml
macros:
  "A": "value-${UNDEFINED}"
```
**Expected:** Error: `unknown macro '${UNDEFINED}' found in macros.A` (or similar)

### 5. Regression Tests
- Run all existing macro tests: `TestConfig_MacroReplacement`, `TestConfig_MacroReservedNames`, etc.
- Ensure all pass without modification (except test fixtures if needed)

## Checklist

### Phase 1: Data Structure Changes
- [ ] Implement custom `UnmarshalYAML` method for `MacroList` that uses `yaml.Node`
- [ ] Define new ordered `MacroList` type as `[]MacroEntry`
- [ ] Update `MacroList` type definition in [config.go](proxy/config/config.go#L19)
- [ ] Update `Config.Macros` field type in [config.go](proxy/config/config.go#L69)
- [ ] Update `ModelConfig.Macros` field type in [model_config.go](proxy/config/model_config.go#L33)
- [ ] Implement helper functions:
  - [ ] `func (ml MacroList) Get(name string) (any, bool)` - lookup by name
  - [ ] `func (ml MacroList) Set(name string, value any) MacroList` - add/override entry
  - [ ] `func (ml MacroList) ToMap() map[string]any` - convert to map if needed

### Phase 2: Macro Validation Updates
- [ ] Update macro validation loop at [config.go:153-157](proxy/config/config.go#L153-L157)
- [ ] Update model macro validation at [config.go:175-179](proxy/config/config.go#L175-L179)
- [ ] Add self-reference detection to `validateMacro` function [config.go:389](proxy/config/config.go#L389)
- [ ] Test self-reference detection with new test case

### Phase 3: Macro Substitution Algorithm
- [ ] Implement ordered macro merging (global → model → reserved) at [config.go:181-188](proxy/config/config.go#L181-L188)
- [ ] Implement single-pass LIFO substitution loop (reverse iteration) at [config.go:193-202](proxy/config/config.go#L193-L202)
  - [ ] Substitute in all string fields (cmd, cmdStop, proxy, checkEndpoint, stripParams)
  - [ ] Substitute in metadata within same loop
- [ ] Ensure `MODEL_ID` is added to merged macros before substitution
- [ ] Ensure `PORT` is added after port assignment (if needed)
- [ ] Replace `substituteMetadataMacros` with new `substituteMacroInValue` function that processes one macro at a time [config.go:420](proxy/config/config.go#L420)
- [ ] Remove old metadata substitution code that was separate from main loop [config.go:245-251](proxy/config/config.go#L245-L251)

### Phase 4: Testing
- [ ] Run `make test-dev` - fix any static checking errors
- [ ] Add test: macro-in-macro basic substitution
- [ ] Add test: LIFO substitution order with 3+ macro levels
- [ ] Add test: MODEL_ID in global macro used by model
- [ ] Add test: PORT in global macro used by model
- [ ] Add test: model macro overrides global macro in substitution
- [ ] Add test: self-reference detection error
- [ ] Add test: undefined macro reference error
- [ ] Verify all existing macro tests pass: `TestConfig_Macro*`
- [ ] Run `make test-all` - ensure all tests including concurrency tests pass

### Phase 5: Documentation
- [ ] Update plan status in this file (mark completed)
- [ ] Update CLAUDE.md if macro behavior needs documentation
- [ ] Verify no new error messages need user documentation

## Bug Example (Original Issue)

```yaml
macros:
  "podman-llama": >
    podman run --name ${MODEL_ID}
    --init --rm -p ${PORT}:8080 -v /home/alex/ai/models:/models:z --gpus=all
    ghcr.io/ggml-org/llama.cpp:server-cuda

  "standard-options": >
    --no-mmap --jinja

  "kv8": >
    -fa on -ctk q8_0 -ctv q8_0
```

**Current Bug:**
- During macro substitution, if `${MODEL_ID}` is processed before `${podman-llama}`, the `${MODEL_ID}` reference inside `podman-llama` remains unsubstituted
- Results in error: `unknown macro '${MODEL_ID}' found in model.cmd`

**After Fix:**
- Macros substituted in LIFO order: `kv8` → `standard-options` → `podman-llama`
- `MODEL_ID` is a reserved macro, substituted last (after all user macros)
- `${MODEL_ID}` inside `podman-llama` is correctly replaced with the model name
