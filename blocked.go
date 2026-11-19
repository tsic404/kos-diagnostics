package diagnostics

import (
	"context"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/tsic404/kosd/api/v1beta1"
	"github.com/tsic404/kosd/pkg/controlplane"
	"github.com/tsic404/kosd/pkg/identity"
	"github.com/tsic404/kosd/pkg/pages/cluster"
	volumeapi "github.com/tsic404/kosd/pkg/volume"
)

// Blocked is the blocked-objects item: the objects that hold progress — an
// application whose uninstall is stuck behind its blocked list, and a volume
// whose claim or deletion is blocked (DIAG-C-1, KOSD-C-33).
type Blocked struct {
	// Objects is one entry per blocking object, ordered by kind, namespace and
	// name.
	Objects []BlockedObject `json:"objects"`
}

// BlockedObject is one object that blocks progress. Kind decides what the
// entry's condition means: an Application carries its uninstall block, a
// Volume the condition that holds its claim or deletion (KOSD-C-33, KOSD-C-38).
type BlockedObject struct {
	// Kind is the object kind.
	Kind string `json:"kind"`
	// Namespace is empty for cluster-scoped objects.
	Namespace string `json:"namespace,omitempty"`
	// Name is the object name.
	Name string `json:"name"`
	// Stalled marks an uninstall blocked beyond the stall bound (KOSD-C-33).
	Stalled bool `json:"stalled,omitempty"`
	// Since is when the block started, in RFC 3339.
	Since string `json:"since,omitempty"`
	// Condition is the blocking condition the object carries.
	Condition string `json:"condition,omitempty"`
	// Objects names the objects that hold an uninstall, one reference per
	// entry.
	Objects []string `json:"objects,omitempty"`
	// Detail is the condition message that explains the block.
	Detail string `json:"detail,omitempty"`
}

// blocked lists the stuck uninstalls with the objects that hold them, plus the
// volumes whose blocking condition needs the same attention. The page reads the
// state the reconcilers wrote and never recomputes the stall bound (KOSD-C-33).
func (p *Product) blocked(ctx context.Context, session identity.Session) (any, *controlplane.Error) {
	applications, failure := p.applications(ctx, session)
	if failure != nil {
		return nil, failure
	}
	blocked := make([]BlockedObject, 0)
	for _, application := range applications {
		uninstall := application.Status.Uninstall
		if uninstall == nil || len(uninstall.Blocked) == 0 {
			continue
		}
		entry := BlockedObject{
			Kind: "Application", Namespace: application.Namespace, Name: application.Name,
			Stalled: uninstall.Stalled,
			Objects: make([]string, 0, len(uninstall.Blocked)),
		}
		if uninstall.BlockedSince != nil {
			entry.Since = uninstall.BlockedSince.UTC().Format(time.RFC3339)
		}
		for _, ref := range uninstall.Blocked {
			entry.Objects = append(entry.Objects, referenceOf(ref))
		}
		blocked = append(blocked, entry)
	}
	volumes, failure := p.reader().List(ctx, "Volume", "")
	if failure != nil {
		return nil, failure
	}
	for _, object := range volumes {
		var volume v1beta1.Volume
		if failure := cluster.Decode(object, &volume); failure != nil {
			return nil, failure
		}
		condition, found := blockingCondition(volume.Status.Conditions)
		if !found {
			continue
		}
		blocked = append(blocked, BlockedObject{
			Kind: "Volume", Namespace: volume.Namespace, Name: volume.Name,
			Condition: condition.Type, Detail: condition.Message,
		})
	}
	sort.Slice(blocked, func(left, right int) bool {
		if blocked[left].Kind != blocked[right].Kind {
			return blocked[left].Kind < blocked[right].Kind
		}
		if blocked[left].Namespace != blocked[right].Namespace {
			return blocked[left].Namespace < blocked[right].Namespace
		}
		return blocked[left].Name < blocked[right].Name
	})
	return Blocked{Objects: blocked}, nil
}

// referenceOf renders one blocking reference. The API group is part of the
// object class (KOSD-C-19), so two extensions declaring the same kind stay
// distinguishable (KOSD-C-33).
func referenceOf(ref v1beta1.ObjectRef) string {
	reference := ref.Kind + "/" + ref.Namespace + "/" + ref.Name
	if ref.Group != "" {
		reference = ref.Group + "/" + reference
	}
	return reference
}

// blockingCondition finds the first blocking or lost condition of a volume: a
// volume whose claim cannot bind, whose data is lost, or that an extension
// object still references must be visible to the user (KOSD-C-33, KOSD-C-38).
func blockingCondition(conditions []metav1.Condition) (metav1.Condition, bool) {
	for _, condition := range conditions {
		if condition.Status != metav1.ConditionTrue {
			continue
		}
		switch condition.Type {
		case volumeapi.ConditionClaimBlocked, volumeapi.ConditionLost, volumeapi.ConditionDeletionBlocked:
			return condition, true
		}
	}
	return metav1.Condition{}, false
}
