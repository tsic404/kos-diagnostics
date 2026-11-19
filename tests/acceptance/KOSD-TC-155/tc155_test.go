// Package kosdtc155 verifies that kos-diagnostics ships as an independently
// installable extension package: the artifact carries the declaration in the
// closed field set the framework fixes and the author SDK validates, its
// digest covers the declaration and every asset the declaration names, and the
// packaged declaration is the one the kernel loads (KOSD-AC-72, KOSD-TC-155).
package kosdtc155

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/tsic404/kos-sdk/manifest"
	"github.com/tsic404/kos-sdk/packaging"

	"github.com/tsic404/kosd/pkg/extension"

	"github.com/tsic404/kos-diagnostics"
)

// TestTC155ManifestFields checks the declaration's field set against SDK-C-3:
// the manifest carries no field the framework does not define, omits none of
// the blocks KOSD-C-18 requires, and leaves apiGroup out because the product
// contributes no object class.
func TestTC155ManifestFields(t *testing.T) {
	pkg, archive, err := diagnostics.Build(nil)
	if err != nil {
		t.Fatalf("build the extension package: %v", err)
	}
	verified, err := packaging.Verify(archive, pkg.Digest)
	if err != nil {
		t.Fatalf("verify the built package: %v", err)
	}

	manifestFields := fieldSet(t, verified.Manifest)
	declared := map[string]bool{}
	for field := range manifestFields {
		declared[field] = true
	}
	closed := map[string]bool{}
	for _, field := range manifest.ExtensionManifestFields {
		closed[field] = true
	}
	for field := range declared {
		if !closed[field] {
			t.Fatalf("manifest carries field %q, which the framework does not define: %v", field, declared)
		}
	}
	for _, field := range []string{
		"identifier", "version", "frameworkVersion",
		"contributions", "dependencies", "permissions", "configuration", "retention",
	} {
		if !declared[field] {
			t.Fatalf("manifest omits %q: %v", field, declared)
		}
	}
	if declared["apiGroup"] {
		t.Fatal("manifest declares an apiGroup although the product contributes no object class")
	}

	// The blocks this product leaves empty state that it declares nothing in
	// them; they are not filled with invented defaults (SDK-C-3).
	if dependencies := verified.Manifest.Dependencies; len(dependencies.Capabilities) != 0 || len(dependencies.Components) != 0 {
		t.Fatalf("dependencies = %+v, want none outside the framework range", dependencies)
	}
	if len(verified.Manifest.Retention) != 0 {
		t.Fatalf("retention = %+v, want nothing to keep", verified.Manifest.Retention)
	}
	if verified.Manifest.Configuration.Schema != nil ||
		len(verified.Manifest.Configuration.StatusWriters) != 0 ||
		len(verified.Manifest.Configuration.Conditions) != 0 {
		t.Fatalf("configuration = %+v, want no configuration and no status interface", verified.Manifest.Configuration)
	}

	// Permissions declare only what the page reads and the role it needs; a
	// read-only page names no write scope and no object writer (KOSD-C-18).
	if len(verified.Manifest.Permissions) != 1 {
		t.Fatalf("permissions = %+v, want the page's read scope", verified.Manifest.Permissions)
	}
	permission := verified.Manifest.Permissions[0]
	if permission.Contribution != diagnostics.PageDiagnostics {
		t.Fatalf("permission contribution = %q, want %q", permission.Contribution, diagnostics.PageDiagnostics)
	}
	if permission.RequiredRole == "" {
		t.Fatal("permission names no control-plane role")
	}
	if len(permission.ReadScope.Kinds) == 0 {
		t.Fatal("permission names no read scope")
	}
	if len(permission.ObjectWriters) != 0 {
		t.Fatalf("permission names object writers %v, want a read-only page", permission.ObjectWriters)
	}
}

// TestTC155PackageEntriesMatchDeclaration checks SDK-C-4: the package carries
// exactly the declaration and the assets that declaration names. This product
// declares no UI asset, so the bare package is the manifest and its digest.
func TestTC155PackageEntriesMatchDeclaration(t *testing.T) {
	pkg, archive, err := diagnostics.Build(nil)
	if err != nil {
		t.Fatalf("build the extension package: %v", err)
	}
	verified, err := packaging.Verify(archive, pkg.Digest)
	if err != nil {
		t.Fatalf("verify the built package: %v", err)
	}
	if len(verified.Assets) != 0 {
		t.Fatalf("the package carries assets the product did not declare: %+v", verified.Assets)
	}
	if entries := entriesOf(t, archive); len(entries) != 2 ||
		entries[0] != packaging.ManifestEntry || entries[1] != packaging.DigestEntry {
		t.Fatalf("package entries = %v, want %q and %q", entries, packaging.ManifestEntry, packaging.DigestEntry)
	}

	// The contribution the package ships and the one the kernel activates are
	// the same: the manifest is rendered from Declaration().
	declared := fieldSet(t, verified.Manifest)
	contributions, ok := declared["contributions"].(map[string]any)
	if !ok {
		t.Fatalf("contributions = %v, want an object", declared["contributions"])
	}
	if len(contributions) != 1 {
		t.Fatalf("contribution classes = %v, want the page class alone", contributions)
	}
	packaged, ok := contributions["pages"].([]any)
	if !ok || len(packaged) != 1 {
		t.Fatalf("packaged pages = %v, want the diagnostics page", contributions["pages"])
	}
	page, ok := packaged[0].(map[string]any)
	if !ok || page["id"] != diagnostics.PageDiagnostics {
		t.Fatalf("packaged page = %v, want %q", packaged[0], diagnostics.PageDiagnostics)
	}

	active := diagnostics.Declaration().Contributions.Pages
	if len(active) != 1 || active[0].ID != diagnostics.PageDiagnostics {
		t.Fatalf("activated pages = %+v, want the packaged page", active)
	}
	if !sameJSON(t, page["items"], pageItems(t, active[0])) {
		t.Fatalf("packaged items = %v, want the activated ones %v", page["items"], active[0].Items)
	}
}

