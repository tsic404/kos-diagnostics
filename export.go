package diagnostics

import (
	"context"

	"github.com/tsic404/kosd/pkg/controlplane"
	"github.com/tsic404/kosd/pkg/identity"
)

// ExportTargets is the export item: the rule its value follows and the
// applications the control-plane bundle surface can assemble a document for
// (KOSD-C-12, KOSD-C-26).
type ExportTargets struct {
	// Rule is the rule the targets follow (KOSD-C-53, KOSD-C-59).
	Rule string `json:"rule"`
	// Applications is one entry per application the session may see, ordered by
	// namespace and name.
	Applications []ExportTarget `json:"applications"`
}

// exportRule is the rule the export target list follows: the export reads the
// applications the session may see and assembles each bundle at request time,
// without writing an object (DIAG-C-1).
const exportRule = "导出目标为当前会话可见的应用；诊断包读时组装，不写对象（KOSD-C-26、KOSD-C-59）"

// ExportTarget is one application a bundle can be exported for. It names the
// input object of the export action, so the computed item states what its rule
// was evaluated over (KOSD-C-53).
type ExportTarget struct {
	// Namespace is the application's namespace.
	Namespace string `json:"namespace"`
	// Name is the application name.
	Name string `json:"name"`
	// Health is the application's health state.
	Health string `json:"health,omitempty"`
}

// exportTargets lists the applications the export action reads. The action is
// a read: it assembles the bundle at request time and changes no object
// (DIAG-C-1).
func (p *Product) exportTargets(ctx context.Context, session identity.Session) (any, *controlplane.Error) {
	applications, failure := p.applications(ctx, session)
	if failure != nil {
		return nil, failure
	}
	targets := make([]ExportTarget, 0, len(applications))
	for _, application := range applications {
		targets = append(targets, ExportTarget{
			Namespace: application.Namespace, Name: application.Name,
			Health: string(application.Status.Health.State),
		})
	}
	return ExportTargets{Rule: exportRule, Applications: targets}, nil
}
