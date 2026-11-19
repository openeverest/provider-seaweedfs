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

// reservedBackupFilerRootName is the single root entry the seaweedfs-operator
// stages filer.meta.gz catalogs under (/.seaweedfs-operator/backups/...).
// See reservedBackupFilerDir in the operator's backup_storage.go.
const reservedBackupFilerRootName = ".seaweedfs-operator"

// System root names from SeaweedFS itself:
//
//	buckets — s3_constants.DefaultBucketsPath ("/buckets")
//	topics  — filer.TopicsDir ("/topics"); IsHiddenSystemEntry
//	etc     — IsHiddenSystemEntry (with topics) under "/"
//
// topics/etc are ignored by the emptiness gate; buckets may appear only when
// /buckets itself is empty (user S3 data lives there).
const (
	systemFilerRootBuckets = "buckets"
	systemFilerRootTopics  = "topics"
	systemFilerRootEtc     = "etc"
)

// Emptiness is decided from small listings, not a full walk:
// non-root needs at most one entry; root lists enough to see the allowed
// system/reserved names plus one unexpected, then probes /buckets when present.
const (
	// .seaweedfs-operator + buckets + topics + etc + one unexpected.
	filerEmptyProbeLimitRoot    = 5
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
		"filer path %q is not empty (found %q); refuse restore because fs.meta.load overwrites existing entries and can delete live chunks; under / only %q, empty %q/, %q, and %q may remain",
		e.Dir, e.Entry, reservedBackupFilerRootName, systemFilerRootBuckets, systemFilerRootTopics, systemFilerRootEtc,
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
// disallowed entries. It issues small filer HTTP lists (Accept:
// application/json) — it does not walk or page the directory.
//
// Under "/" the reserved catalog root .seaweedfs-operator and SeaweedFS
// system roots topics/etc may appear; buckets may appear only if /buckets
// itself is empty. Any other root name fails. Under a non-root path, any
// entry fails. A missing path (HTTP 404) is treated as empty.
//
// Note: the operator's fs.meta.load -dirPrefix is strings.HasPrefix on the
// entry's Dir (weed/shell/command_fs_meta_load.go), so this gate listing
// filerPath alone does not cover sibling prefix collisions.
func ensureFilerPathEmptyForRestore(ctx context.Context, baseURL, filerPath string) error {
	dir := filerPath
	if dir == "" {
		dir = "/"
	}

	if dir == "/" {
		return ensureFilerRootEmptyForRestore(ctx, baseURL)
	}

	resp, err := listFilerDirectory(ctx, baseURL, dir, filerEmptyProbeLimitSubtree)
	if err != nil {
		return err
	}
	names := resp.entryNames()
	if len(names) > 0 {
		return &errFilerNotEmpty{Dir: dir, Entry: names[0]}
	}
	return nil
}

func ensureFilerRootEmptyForRestore(ctx context.Context, baseURL string) error {
	resp, err := listFilerDirectory(ctx, baseURL, "/", filerEmptyProbeLimitRoot)
	if err != nil {
		return err
	}

	hasBuckets := false
	for _, name := range resp.entryNames() {
		switch name {
		case reservedBackupFilerRootName, systemFilerRootTopics, systemFilerRootEtc:
			continue
		case systemFilerRootBuckets:
			hasBuckets = true
		default:
			return &errFilerNotEmpty{Dir: "/", Entry: name}
		}
	}
	if !hasBuckets {
		return nil
	}
	return ensureFilerPathEmptyForRestore(ctx, baseURL, "/"+systemFilerRootBuckets)
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
	case res.StatusCode == http.StatusUnauthorized:
		return filerDirListResponse{}, fmt.Errorf(
			"filer refused directory listing (HTTP 401): jwtSigning.filerRead is enabled and the probe is unsigned; cannot verify %q is empty before restore",
			dir,
		)
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
