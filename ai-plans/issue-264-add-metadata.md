# Add Model Metadata Support with Typed Macros

## Overview

Implement support for arbitrary metadata on model configurations that can be exposed through the `/v1/models` API endpoint. This feature extends the existing macro system to support scalar types (string, int, float, bool) instead of only strings, enabling type-safe metadata values.

The metadata will be schemaless, allowing users to define any key-value pairs they need. Macro substitution will work within metadata values, preserving types when macros are used directly and converting to strings when macros are interpolated within strings.

## Design Requirements

### 1. Enhanced Macro System

**Current State:**

- Macros are defined as `map[string]string` at both global and model levels
- Only string substitution is supported
- Macros are replaced in: `cmd`, `cmdStop`, `proxy`, `checkEndpoint`, `filters.stripParams`

**Required Changes:**

- Change `MacroList` type from `map[string]string` to `map[string]any`
- Support scalar types: `string`, `int`, `float64`, `bool`
- Implement type-preserving macro substitution:
  - Direct macro usage (`key: ${macro}`) preserves the macro's type
  - Interpolated usage (`key: "text ${macro}"`) converts to string
- Add validation to ensure macro values are scalar types only
- Update existing macro substitution logic in [proxy/config/config.go](proxy/config/config.go) to handle `any` types

**Implementation Details:**

- Create a generic helper function to perform macro substitution that:
  - Takes a value of type `any`
  - Recursively processes maps, slices, and scalar values
  - Replaces `${macro_name}` patterns with macro values
  - Preserves types for direct substitution
  - Converts to strings for interpolated substitution
- Update `validateMacro()` function to accept `any` type and validate scalar types
- Maintain backward compatibility with existing string-only macros

### 2. Metadata Field in ModelConfig

**Location:** [proxy/config/model_config.go](proxy/config/model_config.go)

**Required Changes:**

- Add `Metadata map[string]any` field to `ModelConfig` struct
- Support YAML unmarshaling of arbitrary structures (maps, arrays, scalars)
- Apply macro substitution to metadata values during config loading

**Schema Requirements:**

- Metadata is optional (default: empty/nil map)
- Supports nested structures (objects within objects, arrays, etc.)
- All string values within metadata undergo macro substitution
- Type preservation rules apply as described above

### 3. Macro Substitution in Metadata

**Location:** [proxy/config/config.go](proxy/config/config.go) in `LoadConfigFromReader()`

**Process Flow:**

1. After loading YAML configuration
2. After model-level and global macro merging
3. Apply macro substitution to `ModelConfig.Metadata` field
4. Use the same merged macros available to `cmd`, `proxy`, etc.
5. Process recursively through all nested structures

**Substitution Rules:**

- `port: ${PORT}` → keeps integer type from PORT macro
- `temperature: ${temp}` → keeps float type from temp macro
- `note: "Running on ${PORT}"` → converts to string `"Running on 10001"`
- Arrays and nested objects are processed recursively
- Unknown macros should cause configuration load error (consistent with existing behavior)

### 4. API Response Updates

