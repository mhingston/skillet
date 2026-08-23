// Package adapter contains the small host-facing client used by Skillet
// adapters. It deliberately keeps host activation outside the registry.
package adapter

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Client struct {
	Server string
	Token  string
}

type SearchResult struct {
	Candidates []struct {
		CandidateID string `json:"candidate_id"`
		Skill       struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"skill"`
	} `json:"candidates"`
}

type LifecycleReference struct {
	RevisionID        string `json:"revision_id"`
	SkillID           string `json:"skill_id"`
	Commit            string `json:"commit"`
	Tree              string `json:"tree"`
	ArchiveSHA256     string `json:"archive_sha256"`
	QueryID           string `json:"query_id,omitempty"`
	MaterializationID string `json:"materialization_id,omitempty"`
}

type MaterializeResult struct {
	Skill struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Version string `json:"version,omitempty"`
	} `json:"skill"`
	Package struct {
		DownloadURL   string `json:"download_url"`
		ArchiveSHA256 string `json:"archive_sha256"`
		Format        string `json:"format"`
	} `json:"package"`
	Lifecycle LifecycleReference `json:"lifecycle"`
}

type LifecycleResult struct {
	Status     string `json:"status"`
	Event      string `json:"event"`
	RevisionID string `json:"revision_id"`
}

func (c Client) connect(ctx context.Context) (*mcp.ClientSession, error) {
	hc := &http.Client{Transport: bearerTransport{base: http.DefaultTransport, token: c.Token}}
	client := mcp.NewClient(&mcp.Implementation{Name: "skillet-adapter", Version: "1"}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: strings.TrimRight(c.Server, "/"), HTTPClient: hc, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
}

func (c Client) Search(ctx context.Context, query string, limit int) (SearchResult, error) {
	s, err := c.connect(ctx)
	if err != nil {
		return SearchResult{}, err
	}
	defer s.Close()
	r, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "search_skills", Arguments: map[string]any{"query": query, "limit": limit}})
	if err != nil {
		return SearchResult{}, err
	}
	if r.IsError {
		return SearchResult{}, fmt.Errorf("search_skills returned an error")
	}
	var out SearchResult
	if err := decodeStructured(r.StructuredContent, &out); err != nil {
		return out, err
	}
	return out, nil
}

func (c Client) Materialize(ctx context.Context, candidateID string, values ...string) (MaterializeResult, string, error) {
	version, versionRange, skillID, destination := "", "", "", ""
	if len(values) == 1 {
		destination = values[0]
	} else if len(values) == 3 {
		version, versionRange, destination = values[0], values[1], values[2]
	} else {
		return MaterializeResult{}, "", fmt.Errorf("materialize arguments are invalid")
	}
	s, err := c.connect(ctx)
	if err != nil {
		return MaterializeResult{}, "", err
	}
	defer s.Close()
	args := map[string]any{
		"client": map[string]any{"os": hostOS(), "shell": "posix"},
	}
	if candidateID != "" {
		args["candidate_id"] = candidateID
	} else if version != "" {
		args["version"] = version
	} else if versionRange != "" {
		args["range"] = versionRange
	}
	if len(values) == 4 {
		version, versionRange, skillID, destination = values[0], values[1], values[2], values[3]
		delete(args, "version")
		delete(args, "range")
		if version != "" {
			args["version"] = version
		}
		if versionRange != "" {
			args["range"] = versionRange
		}
		args["skill_id"] = skillID
	}
	r, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "materialize_skill", Arguments: args})
	if err != nil {
		return MaterializeResult{}, "", err
	}
	if r.IsError {
		return MaterializeResult{}, "", fmt.Errorf("materialize_skill returned an error")
	}
	var out MaterializeResult
	if err := decodeStructured(r.StructuredContent, &out); err != nil {
		return out, "", err
	}
	if out.Package.DownloadURL == "" || out.Package.ArchiveSHA256 == "" || out.Skill.Name == "" {
		return out, "", fmt.Errorf("materialize response lacks package, digest, or skill name")
	}
	path, err := downloadAndExtract(ctx, out.Package.DownloadURL, out.Package.ArchiveSHA256, out.Package.Format, destination, out.Skill.Name)
	return out, path, err
}

func (c Client) ReportLifecycle(ctx context.Context, lifecycle LifecycleReference, event, source, correlationID string) (LifecycleResult, error) {
	s, err := c.connect(ctx)
	if err != nil {
		return LifecycleResult{}, err
	}
	defer s.Close()
	r, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "report_skill_lifecycle", Arguments: map[string]any{
		"lifecycle":      lifecycle,
		"event":          event,
		"source":         source,
		"correlation_id": correlationID,
	}})
	if err != nil {
		return LifecycleResult{}, err
	}
	if r.IsError {
		return LifecycleResult{}, fmt.Errorf("report_skill_lifecycle returned an error")
	}
	var out LifecycleResult
	if err := decodeStructured(r.StructuredContent, &out); err != nil {
		return out, err
	}
	return out, nil
}

func decodeStructured(value any, dst any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

func downloadAndExtract(ctx context.Context, url, expected, format, destination, name string) (string, error) {
	if format != "tar.gz" {
		return "", fmt.Errorf("unsupported package format %q", format)
	}
	if filepath.Base(name) != name || name == "." || name == ".." {
		return "", fmt.Errorf("unsafe skill name %q", name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("package download returned %s", resp.Status)
	}
	tmp, err := os.CreateTemp("", "skillet-package-*.tar.gz")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", err
	}
	b, err := os.ReadFile(tmpName)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != strings.ToLower(expected) {
		return "", fmt.Errorf("archive digest mismatch")
	}
	stage, err := os.MkdirTemp("", "skillet-extract-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	f, err := os.Open(tmpName)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, readErr := tr.Next()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
		clean := filepath.Clean(filepath.FromSlash(hdr.Name))
		if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("unsafe archive path %q", hdr.Name)
		}
		target := filepath.Join(stage, clean)
		if hdr.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0700); err != nil {
				return "", err
			}
			continue
		}
		if !hdr.FileInfo().Mode().IsRegular() {
			return "", fmt.Errorf("unsupported archive entry %q", hdr.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return "", err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, hdr.FileInfo().Mode().Perm())
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(out, tr)
		closeErr := out.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	entry := filepath.Join(stage, name, "SKILL.md")
	if _, err := os.Stat(entry); err != nil {
		return "", fmt.Errorf("package lacks %s/SKILL.md: %w", name, err)
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		return "", err
	}
	final := filepath.Join(destination, name)
	if err := os.RemoveAll(final); err != nil {
		return "", err
	}
	if err := os.Rename(filepath.Join(stage, name), final); err != nil {
		return "", err
	}
	return filepath.Join(final, "SKILL.md"), nil
}

func hostOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	default:
		return runtime.GOOS
	}
}

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.token != "" {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+t.token)
	}
	return t.base.RoundTrip(r)
}
