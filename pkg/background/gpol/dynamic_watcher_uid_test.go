package gpol

import (
	"context"
	"errors"
	"testing"

	"github.com/kyverno/kyverno/pkg/background/common"
	"github.com/kyverno/kyverno/pkg/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// TestDeleteCachedDownstreamUIDPrecondition models a replacement at the API
// server without depending on informer delivery or cluster scheduling.
func TestDeleteCachedDownstreamUIDPrecondition(t *testing.T) {
	const oldUID types.UID = "old-uid"
	const newUID types.UID = "replacement-uid"
	groupResource := schema.GroupResource{Resource: "configmaps"}
	labels := map[string]string{
		common.GeneratePolicyLabel:     "gpol",
		common.GenerateTriggerUIDLabel: "trigger",
	}
	tests := []struct {
		name       string
		cachedUID  types.UID
		dataUID    types.UID
		nilData    bool
		deleteErr  error
		liveUID    types.UID
		getErr     error
		wantDelete bool
		wantGet    bool
		wantCache  bool
	}{
		{name: "successful delete", cachedUID: oldUID, dataUID: oldUID, wantDelete: true},
		{name: "not found retires cache", cachedUID: oldUID, dataUID: oldUID, deleteErr: apierrors.NewNotFound(groupResource, "settings"), wantDelete: true},
		{name: "replacement conflict retires old cache", cachedUID: oldUID, dataUID: oldUID, deleteErr: apierrors.NewConflict(groupResource, "settings", errors.New("UID precondition failed")), liveUID: newUID, wantDelete: true, wantGet: true},
		{name: "conflict and gone retires cache", cachedUID: oldUID, dataUID: oldUID, deleteErr: apierrors.NewConflict(groupResource, "settings", errors.New("UID precondition failed")), getErr: apierrors.NewNotFound(groupResource, "settings"), wantDelete: true, wantGet: true},
		{name: "ambiguous conflict preserves cache", cachedUID: oldUID, dataUID: oldUID, deleteErr: apierrors.NewConflict(groupResource, "settings", errors.New("conflict")), liveUID: oldUID, wantDelete: true, wantGet: true, wantCache: true},
		{name: "failed conflict verification preserves cache", cachedUID: oldUID, dataUID: oldUID, deleteErr: apierrors.NewConflict(groupResource, "settings", errors.New("conflict")), getErr: apierrors.NewServiceUnavailable("API unavailable"), wantDelete: true, wantGet: true, wantCache: true},
		{name: "transient deletion error retains cache", cachedUID: oldUID, dataUID: oldUID, deleteErr: apierrors.NewServiceUnavailable("API unavailable"), wantDelete: true, wantCache: true},
		{name: "empty cache UID refuses deletion", cachedUID: "", dataUID: oldUID, wantCache: true},
		{name: "missing cached object refuses deletion", cachedUID: oldUID, nilData: true, wantCache: true},
		// Propagation clears the desired-state object's UID; the cache key
		// remains the observed UID and must still protect the deletion.
		{name: "UID cleared by propagation still uses cache key", cachedUID: oldUID, dataUID: "", wantDelete: true},
		// Recreation can re-key a copied cache entry without changing Data.
		{name: "recreated downstream uses new cache key", cachedUID: newUID, dataUID: oldUID, wantDelete: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := makeUnstructured("", "", "v1", "ConfigMap", "settings", "app", tt.dataUID, labels)
			if tt.nilData {
				obj = nil
			}
			entry := Resource{Name: "settings", Namespace: "app", Labels: labels, Data: obj}
			cache := map[types.UID]Resource{tt.cachedUID: entry}
			calls, gets := 0, 0
			client := &MockClient{
				deleteFn: func(_ context.Context, apiVersion, kind, namespace, name string, dryRun bool, opts metav1.DeleteOptions) error {
					calls++
					assert.Equal(t, "v1", apiVersion)
					assert.Equal(t, "ConfigMap", kind)
					assert.Equal(t, "app", namespace)
					assert.Equal(t, "settings", name)
					assert.False(t, dryRun)
					if assert.NotNil(t, opts.Preconditions) && assert.NotNil(t, opts.Preconditions.UID) {
						assert.Equal(t, tt.cachedUID, *opts.Preconditions.UID)
					}
					return tt.deleteErr
				},
				getFn: func(_ context.Context, apiVersion, kind, namespace, name string) (*unstructured.Unstructured, error) {
					gets++
					if tt.getErr != nil {
						return nil, tt.getErr
					}
					return makeUnstructured("", "", "v1", "ConfigMap", "settings", "app", tt.liveUID, nil), nil
				},
			}
			w := &watcher{metadataCache: cache}
			wm := &WatchManager{log: logging.WithName("test"), client: client}
			wm.deleteCachedDownstream(wm.log, w, tt.cachedUID, entry)
			if tt.wantDelete {
				require.Equal(t, 1, calls, "must send exactly one guarded DELETE")
			} else {
				assert.Zero(t, calls)
			}
			if tt.wantGet {
				assert.Equal(t, 1, gets)
			} else {
				assert.Zero(t, gets)
			}
			_, exists := cache[tt.cachedUID]
			assert.Equal(t, tt.wantCache, exists)
		})
	}
}

// Both trigger removal and stale-desired-set cleanup use the same identity
// guard, even if a new unrelated resource occupies the original name.
func TestCleanupDownstreamsProtectsReplacement(t *testing.T) {
	for _, path := range []string{"trigger delete", "stale downstream"} {
		t.Run(path, func(t *testing.T) {
			const oldUID types.UID = "old"
			const replacementUID types.UID = "new"
			gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
			labels := map[string]string{
				common.GeneratePolicyLabel:     "gpol",
				common.GenerateTriggerUIDLabel: "trigger",
			}
			obj := makeUnstructured("", "", "v1", "ConfigMap", "settings", "app", oldUID, labels)
			entry := Resource{Name: obj.GetName(), Namespace: obj.GetNamespace(), Labels: labels, Data: obj}
			w := &watcher{metadataCache: map[types.UID]Resource{oldUID: entry}}
			deletes := 0
			client := &MockClient{
				deleteFn: func(_ context.Context, _, _, _, _ string, _ bool, opts metav1.DeleteOptions) error {
					deletes++
					require.NotNil(t, opts.Preconditions)
					require.NotNil(t, opts.Preconditions.UID)
					assert.Equal(t, oldUID, *opts.Preconditions.UID)
					return apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, "settings", errors.New("UID precondition failed"))
				},
				getFn: func(_ context.Context, _, _, _, _ string) (*unstructured.Unstructured, error) {
					return makeUnstructured("", "", "v1", "ConfigMap", "settings", "app", replacementUID, nil), nil
				},
			}
			wm := &WatchManager{
				client: client, log: logging.WithName("test"),
				policyRefs: map[string][]schema.GroupVersionResource{"gpol": {gvr}},
				dynamicWatchers: map[schema.GroupVersionResource]*watcher{gvr: w},
			}
			trigger := &v1.ResourceSpec{UID: "trigger"}
			if path == "trigger delete" {
				wm.DeleteDownstreams("gpol", trigger)
			} else {
				wm.CleanupStaleDownstreams("gpol", trigger, nil)
			}
			assert.Equal(t, 1, deletes)
			assert.Empty(t, w.metadataCache)
		})
	}
}
