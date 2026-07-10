//go:build mage

package main

import (
	"fmt"
	"os"
	"regexp"

	"github.com/magefile/mage/mg"
)

// Release namespace bundles the multi-ecosystem version bump that precedes
// tagging vX.Y.Z (see the publish playbook in the main README's Install
// section). Go itself has no version file -- the git tag is the version --
// so this only needs to touch npm/package.json and rust/Cargo.toml.
type Release mg.Namespace

var semverRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)$`)

// SetVersion writes version (accepts an optional leading "v", e.g. "v0.2.0"
// or "0.2.0") into npm/package.json and rust/Cargo.toml, keeping every
// registry-published ecosystem at the same version as the git tag about to
// be cut. Run this, review the diff, commit it, then tag vX.Y.Z.
func (Release) SetVersion(version string) error {
	m := semverRe.FindStringSubmatch(version)
	if m == nil {
		return fmt.Errorf("release: %q is not a semver version (want X.Y.Z or vX.Y.Z)", version)
	}
	bare := m[1]

	if err := replaceVersion("npm/package.json", jsonVersionRe, bare); err != nil {
		return err
	}
	if err := replaceVersion("rust/Cargo.toml", cargoVersionRe, bare); err != nil {
		return err
	}
	fmt.Printf("release: set npm/package.json and rust/Cargo.toml to %s\n", bare)
	fmt.Println("release: review the diff, commit, then: git tag -a v" + bare + " -m v" + bare)
	return nil
}

var jsonVersionRe = regexp.MustCompile(`"version":\s*"[^"]*"`)

// cargoVersionRe matches only a line beginning with "version = "..."" (the
// [package] table's own field), not a dependency's `version = "..."` key
// inside a `[dependencies.foo]` table -- those would also match `^version
// = ` at line start if this crate ever grows path/git deps written in
// table form, so this intentionally only touches the *first* match (see
// replaceVersion), which is always the [package] version in a
// conventionally-ordered Cargo.toml.
var cargoVersionRe = regexp.MustCompile(`(?m)^version\s*=\s*"[^"]*"`)

// replaceVersion rewrites the first regexp match of a `"version": "..."` /
// `version = "..."` field in the file at path to version, preserving
// everything else in the file byte-for-byte.
func replaceVersion(path string, fieldRe *regexp.Regexp, version string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	loc := fieldRe.FindIndex(data)
	if loc == nil {
		return fmt.Errorf("release: no version field found in %s", path)
	}
	quoteRe := regexp.MustCompile(`"[^"]*"$`)
	qLoc := quoteRe.FindIndex(data[loc[0]:loc[1]])
	if qLoc == nil {
		return fmt.Errorf("release: version field in %s has no quoted value", path)
	}
	valueStart := loc[0] + qLoc[0] + 1 // just past the opening quote
	valueEnd := loc[0] + qLoc[1] - 1   // just before the closing quote

	out := append([]byte{}, data[:valueStart]...)
	out = append(out, version...)
	out = append(out, data[valueEnd:]...)
	return os.WriteFile(path, out, 0o644)
}
