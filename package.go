package diagnostics

import (
	"fmt"

	"github.com/tsic404/kos-sdk/packaging"
)

// Build packs the product into the extension artifact KOSD-C-17 distributes:
// the declaration, the UI assets the caller supplies, and the digest covering
// both (SDK-C-4). It is the one packing path every author uses; the packer
// reads no product identity, so a first-party system application and a
// third-party extension produce the same structure.
//
// The product declares no UI assets: both its page and its export entry are
// server-side projections. A caller that supplies none therefore gets a package
// of exactly two entries, the manifest and its digest, and no asset is invented
// to fill the structure (SDK-C-4).
//
// A declaration the SDK rejects yields an error and no archive.
func Build(assets []packaging.Asset) (packaging.Package, []byte, error) {
	manifest, err := Manifest()
	if err != nil {
		return packaging.Package{}, nil, err
	}
	pkg, archive, err := packaging.Pack(manifest, assets)
	if err != nil {
		return packaging.Package{}, nil, fmt.Errorf("pack kos-diagnostics: %w", err)
	}
	return pkg, archive, nil
}
