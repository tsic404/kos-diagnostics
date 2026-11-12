// Package diagnostics is the kos-diagnostics product: the diagnostics page and
// the diagnostic-bundle export entry.
//
// The page is a read-only projection. It shows the degradation reasons and the
// action that resolves each one, the stalled uninstalls with the objects that
// block them, the evidence chain of the reference path, and the bundle export
// entry. Every item names its source type and the source that resolves it, and
// a computed item also names the rule and the objects its value was computed
// from (KOSD-C-53).
//
// The evidence chain and the diagnostic bundle stay assembled by kosd
// (pkg/diagnostics, KOSD-C-26); this product reads them through the control
// plane and never copies their judgement. The page writes nothing: its only
// action is the export, which reads at request time (DIAG-C-1).
package diagnostics
