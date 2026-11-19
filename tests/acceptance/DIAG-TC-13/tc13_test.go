// Package diagtc13 verifies DIAG-TC-13: stopping the extension that provides a
// diagnostic adapter marks only that adapter's segment unobservable — the other
// segments are still given — and the diagnostic bundle still carries the five
// content classes with the same chain (DIAG-AC-6, DIAG-C-1, KOSD-C-26,
// KOSD-C-28).
package diagtc13

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/tsic404/kosd/pkg/diagnostics"
	"github.com/tsic404/kosd/pkg/identity"
	"github.com/tsic404/kosd/pkg/pages"

	product "github.com/tsic404/kos-diagnostics"
	"github.com/tsic404/kos-diagnostics/tests/acceptance/fixture"
)

func TestTC13AdapterUnavailable(t *testing.T) {
	world := fixture.Start(t)
	session := fixture.Session()
	ctx := context.Background()

	// The extension segment is observable while its adapter is active.
	before := chainOf(t, evidenceOf(t, world.NewShell(t).Service(), ctx, session), fixture.Application)
	widget := segmentOf(t, before, isWidget)
	if !widget.Observable {
		t.Fatalf("the extension segment is unobservable while the adapter is active: %s", widget.Reason)
	}

	// The install projection loses the adapter with its component (KOSD-C-28);
	// the page is re-read over the new projection.
	world.DisableAdapter()
	after := chainOf(t, evidenceOf(t, world.NewShell(t).Service(), ctx, session), fixture.Application)

	// The kernel links are unchanged: only the adapter's own segment changed.
	if beforeLinks, afterLinks := kernelLinks(before), kernelLinks(after); !reflect.DeepEqual(beforeLinks, afterLinks) {
		t.Fatalf("kernel links = %v after the stop, want %v", afterLinks, beforeLinks)
	}
	// The segment keeps its place in the chain and is marked unobservable: a
	// chain that silently drops it fails here (KOSD-C-26).
	stopped := segmentsWhere(after, isWidget)
	if len(stopped) != 1 {
		t.Fatalf("extension segments after the stop = %d, want exactly one kept in place", len(stopped))
	}
	if stopped[0].Observable {
		t.Fatal("the extension segment is observable after its adapter stopped")
	}
	if stopped[0].Reason == "" {
		t.Fatal("the unobservable segment names no reason")
	}
	if len(stopped[0].Evidence) != 0 {
		t.Fatalf("the unobservable segment carries %d evidence entries", len(stopped[0].Evidence))
	}
	for _, segment := range after.Segments {
		switch {
		case isWidget(segment):
			continue
		case segment.Object.Kind == "SystemComponent":
			t.Fatalf("a component segment remains after its adapter stopped: %v", segment.Object)
		default:
			if !segment.Observable || len(segment.Evidence) == 0 {
				t.Fatalf("segment %s stopped being observable: %s", segment.Link, segment.Reason)
			}
		}
	}

	// The bundle still carries its five content classes and the same chain,
	// exported through the entry the page still offers (DIAG-AC-44, KOSD-C-12).
	shell := world.NewShell(t)
	page, failure := shell.Service().Read(ctx, session, product.PageDiagnostics)
	if failure != nil {
		t.Fatalf("read the diagnostics page after the stop: %v", failure)
	}
	if len(page.Actions) != 1 || page.Actions[0].Entry.Path != product.PathBundles {
		t.Fatalf("export entry = %+v, want the control-plane bundle surface", page.Actions)
	}
	status, _, body := world.Export(t, page.Actions[0].Entry.Method, page.Actions[0].Entry.Path, map[string]any{
		"namespace": fixture.Namespace, "name": fixture.Application,
	})
	if status != http.StatusOK {
		t.Fatalf("export = %d %s", status, body)
	}
	document := fixture.DocumentOf(t, body)
	for _, key := range []string{"conditionSnapshot", "recentEvents", "renderDiff", "capabilities", "versionMatrix"} {
		if _, found := document[key]; !found {
			t.Fatalf("the bundle carries no %q content class", key)
		}
	}
	bundle := diagnostics.Bundle{}
	if err := json.Unmarshal(body, &bundle); err != nil {
		t.Fatalf("decode the diagnostic bundle: %v", err)
	}
	if len(bundle.ConditionSnapshot) == 0 {
		t.Fatal("the bundle carries no condition snapshot")
	}
	if !bundle.RecentEvents.Available || len(bundle.RecentEvents.Events) == 0 {
		t.Fatalf("the bundle carries no recent events: %+v", bundle.RecentEvents)
	}
	if !bundle.RenderDiff.Available {
		t.Fatalf("the bundle carries no render diff: %s", bundle.RenderDiff.Reason)
	}
	if len(bundle.Capabilities) == 0 {
		t.Fatal("the bundle carries no capability list")
	}
	if len(bundle.VersionMatrix.KernelAPIVersions) == 0 || len(bundle.VersionMatrix.Nodes) == 0 ||
		len(bundle.VersionMatrix.Extensions) == 0 {
		t.Fatalf("the bundle carries no version matrix: %+v", bundle.VersionMatrix)
	}
	inBundle := segmentsWhere(bundle.Evidence, isWidget)
	if len(inBundle) != 1 {
		t.Fatalf("bundle extension segments = %d, want exactly one kept in place", len(inBundle))
	}
	if inBundle[0].Observable || len(inBundle[0].Evidence) != 0 {
		t.Fatalf("the bundle's extension segment is observable: %+v", inBundle[0])
	}
}

// evidenceOf reads the evidence item over one page shell.
func evidenceOf(t *testing.T, service *pages.Service, ctx context.Context, session identity.Session) product.Evidence {
	t.Helper()
	page, failure := service.Read(ctx, session, product.PageDiagnostics)
	if failure != nil {
		t.Fatalf("read the diagnostics page: %v", failure)
	}
	evidence, ok := itemValue(page, product.ItemEvidence).(product.Evidence)
	if !ok {
		t.Fatalf("evidence item value = %T", itemValue(page, product.ItemEvidence))
	}
	return evidence
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

// itemValue returns one rendered item's value.
func itemValue(page pages.Page, id string) any {
	for _, item := range page.Items {
		if item.ID == id {
			return item.Value
		}
	}
	return nil
}

// segmentsWhere returns the segments a predicate selects, so a case can require
// a segment to stay in place instead of only checking the ones that remain.
func segmentsWhere(chain diagnostics.Chain, match func(segment diagnostics.Segment) bool) []diagnostics.Segment {
	segments := make([]diagnostics.Segment, 0, len(chain.Segments))
	for _, segment := range chain.Segments {
		if match(segment) {
			segments = append(segments, segment)
		}
	}
	return segments
}

// kernelLinks returns the links of the segments that observed kernel objects, in
// chain order.
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

// segmentOf returns the one segment a predicate selects.
func segmentOf(t *testing.T, chain diagnostics.Chain, match func(segment diagnostics.Segment) bool) diagnostics.Segment {
	t.Helper()
	for _, segment := range chain.Segments {
		if match(segment) {
			return segment
		}
	}
	t.Fatalf("no segment matched among %d segments", len(chain.Segments))
	return diagnostics.Segment{}
}

// isWidget selects the extension object's segment.
func isWidget(segment diagnostics.Segment) bool {
	return segment.Object.Group == fixture.WidgetGroup && segment.Object.Kind == "Widget"
}
