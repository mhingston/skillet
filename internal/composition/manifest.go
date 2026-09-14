package composition

import (
	"bytes"
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

const (
	ManifestVersion       = 1
	MaxCollections        = 64
	MaxCollectionMembers  = 128
)

type Manifest struct {
	Version     int          `yaml:"version" json:"version"`
	Collections []Collection `yaml:"collections" json:"collections"`
}

func LoadManifest(path string) (Manifest, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	return ParseManifest(contents)
}

func ParseManifest(contents []byte) (Manifest, error) {
	var manifest Manifest
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse composition manifest: %w", err)
	}
	if manifest.Version != ManifestVersion {
		return Manifest{}, fmt.Errorf("composition manifest version must be %d", ManifestVersion)
	}
	if len(manifest.Collections) > MaxCollections {
		return Manifest{}, fmt.Errorf("composition manifest exceeds %d collections", MaxCollections)
	}
	seen := map[string]bool{}
	for i := range manifest.Collections {
		collection := &manifest.Collections[i]
		if len(collection.Members) > MaxCollectionMembers {
			return Manifest{}, fmt.Errorf("collection %q exceeds %d members", collection.ID, MaxCollectionMembers)
		}
		if err := ValidateCollection(*collection); err != nil {
			return Manifest{}, err
		}
		if seen[collection.ID] {
			return Manifest{}, fmt.Errorf("duplicate collection %q", collection.ID)
		}
		seen[collection.ID] = true
		collection.Members = sortedReferences(collection.Members)
	}
	sort.Slice(manifest.Collections, func(i, j int) bool { return manifest.Collections[i].ID < manifest.Collections[j].ID })
	return manifest, nil
}
