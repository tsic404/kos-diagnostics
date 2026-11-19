package diagnostics

import (
	"encoding/json"
	"fmt"
)

// Manifest returns the declaration in the closed field set KOSD-C-18 fixes and
// the author SDK validates (SDK-C-3). The blocks the contract requires are
// present even when this product declares nothing in them; no field is added
// and no missing value is filled in with a default.
//
// The contributions and permissions are rendered from the same Go declaration
// the kernel activates, so the manifest the package ships and the declaration
// the kernel loads cannot drift. apiGroup is absent because the product
// contributes no object class; the SDK keeps it optional exactly for that case
// (KOSD-C-19).
func Manifest() (map[string]any, error) {
	declaration := Declaration()
	pages, err := jsonValue(declaration.Contributions.Pages)
	if err != nil {
		return nil, err
	}
	permissions, err := jsonValue(declaration.Permissions)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"identifier":       declaration.Identifier,
		"version":          declaration.Version,
		"frameworkVersion": declaration.FrameworkVersion,
		"contributions":    map[string]any{"pages": pages},
		// The framework range above is the only edge this product has: it
		// depends on no other extension (KOSD-C-18).
		"dependencies": map[string]any{},
		"permissions":  permissions,
		// The product carries no configuration and exposes no status interface
		// of its own; an empty structure states exactly that (KOSD-C-18).
		"configuration": map[string]any{},
		// The page holds no data of its own, so uninstalling the component
		// keeps nothing behind (KOSD-C-18, KOSD-C-29).
		"retention": []any{},
	}, nil
}

// jsonValue renders one part of the declaration as the JSON the manifest
// carries, so the package ships the wire shape of the contract rather than a
// Go type's zero values.
func jsonValue(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode declaration: %w", err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, fmt.Errorf("decode declaration: %w", err)
	}
	return decoded, nil
}
