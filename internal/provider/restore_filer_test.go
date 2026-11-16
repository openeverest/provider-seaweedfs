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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	backupv1alpha1 "github.com/openeverest/openeverest/v2/api/backup/v1alpha1"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"
)

func withFilerListServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	oldClient := filerHTTPClient
	oldBase := filerHTTPBaseURLFn
	filerHTTPClient = srv.Client()
	filerHTTPBaseURLFn = func(_, _ string) string { return srv.URL }
	t.Cleanup(func() {
		filerHTTPClient = oldClient
		filerHTTPBaseURLFn = oldBase
	})
	return srv
}

func filerListJSONHandler(status int, fullPaths ...string) http.Handler {
	entries := make([]filerDirEntry, 0, len(fullPaths))
	for _, fp := range fullPaths {
		entries = append(entries, filerDirEntry{FullPath: fp})
	}
	body, err := json.Marshal(filerDirListResponse{
		Entries:     entries,
		EmptyFolder: len(entries) == 0,
	})
	if err != nil {
		panic(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			http.Error(w, "Accept application/json required", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
}

func TestEnsureFilerPathEmptyUsesTinyLimit(t *testing.T) {
	var gotLimit string
	srv := withFilerListServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLimit = r.URL.Query().Get("limit")
		filerListJSONHandler(http.StatusOK, "/.seaweedfs-operator").ServeHTTP(w, r)
	}))
	require.NoError(t, ensureFilerPathEmptyForRestore(context.Background(), srv.URL, "/"))
	assert.Equal(t, "2", gotLimit)

	gotLimit = ""
	srv2 := withFilerListServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLimit = r.URL.Query().Get("limit")
		filerListJSONHandler(http.StatusNotFound).ServeHTTP(w, r)
	}))
	require.NoError(t, ensureFilerPathEmptyForRestore(context.Background(), srv2.URL, "/buckets"))
	assert.Equal(t, "1", gotLimit)
}

func TestEnsureFilerPathEmptyAllowsReservedRootOnly(t *testing.T) {
	srv := withFilerListServer(t, filerListJSONHandler(http.StatusOK, "/.seaweedfs-operator"))
	require.NoError(t, ensureFilerPathEmptyForRestore(context.Background(), srv.URL, "/"))
}

func TestEnsureFilerPathEmptyRejectsBucketsAtRoot(t *testing.T) {
	srv := withFilerListServer(t, filerListJSONHandler(http.StatusOK, "/.seaweedfs-operator", "/buckets"))
	err := ensureFilerPathEmptyForRestore(context.Background(), srv.URL, "/")
	require.Error(t, err)
	assert.True(t, isFilerNotEmpty(err))
	assert.Contains(t, err.Error(), "buckets")
}

func TestEnsureFilerPathEmptyTreatsMissingAsEmpty(t *testing.T) {
	srv := withFilerListServer(t, filerListJSONHandler(http.StatusNotFound))
	require.NoError(t, ensureFilerPathEmptyForRestore(context.Background(), srv.URL, "/buckets"))
}

func TestEnsureFilerPathEmptyRejectsEntriesUnderSubtree(t *testing.T) {
	srv := withFilerListServer(t, filerListJSONHandler(http.StatusOK, "/buckets/a"))
	err := ensureFilerPathEmptyForRestore(context.Background(), srv.URL, "/buckets")
	require.Error(t, err)
	assert.True(t, isFilerNotEmpty(err))
}

func TestEnsureFilerPathEmptyFailsWhenListingForbidden(t *testing.T) {
	srv := withFilerListServer(t, filerListJSONHandler(http.StatusForbidden))
	err := ensureFilerPathEmptyForRestore(context.Background(), srv.URL, "/")
	require.Error(t, err)
	assert.False(t, isFilerNotEmpty(err))
	assert.False(t, isFilerProbeTransient(err))
	assert.Contains(t, err.Error(), "exposeDirectoryData")
}

func TestSyncRestoreFailsWhenFilerNotEmpty(t *testing.T) {
	withFilerListServer(t, filerListJSONHandler(http.StatusOK, "/buckets"))
	instance := enabledBackupInstance("my-s3")
	backup := succeededBackup("adhoc-1", "sw", "my-s3")
	sw := seaweedClusterWithBackupStorage("sw", "my-s3")
	restore := restoreForBackup("restore-1", "sw", "adhoc-1", restoreParams("/"))
	ctx := backupSpecTestContext(t, instance, sw, backup, restore)

	exec, err := (&Provider{}).SyncRestore(ctx, restore)
	require.NoError(t, err)
	assert.Equal(t, backupv1alpha1.RestoreStateFailed, exec.State)
	assert.Contains(t, exec.Message, "not empty")

	err = ctx.Client().Get(ctx.Context(), client.ObjectKey{Namespace: "ns", Name: "restore-1"}, &seaweedv1.SeaweedRestore{})
	assert.True(t, apierrors.IsNotFound(err))
}

func TestSyncRestorePendingWhenFilerUnreachable(t *testing.T) {
	oldBase := filerHTTPBaseURLFn
	filerHTTPBaseURLFn = func(_, _ string) string { return "http://127.0.0.1:1" }
	t.Cleanup(func() { filerHTTPBaseURLFn = oldBase })

	instance := enabledBackupInstance("my-s3")
	backup := succeededBackup("adhoc-1", "sw", "my-s3")
	sw := seaweedClusterWithBackupStorage("sw", "my-s3")
	restore := restoreForBackup("restore-1", "sw", "adhoc-1", restoreParams("/"))
	ctx := backupSpecTestContext(t, instance, sw, backup, restore)

	exec, err := (&Provider{}).SyncRestore(ctx, restore)
	require.NoError(t, err)
	assert.Equal(t, backupv1alpha1.RestoreStatePending, exec.State)
	assert.Contains(t, exec.Message, "Waiting for filer")
}