**Location:** [proxy/proxymanager.go:350](proxy/proxymanager.go#L350) `listModelsHandler()`

**Current Behavior:**

- Returns model records with: `id`, `object`, `created`, `owned_by`
- Optionally includes: `name`, `description`

**Required Changes:**

- Add metadata to each model record under the key `llamaswap_meta`
- Only include `llamaswap_meta` if metadata is non-empty
- Preserve all types when marshaling to JSON
- Maintain existing sorting by model ID

**Example Response:**

```json
{
  "object": "list",
  "data": [
    {
      "id": "llama",
      "object": "model",
      "created": 1234567890,
      "owned_by": "llama-swap",
      "name": "llama 3.1 8B",
      "description": "A small but capable model",
      "llamaswap_meta": {
        "port": 10001,
        "temperature": 0.7,
        "note": "The llama is running on port 10001 temp=0.7, context=16384",
        "a_list": [1, 1.23, "macros are OK in list and dictionary types: llama"],
        "an_obj": {
          "a": "1",
          "b": 2,
          "c": [0.7, false, "model: llama"]
        }
      }
    }
  ]
}
```

### 5. Validation and Error Handling

**Macro Validation:**

- Extend `validateMacro()` to accept values of type `any`
- Verify macro values are scalar types: `string`, `int`, `float64`, `bool`
- Reject complex types (maps, slices, structs) as macro values
- Maintain existing validation for macro names and lengths

**Configuration Loading:**

- Fail fast if unknown macros are found in metadata
- Provide clear error messages indicating which model and field contains errors
- Ensure macros in metadata follow same rules as macros in cmd/proxy fields

## Testing Plan

### Test 1: Model-Level Macros with Different Types

**File:** [proxy/config/model_config_test.go](proxy/config/model_config_test.go)

**Test Cases:**

- Define model with macros of each scalar type
- Verify metadata correctly substitutes and preserves types
- Test direct substitution (`port: ${PORT}`)
- Test string interpolation (`note: "Port is ${PORT}"`)
- Verify nested objects and arrays work correctly

### Test 2: Global and Model Macro Precedence

**File:** [proxy/config/config_test.go](proxy/config/config_test.go)

**Test Cases:**

- Define same macro at global and model level with different types
- Verify model-level macro takes precedence
- Test metadata uses correct macro value
- Verify type is preserved from the winning macro

### Test 3: Macro Validation

**File:** [proxy/config/config_test.go](proxy/config/config_test.go)

**Test Cases:**

- Test that complex types (maps, arrays) are rejected as macro values
  - Verify error message includes: macro name and type that was rejected
- Test that scalar types (string, int, float, bool) are accepted
  - Each type should load without error
- Test macro name validation still works with `any` types
  - Invalid characters, reserved names, length limits should still be enforced

### Test 4: Metadata in API Response

**File:** [proxy/proxymanager_test.go](proxy/proxymanager_test.go)

**Existing Test:** `TestProxyManager_ListModelsHandler`

**Test Cases:**

- Model with metadata → verify `llamaswap_meta` key appears
- Model without metadata → verify `llamaswap_meta` key is absent
- Verify all types are correctly marshaled to JSON
- Verify nested structures are preserved
- Verify macro substitution has occurred before serialization

### Test 5: Unknown Macros in Metadata

**File:** [proxy/config/config_test.go](proxy/config/config_test.go)

**Test Cases:**

- Use undefined macro in metadata
- Verify configuration loading fails with clear error
- Error should indicate model name and that macro is undefined

### Test 6: Recursive Substitution

**File:** [proxy/config/config_test.go](proxy/config/config_test.go)

**Test Cases:**

- Metadata with deeply nested structures
- Arrays containing objects with macros
- Objects containing arrays with macros
- Mixed string interpolation and direct substitution at various nesting levels

## Checklist

### Configuration Schema Changes

- [x] Change `MacroList` type from `map[string]string` to `map[string]any` in [proxy/config/config.go:19](proxy/config/config.go#L19)
- [x] Add `Metadata map[string]any` field to `ModelConfig` struct in [proxy/config/model_config.go:37](proxy/config/model_config.go#L37)
- [x] Update `validateMacro()` function signature to accept `any` type for values
- [x] Add validation logic to ensure macro values are scalar types only

### Macro Substitution Logic

- [x] Create generic recursive function `substituteMetadataMacros()` to handle `any` types
- [x] Implement type-preserving direct substitution logic
- [x] Implement string interpolation with type conversion
- [x] Handle maps: recursively process all values
- [x] Handle slices: recursively process all elements
- [x] Handle scalar types: perform string-based macro substitution if value is string
- [x] Integrate macro substitution into `LoadConfigFromReader()` after existing macro expansion
- [x] Update existing macro substitution calls to use merged macros with correct types

### API Response Changes

- [x] Modify `listModelsHandler()` in [proxy/proxymanager.go:350](proxy/proxymanager.go#L350)
- [x] Add `llamaswap_meta` field to model records when metadata exists
- [x] Ensure empty metadata results in omitted `llamaswap_meta` key
- [x] Verify JSON marshaling preserves all types correctly

### Testing - Config Package

- [x] Add test for string macros in metadata: [proxy/config/config_test.go](proxy/config/config_test.go)
- [x] Add test for int macros in metadata: [proxy/config/config_test.go](proxy/config/config_test.go)
- [x] Add test for float macros in metadata: [proxy/config/config_test.go](proxy/config/config_test.go)
- [x] Add test for bool macros in metadata: [proxy/config/config_test.go](proxy/config/config_test.go)
- [x] Add test for string interpolation in metadata: [proxy/config/config_test.go](proxy/config/config_test.go)
- [x] Add test for model-level macro precedence: [proxy/config/config_test.go](proxy/config/config_test.go)
- [x] Add test for nested structures in metadata: [proxy/config/config_test.go](proxy/config/config_test.go)
- [x] Add test for unknown macro in metadata (should error): [proxy/config/config_test.go](proxy/config/config_test.go)
- [x] Add test for invalid macro type validation: [proxy/config/config_test.go](proxy/config/config_test.go)

### Testing - Model Config Package

- [x] Add test cases to [proxy/config/model_config_test.go](proxy/config/model_config_test.go) for metadata unmarshaling
- [x] Test metadata with various scalar types
- [x] Test metadata with nested objects and arrays

### Testing - Proxy Manager

- [x] Update `TestProxyManager_ListModelsHandler` in [proxy/proxymanager_test.go](proxy/proxymanager_test.go)
- [x] Add test case for model with metadata
- [x] Add test case for model without metadata
- [x] Verify `llamaswap_meta` key presence/absence
- [x] Verify type preservation in JSON output
- [x] Verify macro substitution has occurred

### Documentation

- [x] Verify [config.example.yaml](config.example.yaml) already has complete metadata examples (lines 149-171)
- [x] No additional documentation needed per project instructions

## Known Issues and Considerations

### Inconsistencies

None identified. The plan references the correct existing example in [config.example.yaml:149-171](config.example.yaml#L149-L171).

### Design Decisions

1. **Why `llamaswap_meta` instead of merging into record?**

   - Avoids potential collisions with OpenAI API standard fields
   - Makes it clear this is llama-swap specific metadata
   - Easier for clients to distinguish standard vs. custom fields

2. **Why support nested structures?**

   - Provides maximum flexibility for users
   - Aligns with the schemaless design principle
   - Example config already demonstrates this capability

3. **Why validate macro types?**
   - Prevents confusing behavior (e.g., substituting a map)
   - Makes configuration errors explicit at load time
   - Simpler implementation and testing