// TestTC155ManifestParsesAsTheKernelDeclaration checks that the manifest the
// SDK validates is also the declaration the kernel reads: no field the kernel
// does not know, no contribution lost in the wire form.
func TestTC155ManifestParsesAsTheKernelDeclaration(t *testing.T) {
	declaration, err := diagnostics.Manifest()
	if err != nil {
		t.Fatalf("render the manifest: %v", err)
	}
	document, err := json.Marshal(declaration)
	if err != nil {
		t.Fatalf("encode the manifest: %v", err)
	}
	parsed, err := extension.Parse(document)
	if err != nil {
		t.Fatalf("parse the manifest as a kernel declaration: %v", err)
	}
	loaded, err := parsed.Load()
	if err != nil {
		t.Fatalf("load the parsed declaration: %v", err)
	}
	if len(loaded.Problems) != 0 {
		t.Fatalf("problems = %+v, want none", loaded.Problems)
	}
	if loaded.Identifier != diagnostics.Identifier || loaded.Version != diagnostics.Version {
		t.Fatalf("declaration = %s@%s, want %s@%s",
			loaded.Identifier, loaded.Version, diagnostics.Identifier, diagnostics.Version)
	}
	if len(loaded.Contributions.Pages) != 1 || loaded.Contributions.Pages[0].ID != diagnostics.PageDiagnostics {
		t.Fatalf("pages = %+v, want the diagnostics page", loaded.Contributions.Pages)
	}
}

// TestTC155DigestCoversManifestAndAssets checks SDK-C-4: the same input yields
// the same digest, and an asset or a manifest change yields a different one.
func TestTC155DigestCoversManifestAndAssets(t *testing.T) {
	first, _, err := diagnostics.Build(nil)
	if err != nil {
		t.Fatalf("build the bare package: %v", err)
	}
	again, _, err := diagnostics.Build(nil)
	if err != nil {
		t.Fatalf("build the bare package again: %v", err)
	}
	if first.Digest != again.Digest {
		t.Fatalf("the same input produced digests %s and %s", first.Digest, again.Digest)
	}

	withAsset, _, err := diagnostics.Build([]packaging.Asset{{
		Path: packaging.AssetPrefix + "diagnostics.js", Content: []byte("v1"),
	}})
	if err != nil {
		t.Fatalf("build the package with one asset: %v", err)
	}
	changed, _, err := diagnostics.Build([]packaging.Asset{{
		Path: packaging.AssetPrefix + "diagnostics.js", Content: []byte("v2"),
	}})
	if err != nil {
		t.Fatalf("build the package with a changed asset: %v", err)
	}
	if first.Digest == withAsset.Digest {
		t.Fatal("adding a UI asset did not change the package digest")
	}
	if withAsset.Digest == changed.Digest {
		t.Fatal("changing a UI asset did not change the package digest")
	}

	// The digest covers the manifest too: the same packer over a changed
	// declaration yields another digest.
	declaration, err := diagnostics.Manifest()
	if err != nil {
		t.Fatalf("render the manifest: %v", err)
	}
	changedManifest := map[string]any{}
	for field, value := range declaration {
		changedManifest[field] = value
	}
	changedManifest["version"] = "1.0.1"
	other, _, err := packaging.Pack(changedManifest, nil)
	if err != nil {
		t.Fatalf("pack the changed manifest: %v", err)
	}
	if first.Digest == other.Digest {
		t.Fatal("changing the manifest did not change the package digest")
	}
}

// fieldSet decodes the manifest into the object the field-set checks read.
func fieldSet(t *testing.T, declaration manifest.ExtensionManifest) map[string]any {
	t.Helper()
	document, err := json.Marshal(declaration)
	if err != nil {
		t.Fatalf("encode the manifest: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(document, &decoded); err != nil {
		t.Fatalf("decode the manifest: %v", err)
	}
	return decoded
}

// pageItems renders the activated page's items as the wire form the manifest
// carries.
func pageItems(t *testing.T, page extension.Page) any {
	t.Helper()
	document, err := json.Marshal(page.Items)
	if err != nil {
		t.Fatalf("encode the page items: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(document, &decoded); err != nil {
		t.Fatalf("decode the page items: %v", err)
	}
	return decoded
}

// entriesOf lists the archive entries in the order the package wrote them.
func entriesOf(t *testing.T, archive []byte) []string {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("open the package archive: %v", err)
	}
	defer reader.Close()
	var names []string
	entries := tar.NewReader(reader)
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read the package archive: %v", err)
		}
		names = append(names, header.Name)
	}
	return names
}

// sameJSON reports whether two decoded JSON values are equal. Both sides are
// decoded into interface values, so encoding sorts map keys and the comparison
// does not depend on field order.
func sameJSON(t *testing.T, left, right any) bool {
	t.Helper()
	leftDocument, err := json.Marshal(left)
	if err != nil {
		t.Fatalf("encode left value: %v", err)
	}
	rightDocument, err := json.Marshal(right)
	if err != nil {
		t.Fatalf("encode right value: %v", err)
	}
	return bytes.Equal(leftDocument, rightDocument)
}
