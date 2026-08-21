// Package ingest coordinates one immutable repository revision admission.
package ingest

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/gitstore"
	"github.com/mhingston/skillet/internal/packagebuilder"
	"github.com/mhingston/skillet/internal/packagestore"
)

type Result struct {
	Commit                string
	Admitted, Quarantined int
}
type Options struct {
	Include, Exclude []string
	SearchExclusions []discovery.MetadataRule
	PackageLimits    packagebuilder.Limits
}

func gitMode(mode string) int64 {
	parsed, err := strconv.ParseInt(mode, 8, 32)
	if err != nil || parsed&0170000 != 0100000 {
		return 0644
	}
	if parsed&0100 != 0 {
		return 0755
	}
	return 0644
}

func SyncOnce(ctx context.Context, mirror gitstore.Source, repo catalogue.Repository, packages *packagestore.Store, catalog *catalogue.Store, ref string) (Result, error) {
	return SyncOnceWithOptions(ctx, mirror, repo, packages, catalog, ref, Options{Include: []string{"**/SKILL.md"}})
}

func SyncOnceWithOptions(ctx context.Context, mirror gitstore.Source, repo catalogue.Repository, packages *packagestore.Store, catalog *catalogue.Store, ref string, options Options) (Result, error) {
	if mirror == nil || packages == nil || catalog == nil {
		return Result{}, fmt.Errorf("mirror, package store, and catalogue are required")
	}
	commit, err := mirror.Fetch(ctx, ref)
	if err != nil {
		return Result{}, err
	}
	return SyncAtCommitWithOptions(ctx, mirror, repo, packages, catalog, commit, options)
}

// SyncAtCommitWithOptions admits exactly commit. The caller is responsible
// for resolving and fetching the commit into the mirror first.
func SyncAtCommitWithOptions(ctx context.Context, mirror gitstore.Source, repo catalogue.Repository, packages *packagestore.Store, catalog *catalogue.Store, commit string, options Options) (Result, error) {
	if mirror == nil || packages == nil || catalog == nil {
		return Result{}, fmt.Errorf("mirror, package store, and catalogue are required")
	}
	if commit == "" {
		return Result{}, fmt.Errorf("commit is required")
	}
	gitEntries, err := mirror.ListTree(ctx, commit)
	if err != nil {
		return Result{}, err
	}
	objects := make(map[string]string, len(gitEntries))
	entries := make([]discovery.TreeEntry, 0, len(gitEntries))
	for _, entry := range gitEntries {
		objects[entry.Path] = entry.ObjectID
		kind := discovery.RegularFile
		switch {
		case entry.ObjectType == "tree":
			kind = discovery.Directory
		case entry.Mode == "120000":
			kind = discovery.Symlink
		case entry.Mode == "160000":
			kind = discovery.Device
		case entry.ObjectType != "blob":
			kind = discovery.Device
		}
		entries = append(entries, discovery.TreeEntry{Path: entry.Path, Kind: kind})
	}
	discovered, err := discovery.Discover(entries, func(p string) ([]byte, error) {
		object := objects[p]
		if object == "" {
			return nil, fmt.Errorf("missing object for %s", p)
		}
		return mirror.ReadBlob(ctx, object)
	}, discovery.Options{Include: options.Include, Exclude: options.Exclude, SearchExclusions: options.SearchExclusions})
	if err != nil {
		return Result{}, err
	}
	result := Result{Commit: commit}
	presentPaths := make(map[string]struct{}, len(discovered))
	type preparedAdmission struct {
		skill discovery.Skill
		tree  string
		pkgs  catalogue.PackageDigests
	}
	prepared := make([]preparedAdmission, 0, len(discovered))
	for _, skill := range discovered {
		presentPaths[skill.RelativePath] = struct{}{}
	}
	for _, skill := range discovered {
		tree, treeErr := mirror.TreeID(ctx, commit, skill.RelativePath)
		if treeErr != nil {
			return Result{}, treeErr
		}
		if skill.State != discovery.Admitted {
			if err := catalog.RecordQuarantine(ctx, repo, skill, commit, tree); err != nil {
				return Result{}, err
			}
			result.Quarantined++
			continue
		}
		root := skill.RelativePath
		buildEntries := make([]packagebuilder.Entry, 0)
		for _, entry := range gitEntries {
			if entry.Path != root && !strings.HasPrefix(entry.Path, root+"/") {
				continue
			}
			kind := packagebuilder.Regular
			switch {
			case entry.Mode == "120000":
				kind = packagebuilder.Symlink
			case entry.Mode == "160000" || entry.ObjectType != "blob":
				kind = packagebuilder.Special
			}
			buildEntries = append(buildEntries, packagebuilder.Entry{Path: entry.Path, Kind: kind, Mode: gitMode(entry.Mode)})
		}
		pkg, err := packagebuilder.Build(skill.Frontmatter.Name, root, buildEntries, func(p string) ([]byte, error) { return mirror.ReadBlob(ctx, objects[p]) }, options.PackageLimits)
		if err != nil {
			return Result{}, err
		}
		tarDigest, err := packages.Put("tar.gz", pkg.TarGZ)
		if err != nil {
			return Result{}, err
		}
		zipDigest, err := packages.Put("zip", pkg.ZIP)
		if err != nil {
			return Result{}, err
		}
		prepared = append(prepared, preparedAdmission{skill: skill, tree: tree, pkgs: catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest}})
	}
	// Build and retain every package before changing any active catalogue
	// pointer. A malformed or unreadable later skill must not leave an earlier
	// skill from the same source revision partially activated.
	batch := make([]catalogue.Admission, 0, len(prepared))
	for _, admission := range prepared {
		batch = append(batch, catalogue.Admission{Skill: admission.skill, CommitSHA: commit, TreeSHA: admission.tree, Packages: admission.pkgs})
	}
	if _, err := catalog.AdmitBatchAndMarkMissing(ctx, repo, batch, presentPaths); err != nil {
		return Result{}, err
	}
	result.Admitted = len(batch)
	return result, nil
}
