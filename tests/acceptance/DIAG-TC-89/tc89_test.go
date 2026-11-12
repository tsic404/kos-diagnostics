// Package diagtc89 verifies DIAG-TC-89: the diagnostics page's export entry
// assembles a bundle for a degraded application that carries the five content
// classes, locates the degradation reason at a concrete object, and carries
// neither log content nor a credential value; the export leaves every stored
// object field-identical (DIAG-AC-44, DIAG-C-1, KOSD-C-14, KOSD-C-26,
// KOSD-C-64).
package diagtc89

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsic404/kosd/pkg/diagnostics"
	"github.com/tsic404/kosd/pkg/pages"

	product "github.com/tsic404/kos-diagnostics"
	"github.com/tsic404/kos-diagnostics/tests/acceptance/fixture"
)

func TestTC89DiagnosticBundleContents(t *testing.T) {
	world := fixture.Start(t)
	ctx := context.Background()

	page, failure := world.NewShell(t).Service().Read(ctx, fixture.Session(), product.PageDiagnostics)
	if failure != nil {
		t.Fatalf("read the diagnostics page: %v", failure)
	}

	// The page's only action is the export entry, and it names the control-plane
	// bundle surface (DIAG-C-1, KOSD-C-12).
	if len(page.Actions) != 1 || page.Actions[0].Entry.Method != http.MethodPost ||
		page.Actions[0].Entry.Path != product.PathBundles {
		t.Fatalf("export entry = %+v, want POST %s", page.Actions, product.PathBundles)
	}
	exportItem := itemOf(t, page, product.ItemExport)
	if exportItem.Entry == nil || exportItem.Entry.Path != product.PathBundles {
		t.Fatalf("export item entry = %+v, want the control-plane export surface", exportItem.Entry)
	}
	targets, ok := exportItem.Value.(product.ExportTargets)
	if !ok {
		t.Fatalf("export item value = %T", exportItem.Value)
	}
	if !hasTarget(targets, fixture.Namespace, fixture.Application) {
		t.Fatalf("export targets = %+v, want the degraded application", targets.Applications)
	}

	// Exporting through the entry the page offers reads: it changes no object,
	// field by field.
	before := world.Snapshot(t)
	status, headers, encoded := world.Export(t, exportItem.Entry.Method, exportItem.Entry.Path, map[string]any{
		"namespace": fixture.Namespace, "name": fixture.Application,
	})
	if status != http.StatusOK {
		t.Fatalf("export = %d %s", status, encoded)
	}
	if disposition := headers.Get("Content-Disposition"); !strings.Contains(disposition, "attachment") ||
		!strings.Contains(disposition, "diagnostic-bundle-"+fixture.Application+".json") {
		t.Fatalf("Content-Disposition = %q, want an attachment named for the application", disposition)
	}
	if after := world.Snapshot(t); after != before {
		t.Fatal("the export changed stored objects")
	}
	bundle := diagnostics.Bundle{}
	if err := json.Unmarshal(encoded, &bundle); err != nil {
		t.Fatalf("decode the diagnostic bundle: %v", err)
	}

	// The export is a document a user keeps.
	target := filepath.Join(t.TempDir(), "diagnostic-bundle.json")
	if err := os.WriteFile(target, encoded, 0o600); err != nil {
		t.Fatalf("write the diagnostic bundle: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Size() == 0 {
		t.Fatalf("the diagnostic bundle file is missing or empty: %v", err)
	}

	// The document carries the five content classes plus the evidence chain
	// (KOSD-C-26).
	document := map[string]any{}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decode the diagnostic bundle: %v", err)
	}
	for _, key := range []string{"application", "exportedAt", "evidence"} {
		if _, found := document[key]; !found {
			t.Fatalf("the bundle carries no %q", key)
		}
	}
	for _, key := range []string{"conditionSnapshot", "recentEvents", "renderDiff", "capabilities", "versionMatrix"} {
		if _, found := document[key]; !found {
			t.Fatalf("the bundle carries no %q content class", key)
		}
	}
	for _, forbidden := range []string{"logs", "log", "logLines"} {
		if _, found := document[forbidden]; found {
			t.Fatalf("the bundle carries %q; logs never enter the bundle", forbidden)
		}
	}
	// The document's field set is exactly the exported shape: no other class,
	// and no log content, can ride along (KOSD-C-26, KOSD-C-64).
	allowed := map[string]bool{
		"application": true, "exportedAt": true,
		"conditionSnapshot": true, "recentEvents": true, "renderDiff": true,
		"capabilities": true, "versionMatrix": true, "evidence": true,
		"degradation": true,
	}
	for key := range document {
		if !allowed[key] {
			t.Fatalf("the bundle carries an undocumented field %q", key)
		}
	}

	// The five classes are readable, and the reason locates a concrete object.
	if len(bundle.ConditionSnapshot) == 0 {
		t.Fatal("the condition snapshot is empty")
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
	if len(bundle.VersionMatrix.KernelAPIVersions) == 0 || len(bundle.VersionMatrix.Extensions) == 0 ||
		len(bundle.VersionMatrix.Nodes) == 0 {
		t.Fatalf("the bundle carries no version matrix: %+v", bundle.VersionMatrix)
	}
	if bundle.Evidence.Conclusion.Object.Kind != "Node" || bundle.Evidence.Conclusion.Object.Name != fixture.NodeName {
		t.Fatalf("bundle conclusion = %+v, want the NotReady node", bundle.Evidence.Conclusion)
	}
	if bundle.Evidence.Conclusion.NextAction == "" {
		t.Fatal("the bundle conclusion names no next action")
	}

	// The render diff reports field paths, never a value, and a credential
	// object is omitted (KOSD-C-14).
	drifted := false
	omittedSecret := false
	for _, entry := range bundle.RenderDiff.Entries {
		if entry.Object.Kind == "Secret" {
			omittedSecret = entry.Difference == diagnostics.DifferenceOmitted && entry.Reason != ""
		}
		if entry.Difference == diagnostics.DifferenceDrifted && len(entry.Fields) > 0 {
			drifted = true
			for _, field := range entry.Fields {
				if strings.Contains(field, fixture.ConfigValue) || strings.Contains(field, fixture.Credential) {
					t.Fatalf("render diff field %q carries a value", field)
				}
			}
		}
	}
	if !drifted {
		t.Fatal("the render diff names no drifted object")
	}
	if !omittedSecret {
		t.Fatal("the render diff does not omit the credential object")
	}

	// Neither credentials nor log content reach the bundle or its evidence
	// (KOSD-C-14, KOSD-C-64).
	for _, secret := range []string{fixture.Credential, fixture.ConfigValue} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("the bundle carries the sensitive value %q", secret)
		}
		for _, segment := range bundle.Evidence.Segments {
			for _, entry := range segment.Evidence {
				if strings.Contains(entry.RawState, secret) || strings.Contains(entry.Conclusion, secret) {
					t.Fatalf("evidence carries the sensitive value %q", secret)
				}
			}
		}
	}
	for _, entry := range bundle.RecentEvents.Events {
		if strings.Contains(entry.Message, fixture.Credential) || strings.Contains(entry.Message, fixture.ConfigValue) {
			t.Fatalf("an event carries a sensitive value: %+v", entry)
		}
	}

	// The page's degradation item points at the same segment and object the
	// bundle's chain reports (KOSD-C-53).
	degradation, ok := itemOf(t, page, product.ItemDegradation).Value.(product.Degradation)
	if !ok {
		t.Fatalf("degradation item value = %T", itemOf(t, page, product.ItemDegradation).Value)
	}
	reason := reasonOf(t, degradation, fixture.Application)
	if reason.Condition != string(bundle.Evidence.Conclusion.Segment) || reason.Reason != bundle.Evidence.Conclusion.Summary {
		t.Fatalf("degradation reason = %+v, want the bundle's conclusion", reason)
	}
}

// itemOf returns one rendered item.
func itemOf(t *testing.T, page pages.Page, id string) pages.Item {
	t.Helper()
	for _, item := range page.Items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("page carries no item %q", id)
	return pages.Item{}
}

// hasTarget reports whether the export item names one application.
func hasTarget(targets product.ExportTargets, namespace, name string) bool {
	for _, target := range targets.Applications {
		if target.Namespace == namespace && target.Name == name {
			return true
		}
	}
	return false
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
