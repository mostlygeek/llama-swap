// Package matrix compiles and solves the matrix routing DSL without
// materializing every combination described by an expression.
//
// # Model
//
// A DSL expression describes a family of model sets. A leaf contributes one
// model, OR selects an alternative family, and AND takes the union of one set
// from each child family. A named reference points at another expression.
//
// Matrix sets have subset semantics: selecting a set does not start every model
// named by that set. It only says those models are allowed to coexist. This is
// why a candidate containing more currently running models is always at least
// as useful as one containing fewer of them.
//
// # Compilation
//
// Compile parses each named expression once, resolves aliases to real model
// names, validates references, rejects cycles, and links references into an
// immutable AST/DAG. It preserves the existing deterministic topological and
// expression order used for tie-breaking.
//
// Compilation deliberately does not distribute AND over OR. For example, ten
// groups containing ten alternatives each remain a small expression graph
// instead of becoming ten billion stored combinations.
//
// # Runtime solving
//
// Solve only needs to distinguish the requested model and the models that are
// currently running. It assigns those relevant models query-local bit
// positions and projects every DSL choice onto that dynamic bitset:
//
//   - a relevant leaf sets its bit; an irrelevant leaf produces an empty mask;
//   - OR merges the projected states from its children;
//   - AND combines child states with bitwise union;
//   - a reference reuses the states computed for its linked expression.
//
// Evaluation is memoized per AST node for the duration of one query. States
// with identical relevant masks collapse to the first witness. A state whose
// mask is a subset of another state is also discarded: eviction costs are
// positive, so the superset can retain at least as much running work and cannot
// produce a worse decision.
//
// The solver then considers states containing the requested model and selects
// the one with the lowest cost for running models not present in the mask.
// Equal-cost choices retain traversal order. Each state carries a compact
// witness tree so the selected full model set can be reconstructed for logging
// without expanding all other choices.
//
// CanContainAll uses the same projection machinery for configuration checks
// such as selector coexistence. Instead of optimizing eviction cost, it asks
// whether any projected state contains every required bit.
//
// # Complexity and concurrency
//
// Runtime work depends on the number of distinct projected states for the
// relevant models, not on the expression's theoretical Cartesian expansion.
// The worst case can still be exponential when many relevant alternatives are
// mutually incomparable, but there is no fixed expansion or model-count limit.
//
// Program is immutable after Compile. Solve and CanContainAll allocate their
// evaluators per call, so a Program is safe for concurrent reads without locks
// or cache invalidation.
package matrix
