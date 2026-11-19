package diagnostics

import (
	"net/http"

	"github.com/tsic404/kosd/api"
	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/pkg/write"
)

// Identifier is the product's extension identity across versions (KOSD-C-17).
const Identifier = "kos-diagnostics"

// Version is the product version the declaration carries.
const Version = "1.0.0"

// FrameworkVersion is the framework API version range the declaration targets
// (KOSD-C-18). The kernel serves 1.1 (KOSD-C-16).
const FrameworkVersion = ">=1.1 <2.0"

// PageDiagnostics is the diagnostics page (DIAG-C-1).
const PageDiagnostics = "diagnostics"

// Item identities of the diagnostics page, one per displayed subject DIAG-C-1
// names.
const (
	ItemDegradation = "degradation-reasons"
	ItemBlocked     = "blocked-objects"
	ItemEvidence    = "evidence-segments"
	ItemExport      = "export-bundle"
)

// ActionExport is the page's only action: it assembles one application's
// diagnostic bundle at read time (DIAG-C-1, KOSD-C-26).
const ActionExport = "export-diagnostic-bundle"

// PathBundles is the control-plane export class the action writes to
// (KOSD-C-12).
const PathBundles = "/api/v1/diagnostics/bundles"

// Item reasons. The page reads evidence and exports a snapshot, so it offers no
// modification entry besides the export; each item says so and says why
// (DIAG-C-1, KOSD-C-53).
const (
	ReasonDegradation = "本页只读：降级原因按对象健康与证据链结论聚合计算，页面不写入；修正动作由应用页面提供"
	ReasonBlocked     = "本页只读：阻塞对象由应用与卷调和写入，页面不写入；处置动作由应用页面提供"
	ReasonEvidence    = "本页只读：证据沿引用链读取，页面不写入；修正动作由应用页面提供"
)

// Declaration returns the contribution kos-diagnostics ships (KOSD-C-18): the
// diagnostics page and its export entry.
func Declaration() extension.Declaration {
	return extension.Declaration{
		Identifier:       Identifier,
		Version:          Version,
		FrameworkVersion: FrameworkVersion,
		Contributions: extension.Contributions{
			Pages: []extension.Page{Page()},
		},
		Permissions: Permissions(),
	}
}

// Permissions declares what the page reads and the control-plane role it needs
// (KOSD-C-18). The page writes nothing: it presents evidence and exports a
// snapshot, so no entry names a write scope or an object writer (DIAG-C-1).
func Permissions() []extension.Permission {
	return []extension.Permission{{
		Contribution: PageDiagnostics,
		ReadScope: extension.ReadScope{
			// The page reads the kernel objects it names and the native kinds the
			// evidence chain walks behind them (KOSD-C-26).
			Groups: []string{api.Group, "", "apps", "batch", "networking.k8s.io"},
			Kinds: []extension.GroupKind{
				{Group: api.Group, Kind: "Application"},
				{Group: api.Group, Kind: "Endpoint"},
				{Group: api.Group, Kind: "Volume"},
				{Kind: "Event"},
				{Kind: "Node"},
				{Kind: "Pod"},
				{Kind: "Service"},
				{Group: "apps", Kind: "DaemonSet"},
				{Group: "apps", Kind: "Deployment"},
				{Group: "apps", Kind: "ReplicaSet"},
				{Group: "apps", Kind: "StatefulSet"},
				{Group: "batch", Kind: "CronJob"},
				{Group: "batch", Kind: "Job"},
				{Group: "networking.k8s.io", Kind: "Ingress"},
			},
		},
		// The page is read-only, so the lowest signed-in role is enough; the
		// reader still trims every object to the caller's visibility (KOSD-C-59).
		RequiredRole: string(write.RoleGuest),
	}}
}

// Page returns the declared diagnostics page. The export entry is the page's
// only action; the other items state why they carry none.
func Page() extension.Page {
	return extension.Page{
		ID:       PageDiagnostics,
		Title:    "诊断",
		Location: "panel",
		Items: []extension.PageItem{
			{
				ID: ItemDegradation, Label: "降级原因与建议动作",
				Source: extension.PageSourceComputed, Reference: reference(ItemDegradation),
				Reason: ReasonDegradation,
			},
			{
				ID: ItemBlocked, Label: "卸载卡住与阻塞对象",
				Source: extension.PageSourceObject, Reference: reference(ItemBlocked),
				Reason: ReasonBlocked,
			},
			{
				ID: ItemEvidence, Label: "各段证据",
				Source: extension.PageSourceObject, Reference: reference(ItemEvidence),
				Reason: ReasonEvidence,
			},
			{
				ID: ItemExport, Label: "诊断包导出",
				Source: extension.PageSourceComputed, Reference: reference(ItemExport),
				Action: ActionExport,
			},
		},
		Actions: []extension.Action{
			{ID: ActionExport, Method: http.MethodPost, Path: PathBundles},
		},
	}
}

// reference is the item reference this product owns; the page shell dispatches
// by the identifier in front of it (KOSD-C-53).
func reference(item string) string { return Identifier + "/" + item }
