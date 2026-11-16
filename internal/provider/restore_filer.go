// Copyright (C) 2026 The OpenEverest Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"
)

// reservedBackupFilerRootName is the single root entry the operator uses to
// stage filer.meta.gz catalogs (/.seaweedfs-operator/...). Present during a
// same-cluster restore and ignored by the emptiness gate.
const reservedBackupFilerRootName = ".seaweedfs-operator"

// Emptiness is decided from a single small listing, not a full walk:
// non-root needs at most one entry; root needs two so ".seaweedfs-operator"
// alone is distinguishable from ".seaweedfs-operator" + anything else.
const (
	filerEmptyProbeLimitRoot    = 2
	filerEmptyProbeLimitSubtree = 1
)

// filerHTTPClient is used to probe the filer HTTP directory API. Tests swap it.
var filerHTTPClient = &http.Client{Timeout: 10 * time.Second}

// errFilerNotEmpty is a terminal safety-gate failure: fs.meta.load would race
// with existing entries under the restore filerPath.
type errFilerNotEmpty struct {
	Dir   string
	Entry string
}

func (e *errFilerNotEmpty) Error() string {
	return fmt.Sprintf(
		"filer path %q is not empty (found %q); refuse restore because fs.meta.load overwrites existing entries and can delete live chunks; only %q may remain under /",
		e.Dir, e.Entry, reservedBackupFilerRootName,
	)
}

// errFilerProbeTransient means the filer could not be listed yet (network,
// 5xx). SyncRestore maps this to Pending.
type errFilerProbeTransient struct {
	msg string
}

func (e *errFilerProbeTransient) Error() string { return e.msg }

func isFilerNotEmpty(err error) bool {
	var ne *errFilerNotEmpty
	return errors.As(err, &ne)
}

func isFilerProbeTransient(err error) bool {
	var te *errFilerProbeTransient
	return errors.As(err, &te)
}

func defaultFilerHTTPBaseURL(clusterName, namespace string) string {
	return fmt.Sprintf("http://%s-filer.%s:%d", clusterName, namespace, seaweedv1.FilerHTTPPort)
}

// filerHTTPBaseURLFn builds the filer HTTP base URL. Tests replace it.
var filerHTTPBaseURLFn = defaultFilerHTTPBaseURL

// ensureFilerPathEmptyForRestore probes whether the restore filerPath has any
// disallowed entries. It issues one filer HTTP list with a tiny limit
// (Accept: application/json) — it does not walk or page the directory.
//
// Under "/" only .seaweedfs-operator may appear; any other name (or a second
// entry) fails. Under a non-root path, any entry fails. A missing path (HTTP
// 404) is treated as empty.
func ensureFilerPathEmptyForRestore(ctx context.Context, baseURL, filerPath string) error {
	dir := filerPath
	if dir == "" {
		dir = "/"
	}

	limit := filerEmptyProbeLimitSubtree
	if dir == "/" {
		limit = filerEmptyProbeLimitRoot
	}

	resp, err := listFilerDirectory(ctx, baseURL, dir, limit)
	if err != nil {
		return err
	}

	names := resp.entryNames()
	if dir == "/" {
		switch len(names) {
		case 0:
			return nil
		case 1:
			if names[0] == reservedBackupFilerRootName {
				return nil
			}
			return &errFilerNotEmpty{Dir: dir, Entry: names[0]}
		default:
			// Two hits with limit=2 means at least one non-reserved name, or
			// two unexpected names. Prefer reporting a non-reserved entry.
			for _, name := range names {
				if name != reservedBackupFilerRootName {
					return &errFilerNotEmpty{Dir: dir, Entry: name}
				}
			}
			return &errFilerNotEmpty{Dir: dir, Entry: names[0]}
		}
	}

	if len(names) > 0 {
		return &errFilerNotEmpty{Dir: dir, Entry: names[0]}
	}
	return nil
}

type filerDirListResponse struct {
	Entries     []filerDirEntry `json:"Entries"`
	EmptyFolder bool            `json:"EmptyFolder"`
}

// filerDirEntry matches the JSON the filer emits for weed/filer.Entry:
// embedded util.FullPath marshals under the key "FullPath".
type filerDirEntry struct {
	FullPath string `json:"FullPath"`
}

func (r filerDirListResponse) entryNames() []string {
	out := make([]string, 0, len(r.Entries))
	for _, e := range r.Entries {
		name := path.Base(strings.TrimSuffix(e.FullPath, "/"))
		if name == "" || name == "." || name == "/" {
			continue
		}
		out = append(out, name)
	}
	return out
}

func listFilerDirectory(ctx context.Context, baseURL, dir string, limit int) (filerDirListResponse, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	if err != nil {
		return filerDirListResponse{}, fmt.Errorf("parse filer URL: %w", err)
	}
	if dir != "/" {
		u = u.JoinPath(strings.TrimPrefix(dir, "/"))
	}
	q := u.Query()
	q.Set("limit", fmt.Sprintf("%d", limit))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return filerDirListResponse{}, &errFilerProbeTransient{msg: fmt.Sprintf("build filer list request: %v", err)}
	}
	req.Header.Set("Accept", "application/json")

	res, err := filerHTTPClient.Do(req)
	if err != nil {
		return filerDirListResponse{}, &errFilerProbeTransient{
			msg: fmt.Sprintf("Waiting for filer directory listing at %s: %v", u.Host, err),
		}
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return filerDirListResponse{}, &errFilerProbeTransient{msg: fmt.Sprintf("read filer list response: %v", err)}
	}

	switch {
	case res.StatusCode == http.StatusNotFound:
		// Missing directory is empty for restore purposes.
		return filerDirListResponse{EmptyFolder: true}, nil
	case res.StatusCode == http.StatusForbidden:
		return filerDirListResponse{}, fmt.Errorf(
			"filer refused directory listing (HTTP 403): exposeDirectoryData is disabled; cannot verify %q is empty before restore",
			dir,
		)
	case res.StatusCode >= 500:
		return filerDirListResponse{}, &errFilerProbeTransient{
			msg: fmt.Sprintf("Waiting for filer directory listing (HTTP %d)", res.StatusCode),
		}
	case res.StatusCode != http.StatusOK:
		return filerDirListResponse{}, fmt.Errorf(
			"filer directory listing for %q returned HTTP %d: %s",
			dir, res.StatusCode, strings.TrimSpace(string(body)),
		)
	}

	var parsed filerDirListResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return filerDirListResponse{}, fmt.Errorf("decode filer directory listing: %w", err)
	}
	return parsed, nil
}
