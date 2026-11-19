// Command kos-diagnostics-pack builds the kos-diagnostics extension package
// with the author SDK: manifest, UI assets and the digest covering both alike
// (KOSD-C-17, SDK-C-4). The same tool packs a third-party extension; nothing
// here reads a product identity.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tsic404/kos-sdk/packaging"

	"github.com/tsic404/kos-diagnostics"
)

func main() {
	var (
		assetsDir = flag.String("assets", "assets", "directory of UI assets, laid out as assets/<path> in the package")
		out       = flag.String("out", filepath.Join("dist", "kos-diagnostics.tar"), "package archive to write")
		digestOut = flag.String("digest-out", "", "file to write the package digest to (default: <out>.digest)")
	)
	flag.Parse()
	if err := run(*assetsDir, *out, *digestOut); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(assetsDir, out, digestOut string) error {
	assets, err := readAssets(assetsDir)
	if err != nil {
		return err
	}
	pkg, archive, err := diagnostics.Build(assets)
	if err != nil {
		return err
	}
	if digestOut == "" {
		digestOut = out + ".digest"
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(out, archive, 0o644); err != nil {
		return fmt.Errorf("write package: %w", err)
	}
	if err := os.WriteFile(digestOut, []byte(pkg.Digest.String()+"\n"), 0o644); err != nil {
		return fmt.Errorf("write digest: %w", err)
	}
	fmt.Println(pkg.Digest)
	return nil
}

// readAssets reads every file under dir as one package asset. A missing
// directory is an empty asset set: the product declares no UI asset, so a host
// that adds none still produces a valid package.
func readAssets(dir string) ([]packaging.Asset, error) {
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read assets directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("assets path %q is not a directory", dir)
	}
	var assets []packaging.Asset
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read asset %q: %w", path, err)
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		assets = append(assets, packaging.Asset{
			Path:    packaging.AssetPrefix + filepath.ToSlash(relative),
			Content: content,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return assets, nil
}
