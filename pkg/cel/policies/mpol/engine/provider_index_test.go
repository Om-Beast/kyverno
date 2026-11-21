package engine

import (
	"context"
	"fmt"
	"testing"

	policieskyvernoio "github.com/kyverno/api/api/policies.kyverno.io"
	policiesv1beta1 "github.com/kyverno/api/api/policies.kyverno.io/v1beta1"
	celengine "github.com/kyverno/kyverno/pkg/cel/engine"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// indexedPolicyListObserver ensures exception handlers actually request the
// registered name index rather than materializing unrelated cached policies.
type indexedPolicyListObserver struct {
	client.Client
	lookups []string
}

func (c *indexedPolicyListObserver) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*policiesv1beta1.NamespacedMutatingPolicyList); ok {
		options := &client.ListOptions{}
		options.ApplyOptions(opts)
		if options.FieldSelector == nil {
			return fmt.Errorf("missing indexed policy-name selector")
		}
		name, ok := options.FieldSelector.RequiresExactMatch(celengine.NamespacedPolicyNameIndex)
		if !ok {
			return fmt.Errorf("policy-name lookup does not use %s index", celengine.NamespacedPolicyNameIndex)
		}
		c.lookups = append(c.lookups, name)
	}
	return c.Client.List(ctx, list, opts...)
}

// This covers indexed resolution (including cross-namespace same-name matches)
// and the old/new ref invalidation contract for all exception event types.
func TestPolicyExceptionHandler_IndexedNamespacedRequeues(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, policiesv1beta1.AddToScheme(scheme))

	objects := make([]client.Object, 0, 1003)
	for i := 0; i < 1000; i++ {
		objects = append(objects, &policiesv1beta1.NamespacedMutatingPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("unrelated-%04d", i), Namespace: "noise"},
		})
	}
	for _, item := range []struct{ name, namespace string }{
		{"target-policy", "team-a"},
		{"target-policy", "team-b"},
		{"old-policy", "team-c"},
	} {
		objects = append(objects, &policiesv1beta1.NamespacedMutatingPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: item.name, Namespace: item.namespace},
		})
	}
	reader := &indexedPolicyListObserver{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(objects...).
			WithIndex(&policiesv1beta1.NamespacedMutatingPolicy{}, celengine.NamespacedPolicyNameIndex, celengine.IndexPolicyName).
			Build(),
	}
	newException := func(name string) *policiesv1beta1.PolicyException {
		return &policiesv1beta1.PolicyException{
			ObjectMeta: metav1.ObjectMeta{Name: "exemption", Namespace: "exceptions"},
			Spec: policiesv1beta1.PolicyExceptionSpec{
				PolicyRefs: []policiesv1beta1.PolicyRef{
					{Name: name, Kind: policieskyvernoio.NamespacedMutatingPolicyKind},
				},
			},
		}
	}
	target := []reconcile.Request{
		{NamespacedName: client.ObjectKey{Namespace: "team-a", Name: "target-policy"}},
		{NamespacedName: client.ObjectKey{Namespace: "team-b", Name: "target-policy"}},
	}
	oldTarget := reconcile.Request{NamespacedName: client.ObjectKey{Namespace: "team-c", Name: "old-policy"}}

	tests := []struct {
		name string
		event string
		lookups []string
		expected []reconcile.Request
	}{
		{
			name: "create",
			event: "create",
			lookups: []string{"target-policy"},
			expected: target,
		},
		{
			name: "update refreshes removed and added names",
			event: "update",
			lookups: []string{"target-policy", "old-policy"},
			expected: append(append([]reconcile.Request{}, target...), oldTarget),
		},
		{
			name: "delete invalidates compiled exceptions",
			event: "delete",
			lookups: []string{"target-policy"},
			expected: target,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader.lookups = nil
			q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
			defer q.ShutDown()
			h := newPolicyExceptionHandler(reader)

			switch tt.event {
			case "create":
				h.Create(context.Background(), event.TypedCreateEvent[client.Object]{Object: newException("target-policy")}, q)
			case "update":
				h.Update(context.Background(), event.TypedUpdateEvent[client.Object]{
					ObjectOld: newException("old-policy"),
					ObjectNew: newException("target-policy"),
				}, q)
			case "delete":
				h.Delete(context.Background(), event.TypedDeleteEvent[client.Object]{Object: newException("target-policy")}, q)
			}
			var got []reconcile.Request
			for q.Len() > 0 {
				item, _ := q.Get()
				got = append(got, item)
				q.Done(item)
			}
			require.ElementsMatch(t, tt.expected, got)
			require.Equal(t, tt.lookups, reader.lookups)
		})
	}
}
