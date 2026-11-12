package diagnostics

import (
	"context"
	"sort"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/tsic404/kosd/api/v1beta1"
	"github.com/tsic404/kosd/pkg/controlplane"
	diagnostic "github.com/tsic404/kosd/pkg/diagnostics"
	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/pkg/identity"
	"github.com/tsic404/kosd/pkg/pages"
	"github.com/tsic404/kosd/pkg/pages/cluster"
)

// Product is the kos-diagnostics component: its declaration (KOSD-C-18) and the
// reader of the items it declares (KOSD-C-53). The host builds one per active
// component and routes the page's item references to it.
type Product struct {
	// Dynamic reads cluster objects through the control plane (KOSD-C-12).
	Dynamic dynamic.Interface
	// Environment is the extension state the evidence chain and the bundle
	// report (KOSD-C-26); an empty value leaves extension segments
	// unobservable, which the chain states instead of hiding.
	Environment diagnostic.Environment
	// Now supplies the read clock; nil selects time.Now.
	Now func() time.Time
}

// Read resolves one declared item from the source it names. It is the reader
// the page shell calls for every item of the diagnostics page.
func (p *Product) Read(ctx context.Context, session identity.Session, item extension.PageItem) (pages.Reading, *controlplane.Error) {
	value, failure := p.value(ctx, session, item)
	if failure != nil {
		return pages.Reading{}, failure
	}
	return pages.Reading{Value: value, ObservedAt: p.clock()}, nil
}

// value resolves one item from the source it declares.
func (p *Product) value(ctx context.Context, session identity.Session, item extension.PageItem) (any, *controlplane.Error) {
	switch item.ID {
	case ItemDegradation:
		return p.degradation(ctx, session)
	case ItemBlocked:
		return p.blocked(ctx, session)
	case ItemEvidence:
		return p.evidence(ctx, session)
	case ItemExport:
		return p.exportTargets(ctx, session)
	default:
		return nil, unknownItem(item.ID)
	}
}

// reader is the cluster read seam for this call.
func (p *Product) reader() *cluster.Reader {
	return &cluster.Reader{Dynamic: p.Dynamic, Now: p.Now}
}

// chain reads the evidence chain and the bundle the control plane assembles
// (KOSD-C-12, KOSD-C-26). Assembly stays in kosd; this product only reads it.
func (p *Product) chain() *diagnostic.Service {
	return &diagnostic.Service{
		Source:      diagnostic.ClusterSource{Dynamic: p.Dynamic, Now: p.Now},
		Environment: p.Environment,
		Now:         p.Now,
	}
}

// clock is the moment a read observes its values (KOSD-C-13).
func (p *Product) clock() time.Time {
	if p.Now == nil {
		return time.Now()
	}
	return p.Now()
}

// applications lists the applications the session may see, ordered by namespace
// and name (KOSD-C-59).
func (p *Product) applications(ctx context.Context, session identity.Session) ([]v1beta1.Application, *controlplane.Error) {
	objects, failure := p.reader().List(ctx, "Application", "")
	if failure != nil {
		return nil, failure
	}
	applications := make([]v1beta1.Application, 0, len(objects))
	for _, object := range objects {
		// Visibility is judged on the stored object, so an application the
		// session may not see never fails its read by being malformed
		// (KOSD-C-59).
		visible, failure := p.reader().Visible(ctx, session, "Application", object)
		if failure != nil {
			return nil, failure
		}
		if !visible {
			continue
		}
		var application v1beta1.Application
		if failure := cluster.Decode(object, &application); failure != nil {
			return nil, failure
		}
		applications = append(applications, application)
	}
	sort.Slice(applications, func(left, right int) bool {
		if applications[left].Namespace != applications[right].Namespace {
			return applications[left].Namespace < applications[right].Namespace
		}
		return applications[left].Name < applications[right].Name
	})
	return applications, nil
}

// unknownItem marks an item this product does not declare.
func unknownItem(id string) *controlplane.Error {
	return &controlplane.Error{
		Code:        controlplane.CodeCapabilityMissing,
		Message:     "kos-diagnostics 没有声明页面条目 " + id,
		Stage:       controlplane.StageCapability,
		SideEffects: controlplane.SideEffectsNone,
	}
}
