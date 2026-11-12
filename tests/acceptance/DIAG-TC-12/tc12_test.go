// Package diagtc12 verifies DIAG-TC-12: an application whose entry is unreachable
// is shown segment by segment along the reference path, the extension object's
// segment sits at the link its adapter declares and names the declared source
// with an observation time, and every evidence entry tells fact from inference
// while the reason points at a concrete object (DIAG-AC-6, DIAG-C-1,
// KOSD-C-26).
package diagtc12

import (
	"context"
	"testing"

	"github.com/tsic404/kosd/pkg/diagnostics"
	"github.com/tsic404/kosd/pkg/pages"

	product "github.com/tsic404/kos-diagnostics"
	"github.com/tsic404/kos-diagnostics/tests/acceptance/fixture"
)

func TestTC12EvidenceChain(t *testing.T) {
	world := fixture.Start(t)
	page, failure := world.NewShell(t).Service().Read(context.Background(), fixture.Session(), product.PageDiagnostics)
	if failure != nil {
		t.Fatalf("read the diagnostics page: %v", failure)
	}

	evidence, ok := itemValue(page, product.ItemEvidence).(product.Evidence)
	if !ok {
		t.Fatalf("evidence item value = %T", itemValue(page, product.ItemEvidence))
	}
	chain := chainOf(t, evidence, fixture.Application)

	// The kernel links keep KOSD-C-26's order: application, endpoint, route,
	// backend, workload and node.
	kernel := kernelLinks(chain)
	wantKernel := []diagnostics.Link{
		diagnostics.LinkApplication, diagnostics.LinkEndpoint, diagnostics.LinkRoute,
		diagnostics.LinkBackend, diagnostics.LinkWorkload, diagnostics.LinkNode,
	}
	if len(kernel) != len(wantKernel) {
		t.Fatalf("kernel links = %v, want %v", kernel, wantKernel)
	}
	for index, link := range wantKernel {
		if kernel[index] != link {
			t.Fatalf("kernel links = %v, want %v", kernel, wantKernel)
		}
	}

	// Every observable segment states the six elements and whether it is
	// observation or inference (KOSD-C-26).
	bases := map[diagnostics.Basis]bool{}
	for _, segment := range chain.Segments {
		if !segment.Observable {
			t.Fatalf("segment %s is unobservable: %s", segment.Link, segment.Reason)
		}
		if len(segment.Evidence) == 0 {
			t.Fatalf("segment %s carries no evidence", segment.Link)
		}
		if segment.Object.Name == "" {
			t.Fatalf("segment %s names no object", segment.Link)
		}
		for _, entry := range segment.Evidence {
			if entry.Conclusion == "" || entry.Source == "" || entry.Object.Name == "" ||
				entry.ObservedAt.IsZero() || entry.RawState == "" || entry.Impact == "" || entry.NextAction == "" {
				t.Fatalf("segment %s carries an incomplete evidence entry: %+v", segment.Link, entry)
			}
			if entry.Basis != diagnostics.BasisFact && entry.Basis != diagnostics.BasisInference {
				t.Fatalf("segment %s evidence basis = %q", segment.Link, entry.Basis)
			}
			bases[entry.Basis] = true
		}
	}
	if !bases[diagnostics.BasisFact] || !bases[diagnostics.BasisInference] {
		t.Fatalf("evidence bases = %v, want both a fact and an inference", bases)
	}

	// The extension object inserts its segment at the declared link, after the
	// kernel workload segment, and names the declared source (KOSD-C-26).
	widgetIndex := segmentIndex(chain, func(segment diagnostics.Segment) bool {
		return segment.Object.Group == fixture.WidgetGroup && segment.Object.Kind == "Widget"
	})
	if widgetIndex < 0 {
		t.Fatalf("the chain carries no %s/Widget segment", fixture.WidgetGroup)
	}
	widget := chain.Segments[widgetIndex]
	if widget.Object.Name != fixture.WidgetName {
		t.Fatalf("extension segment object = %v", widget.Object)
	}
	if widget.Link != diagnostics.LinkWorkload {
		t.Fatalf("extension segment link = %q, want the declared %q", widget.Link, diagnostics.LinkWorkload)
	}
	if widget.Evidence[0].Source != fixture.AdapterSource {
		t.Fatalf("extension evidence source = %q, want the declared source", widget.Evidence[0].Source)
	}
	if !widget.Evidence[0].ObservedAt.Equal(world.Now) {
		t.Fatalf("extension evidence observedAt = %v, want %v", widget.Evidence[0].ObservedAt, world.Now)
	}
	workloadIndex := segmentIndex(chain, func(segment diagnostics.Segment) bool {
		return segment.Link == diagnostics.LinkWorkload && segment.Object.Kind == "Deployment"
	})
	if workloadIndex < 0 || workloadIndex > widgetIndex {
		t.Fatalf("workload segment index = %d, extension index = %d, want the extension after the workload", workloadIndex, widgetIndex)
	}

	// The adapter covers its own component, which becomes a segment of its own
	// at the same link (KOSD-C-26).
	componentIndex := segmentIndex(chain, func(segment diagnostics.Segment) bool {
		return segment.Object.Kind == "SystemComponent"
	})
	if componentIndex < 0 {
		t.Fatal("the chain carries no component segment")
	}
	component := chain.Segments[componentIndex]
	if component.Object.Name != fixture.ComponentName {
		t.Fatalf("component segment object = %v, want the system component", component.Object)
	}
	if component.Link != diagnostics.LinkWorkload {
		t.Fatalf("component segment link = %q, want the declared %q", component.Link, diagnostics.LinkWorkload)
	}
	if component.Evidence[0].Source != fixture.AdapterSource {
		t.Fatalf("component evidence source = %q", component.Evidence[0].Source)
	}

	// The reason points at the deepest observed object and segment, so a
	// degradation reason is locatable to a concrete object and source.
	if chain.Conclusion.Basis != diagnostics.BasisFact {
		t.Fatalf("conclusion basis = %q, want a fact", chain.Conclusion.Basis)
	}
	if chain.Conclusion.Segment != diagnostics.LinkNode {
		t.Fatalf("conclusion segment = %q, want %q", chain.Conclusion.Segment, diagnostics.LinkNode)
	}
	if chain.Conclusion.Object.Kind != "Node" || chain.Conclusion.Object.Name != fixture.NodeName {
		t.Fatalf("conclusion object = %v, want the NotReady node", chain.Conclusion.Object)
	}
	if chain.Conclusion.NextAction == "" {
		t.Fatal("conclusion names no next action")
	}

	// The page's degradation item carries the same conclusion as its computed
	// reason for the application (KOSD-C-53).
	degradation, ok := itemValue(page, product.ItemDegradation).(product.Degradation)
	if !ok {
		t.Fatalf("degradation item value = %T", itemValue(page, product.ItemDegradation))
	}
	reason := reasonOf(t, degradation, fixture.Application)
	if reason.Condition != string(diagnostics.LinkNode) || reason.Reason != chain.Conclusion.Summary {
		t.Fatalf("degradation reason = %+v, want the chain conclusion", reason)
	}
}

