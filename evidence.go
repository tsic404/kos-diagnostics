package diagnostics

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/tsic404/kosd/pkg/controlplane"
	diagnostic "github.com/tsic404/kosd/pkg/diagnostics"
	"github.com/tsic404/kosd/pkg/identity"
)

// Evidence is the evidence-segments item: the chain of each abnormal
// application, segment by segment (KOSD-C-26).
type Evidence struct {
	// Applications is one entry per abnormal application, ordered by
	// namespace and name.
	Applications []ApplicationEvidence `json:"applications"`
}

// ApplicationEvidence is one application's evidence chain.
type ApplicationEvidence struct {
	// Namespace is the application's namespace.
	Namespace string `json:"namespace"`
	// Name is the application name.
	Name string `json:"name"`
	// Health is the application's health state.
	Health string `json:"health,omitempty"`
	// Chain is the evidence chain the control plane assembled (KOSD-C-26).
	Chain *diagnostic.Chain `json:"chain,omitempty"`
	// Error explains why no chain could be read.
	Error string `json:"error,omitempty"`
}

// evidence assembles the evidence chain of every abnormal application the
// session may see (KOSD-C-26). The chain itself stays in kosd; this product
// passes on the segments, their sources and their unobservable markers.
func (p *Product) evidence(ctx context.Context, session identity.Session) (any, *controlplane.Error) {
	applications, failure := p.applications(ctx, session)
	if failure != nil {
		return nil, failure
	}
	chains := p.chain()
	entries := make([]ApplicationEvidence, 0)
	for _, application := range applications {
		if !abnormal("Application", application.Status.ObjectStatus) {
			continue
		}
		entry := ApplicationEvidence{
			Namespace: application.Namespace, Name: application.Name,
			Health: string(application.Status.Health.State),
		}
		chain, err := chains.Chain(ctx, application.Namespace, application.Name)
		switch {
		case err == nil:
			entry.Chain = &chain
		case apierrors.IsNotFound(err):
			entry.Error = "应用在读取证据链时已不存在"
		default:
			entry.Error = "证据链读取失败"
		}
		entries = append(entries, entry)
	}
	return Evidence{Applications: entries}, nil
}