// kernelLinks returns the links of the segments that observed kernel objects, in
// chain order. Extension evidence shares a link with the kernel segment it
// attaches to, so the object decides which is which.
func kernelLinks(chain diagnostics.Chain) []diagnostics.Link {
	kinds := map[string]bool{
		"Application": true, "Endpoint": true, "Ingress": true,
		"Service": true, "Deployment": true, "Node": true,
	}
	links := make([]diagnostics.Link, 0, len(chain.Segments))
	for _, segment := range chain.Segments {
		if kinds[segment.Object.Kind] {
			links = append(links, segment.Link)
		}
	}
	return links
}

// segmentIndex returns the position of the first segment a predicate selects.
func segmentIndex(chain diagnostics.Chain, match func(segment diagnostics.Segment) bool) int {
	for index, segment := range chain.Segments {
		if match(segment) {
			return index
		}
	}
	return -1
}

// itemValue returns one rendered item's value.
func itemValue(page pages.Page, id string) any {
	for _, item := range page.Items {
		if item.ID == id {
			return item.Value
		}
	}
	return nil
}

// chainOf returns one application's evidence chain.
func chainOf(t *testing.T, evidence product.Evidence, name string) diagnostics.Chain {
	t.Helper()
	for _, entry := range evidence.Applications {
		if entry.Name != name {
			continue
		}
		if entry.Chain == nil {
			t.Fatalf("application %s carries no chain: %s", name, entry.Error)
		}
		return *entry.Chain
	}
	t.Fatalf("evidence carries no chain for %s", name)
	return diagnostics.Chain{}
}

// reasonOf returns one application's degradation reason.
func reasonOf(t *testing.T, degradation product.Degradation, name string) product.ReasonView {
	t.Helper()
	for _, reason := range degradation.Reasons {
		if reason.Kind == "Application" && reason.Name == name {
			return reason
		}
	}
	t.Fatalf("degradation carries no reason for %s", name)
	return product.ReasonView{}
}
