// Package diagtc1 verifies DIAG-TC-1: every display item of the diagnostics page
// carries its source type and the source that resolves it, the degradation item
// states the rule and the objects it judged, the blocked item lists the concrete
// objects that hold an uninstall, the page offers no modification entry besides
// the export, exporting leaves every object field-identical, and disabling the
// component withdraws the page and its export entry while another product's page
// stays visible (DIAG-AC-1, DIAG-C-1, KOSD-C-28, KOSD-C-53).
package diagtc1

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/pkg/pages"

	product "github.com/tsic404/kos-diagnostics"
	"github.com/tsic404/kos-diagnostics/tests/acceptance/fixture"
)

func TestTC1DiagnosticsPageAndExport(t *testing.T) {
	world := fixture.Start(t)
	shell := world.NewShell(t)
	service := shell.Service()
	session := fixture.Session()
	ctx := context.Background()

	// The page and the other product's page are both listed.
	listed := pageIDs(service.List(session))
	if !slices.Contains(listed, product.PageDiagnostics) {
		t.Fatalf("pages = %v, want the diagnostics page", listed)
	}
	if !slices.Contains(listed, fixture.OtherPageID) {
		t.Fatalf("pages = %v, want the other product's page", listed)
	}

	// Every server item declares the source that resolves it, and the source
	// belongs to this product: the shell routes the reference by that prefix
	// (KOSD-C-53).
	declared, found := shell.Projection.PageFor(product.PageDiagnostics, fixture.Session().Role)
	if !found {
		t.Fatal("the install projection does not carry the diagnostics page")
	}
	for _, item := range declared.Items {
		if item.Source == extension.PageSourceLocal {
			continue
		}
		if !strings.HasPrefix(item.Reference, product.Identifier+"/") {
			t.Errorf("item %q reference = %q, want a source owned by %s", item.ID, item.Reference, product.Identifier)
		}
	}

	page, failure := service.Read(ctx, session, product.PageDiagnostics)
	if failure != nil {
		t.Fatalf("read the diagnostics page: %v", failure)
	}
	document := fixture.Document(t, page)
	items := itemsOf(t, document)

	// Each item names its source type and was observed by a server source
	// (KOSD-C-53, KOSD-C-13).
	wantSources := map[string]string{
		product.ItemDegradation: extension.PageSourceComputed,
		product.ItemBlocked:     extension.PageSourceObject,
		product.ItemEvidence:    extension.PageSourceObject,
		product.ItemExport:      extension.PageSourceComputed,
	}
	if len(items) != len(wantSources) {
		t.Fatalf("page carries %d items, want %d", len(items), len(wantSources))
	}
	for id, source := range wantSources {
		item, known := items[id]
		if !known {
			t.Fatalf("page is missing item %q", id)
		}
		if item["source"] != source {
			t.Errorf("item %q source = %v, want %q", id, item["source"], source)
		}
		if text(item, "observedAt") == "" {
			t.Errorf("item %q carries no observation time", id)
		}
		if item["stale"] == true {
			t.Errorf("item %q is marked stale under the read clock", id)
		}
		if item["value"] == nil {
			t.Errorf("item %q carries no value", id)
		}
	}

	// The page states it is read-only and why: every item without a
	// modification entry carries that statement and no entry (DIAG-C-1,
	// KOSD-C-53).
	for id, item := range items {
		if id == product.ItemExport {
			continue
		}
		if item["entry"] != nil {
			t.Errorf("item %q carries a modification entry on a read-only item", id)
		}
		if !strings.Contains(text(item, "reason"), "只读") {
			t.Errorf("item %q reason = %q, want the stated read-only reason", id, text(item, "reason"))
		}
	}

	// The export is the page's only action and the export item's entry; it is
	// a control-plane write entry (DIAG-C-1, KOSD-C-12).
	actions, _ := document["actions"].([]any)
	if len(actions) != 1 {
		t.Fatalf("page actions = %v, want exactly the export", actions)
	}
	action, _ := actions[0].(map[string]any)
	if action["id"] != product.ActionExport {
		t.Fatalf("action = %v, want %v", action, product.ActionExport)
	}
	entry := entryOf(t, items, product.ItemExport)
	if text(entry, "method") != http.MethodPost || text(entry, "path") != product.PathBundles {
		t.Fatalf("export entry = %v, want POST %s", entry, product.PathBundles)
	}
	if items[product.ItemExport]["reason"] != nil {
		t.Fatalf("export item carries reason %v beside its entry", items[product.ItemExport]["reason"])
	}

	// The degradation item states the rule and the objects it judged; the
	// application's reason comes from its evidence chain, another kind's from
	// its own condition (KOSD-C-53, KOSD-C-26).
	degradation := valueOf(t, items, product.ItemDegradation)
	if strings.TrimSpace(text(degradation, "rule")) == "" {
		t.Fatal("degradation item states no rule")
	}
	applicationReason := reasonOf(t, degradation, "Application", fixture.Namespace, fixture.Application)
	if text(applicationReason, "health") != "Degraded" {
		t.Fatalf("application reason = %v, want the degraded health", applicationReason)
	}
	if strings.TrimSpace(text(applicationReason, "nextAction")) == "" {
		t.Fatalf("application reason = %v, want a next action", applicationReason)
	}
	if text(applicationReason, "reason") == "" {
		t.Fatalf("application reason = %v, want the chain's conclusion", applicationReason)
	}
	if basis := text(applicationReason, "basis"); basis != "Fact" && basis != "Inference" {
		t.Fatalf("application reason basis = %q, want fact or inference", basis)
	}
	jobReason := reasonOf(t, degradation, "RestoreJob", fixture.Namespace, "restore-1")
	if text(jobReason, "condition") != "VerificationFailed" || text(jobReason, "basis") != "Fact" {
		t.Fatalf("restore job reason = %v, want its failed condition as an observed fact", jobReason)
	}
	if strings.TrimSpace(text(jobReason, "nextAction")) == "" {
		t.Fatalf("restore job reason = %v, want a next action", jobReason)
	}

	// The blocked item names the stuck uninstall and the concrete objects that
	// hold it, plus the volume whose claim is blocked (KOSD-C-33, KOSD-C-38).
	blocked := valueOf(t, items, product.ItemBlocked)
	stuck := blockedObjectOf(t, blocked, "Application", fixture.Namespace, fixture.BlockedApplication)
	if stuck["stalled"] != true || text(stuck, "since") == "" {
		t.Fatalf("blocked uninstall = %v, want a stalled uninstall with its start", stuck)
	}
	for _, want := range []string{
		"apps/Deployment/" + fixture.Namespace + "/" + fixture.BlockedApplication,
		"Volume/" + fixture.Namespace + "/" + fixture.BlockedVolume,
	} {
		if !slices.Contains(objectsOf(t, stuck), want) {
			t.Errorf("blocked objects = %v, want %q", objectsOf(t, stuck), want)
		}
	}
	blockedVolume := blockedObjectOf(t, blocked, "Volume", fixture.Namespace, fixture.BlockedVolume)
	if text(blockedVolume, "condition") != "ClaimBlocked" {
		t.Fatalf("blocked volume = %v, want its blocking condition", blockedVolume)
	}
	if strings.TrimSpace(text(blockedVolume, "detail")) == "" {
		t.Fatalf("blocked volume = %v, want the condition message that explains the block", blockedVolume)
	}

	// The evidence item carries the degraded application's chain segment by
	// segment (KOSD-C-26).
	evidence := valueOf(t, items, product.ItemEvidence)
	chain := chainOf(t, evidence, fixture.Application)
	segments, _ := chain["segments"].([]any)
	if len(segments) == 0 {
		t.Fatalf("evidence chain = %v, want its segments", chain)
	}

	// The export item names the applications a bundle can be exported for, and
	// the rule its value follows (KOSD-C-53).
	targets := valueOf(t, items, product.ItemExport)
	if strings.TrimSpace(text(targets, "rule")) == "" {
		t.Fatal("export item states no rule")
	}
	for _, name := range []string{fixture.Application, fixture.BlockedApplication} {
		if !hasTarget(targets, fixture.Namespace, name) {
			t.Errorf("export targets = %v, want %s/%s", targets["applications"], fixture.Namespace, name)
		}
	}

	// Exporting through the entry the page offers changes no object, field by
	// field (DIAG-C-1).
	before := world.Snapshot(t)
	status, headers, body := world.Export(t, text(entry, "method"), text(entry, "path"), map[string]any{
		"namespace": fixture.Namespace, "name": fixture.Application,
	})
	if status != http.StatusOK {
		t.Fatalf("export = %d %s", status, body)
	}
	if disposition := headers.Get("Content-Disposition"); !strings.Contains(disposition, "attachment") {
		t.Fatalf("Content-Disposition = %q, want an attachment", disposition)
	}
	exported := fixture.DocumentOf(t, body)
	for _, key := range []string{"conditionSnapshot", "recentEvents", "renderDiff", "capabilities", "versionMatrix", "evidence"} {
		if _, found := exported[key]; !found {
			t.Fatalf("exported bundle carries no %q", key)
		}
	}
	if after := world.Snapshot(t); after != before {
		t.Fatal("the export changed stored objects")
	}

	// Disabling the component withdraws the page and its export entry, and the
	// other product's page stays visible (KOSD-C-28).
	shell.Projection.Disable()
	if listed := pageIDs(service.List(session)); slices.Contains(listed, product.PageDiagnostics) {
		t.Fatalf("pages = %v, want the diagnostics page withdrawn", listed)
	}
	if listed := pageIDs(service.List(session)); !slices.Contains(listed, fixture.OtherPageID) {
		t.Fatalf("pages = %v, want the other product's page to stay", listed)
	}
	if _, failure := service.Read(ctx, session, product.PageDiagnostics); failure == nil {
		t.Fatal("the withdrawn page is still readable")
	}
	if other, failure := service.Read(ctx, session, fixture.OtherPageID); failure != nil || other.ID != fixture.OtherPageID {
		t.Fatalf("read the other product's page: %v", failure)
	}
}

// TestTC1ActiveFailureConditionIsDegraded verifies that an object whose health is
// an observed value but which carries an active failure condition still appears
// in the degradation item, naming that condition and its remedy, and that the
// stated rule covers both halves (DIAG-AC-1, DIAG-C-1, KOSD-C-53, KOSD-C-07).
func TestTC1ActiveFailureConditionIsDegraded(t *testing.T) {
	world := fixture.Start(t)
	page, failure := world.NewShell(t).Service().Read(context.Background(), fixture.Session(), product.PageDiagnostics)
	if failure != nil {
		t.Fatalf("read the diagnostics page: %v", failure)
	}
	degradation := valueOf(t, itemsOf(t, fixture.Document(t, page)), product.ItemDegradation)

	// The stated aggregation rule names both halves: health and failure
	// conditions (KOSD-C-53).
	rule := text(degradation, "rule")
	if !strings.Contains(rule, "健康") || !strings.Contains(rule, "失败条件") {
		t.Fatalf("degradation rule = %q, want health and failure conditions", rule)
	}

	// The volume keeps the health the reconciler observed and reports the block
	// only on its failure condition: it must still be listed (KOSD-C-07).
	reason := reasonOf(t, degradation, "Volume", fixture.Namespace, fixture.LockedVolume)
	if text(reason, "health") != "Healthy" {
		t.Fatalf("locked volume reason = %v, want the observed Healthy health", reason)
	}
	if text(reason, "condition") != "DeletionBlocked" {
		t.Fatalf("locked volume condition = %q, want the active failure condition", text(reason, "condition"))
	}
	if text(reason, "reason") == "" {
		t.Fatalf("locked volume reason carries no message: %v", reason)
	}
	if text(reason, "basis") != "Fact" {
		t.Fatalf("locked volume basis = %q, want the observed fact", text(reason, "basis"))
	}
	// The remedy follows the failure condition, not the generic fallback.
	if remedy := text(reason, "nextAction"); remedy != "先移除仍引用该对象的扩展对象" {
		t.Fatalf("locked volume nextAction = %q, want the remedy for a blocked deletion", remedy)
	}
}

// TestTC1PositiveConditionsAreNotFailures verifies that a True condition that
// reports a positive fact does not make a healthy object a degradation reason
// and does not pull an application's chain into the evidence item, and that a
// failure condition is still reported when a positive condition precedes it in
// the list (DIAG-AC-1, DIAG-C-1, KOSD-C-53, KOSD-C-07).
func TestTC1PositiveConditionsAreNotFailures(t *testing.T) {
	world := fixture.Start(t)
	page, failure := world.NewShell(t).Service().Read(context.Background(), fixture.Session(), product.PageDiagnostics)
	if failure != nil {
		t.Fatalf("read the diagnostics page: %v", failure)
	}
	items := itemsOf(t, fixture.Document(t, page))
	degradation := valueOf(t, items, product.ItemDegradation)

	// Each object is healthy and carries a positive condition whose True value
	// reports an observed fact, never a failure (KOSD-C-07).
	for _, object := range []struct{ kind, name string }{
		{"Volume", fixture.ImportedVolume},
		{"Endpoint", fixture.CertifiedEndpoint},
		{"Application", fixture.PausedApplication},
	} {
		if reason, found := findReason(degradation, object.kind, fixture.Namespace, object.name); found {
			t.Errorf("%s %s entered the degradation item through a positive condition: %v", object.kind, object.name, reason)
		}
	}

	// The paused application stays out of the evidence item; the degraded
	// application's chain is still carried, so the absence is discriminating
	// (KOSD-C-26).
	evidence := valueOf(t, items, product.ItemEvidence)
	if entry, found := findEvidence(evidence, fixture.Namespace, fixture.PausedApplication); found {
		t.Errorf("the evidence item carries the chain of the paused application: %v", entry)
	}
	if _, found := findEvidence(evidence, fixture.Namespace, fixture.Application); !found {
		t.Fatalf("the evidence item carries no chain for the degraded application: %v", evidence["applications"])
	}

	// The failure condition is reported even though a positive True condition
	// precedes it in the list (KOSD-C-07).
	reason, found := findReason(degradation, "Volume", fixture.Namespace, fixture.PositiveFirstVolume)
	if !found {
		t.Fatalf("the volume carrying an active failure condition is not listed: %v", degradation["reasons"])
	}
	if condition := text(reason, "condition"); condition != "DeletionBlocked" {
		t.Fatalf("reported condition = %q, want the failure condition DeletionBlocked", condition)
	}
	if text(reason, "health") != "Healthy" || text(reason, "basis") != "Fact" {
		t.Fatalf("reason = %v, want the observed Healthy health as a fact", reason)
	}
	if remedy := text(reason, "nextAction"); remedy != "先移除仍引用该对象的扩展对象" {
		t.Fatalf("nextAction = %q, want the remedy for a blocked deletion", remedy)
	}
}

// pageIDs collects the page identities of one list read.
func pageIDs(list []pages.Summary) []string {
	ids := make([]string, 0, len(list))
	for _, summary := range list {
		ids = append(ids, summary.ID)
	}
	return ids
}

// itemsOf indexes a rendered page's items by identity.
func itemsOf(t *testing.T, document map[string]any) map[string]map[string]any {
	t.Helper()
	raw, ok := document["items"].([]any)
	if !ok {
		t.Fatalf("page has no items: %v", document)
	}
	items := make(map[string]map[string]any, len(raw))
	for _, entry := range raw {
		item, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("item is not an object: %v", entry)
		}
		items[text(item, "id")] = item
	}
	return items
}

// valueOf reads one item's current value.
func valueOf(t *testing.T, items map[string]map[string]any, id string) map[string]any {
	t.Helper()
	item, known := items[id]
	if !known {
		t.Fatalf("page has no item %q", id)
	}
	value, ok := item["value"].(map[string]any)
	if !ok {
		t.Fatalf("item %q value = %v, want an object", id, item["value"])
	}
	return value
}

// entryOf reads one item's modification entry.
func entryOf(t *testing.T, items map[string]map[string]any, id string) map[string]any {
	t.Helper()
	item, known := items[id]
	if !known {
		t.Fatalf("page has no item %q", id)
	}
	entry, ok := item["entry"].(map[string]any)
	if !ok {
		t.Fatalf("item %q entry = %v, want an object", id, item["entry"])
	}
	return entry
}

// reasonOf finds one degradation reason by object.
func reasonOf(t *testing.T, degradation map[string]any, kind, namespace, name string) map[string]any {
	t.Helper()
	reasons, _ := degradation["reasons"].([]any)
	for _, entry := range reasons {
		reason, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if text(reason, "kind") == kind && text(reason, "namespace") == namespace && text(reason, "name") == name {
			return reason
		}
	}
	t.Fatalf("degradation carries no %s reason for %s/%s: %v", kind, namespace, name, reasons)
	return nil
}

// findReason returns one degradation reason when the item lists it.
func findReason(degradation map[string]any, kind, namespace, name string) (map[string]any, bool) {
	reasons, _ := degradation["reasons"].([]any)
	for _, entry := range reasons {
		reason, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if text(reason, "kind") == kind && text(reason, "namespace") == namespace && text(reason, "name") == name {
			return reason, true
		}
	}
	return nil, false
}

// findEvidence returns one application's evidence entry when the item lists it.
func findEvidence(evidence map[string]any, namespace, name string) (map[string]any, bool) {
	applications, _ := evidence["applications"].([]any)
	for _, entry := range applications {
		application, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if text(application, "namespace") == namespace && text(application, "name") == name {
			return application, true
		}
	}
	return nil, false
}

// blockedObjectOf finds one blocked object by kind and identity.
func blockedObjectOf(t *testing.T, blocked map[string]any, kind, namespace, name string) map[string]any {
	t.Helper()
	objects, _ := blocked["objects"].([]any)
	for _, entry := range objects {
		object, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if text(object, "kind") == kind && text(object, "namespace") == namespace && text(object, "name") == name {
			return object
		}
	}
	t.Fatalf("blocked item carries no %s %s/%s: %v", kind, namespace, name, objects)
	return nil
}

// objectsOf reads the object references of one blocked entry.
func objectsOf(t *testing.T, blocked map[string]any) []string {
	t.Helper()
	raw, _ := blocked["objects"].([]any)
	objects := make([]string, 0, len(raw))
	for _, entry := range raw {
		if reference, ok := entry.(string); ok {
			objects = append(objects, reference)
		}
	}
	return objects
}

// chainOf finds one application's evidence chain.
func chainOf(t *testing.T, evidence map[string]any, name string) map[string]any {
	t.Helper()
	applications, _ := evidence["applications"].([]any)
	for _, entry := range applications {
		application, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if text(application, "name") != name {
			continue
		}
		chain, ok := application["chain"].(map[string]any)
		if !ok {
			t.Fatalf("application %s carries no chain: %v", name, application["error"])
		}
		return chain
	}
	t.Fatalf("evidence carries no chain for %s: %v", name, applications)
	return nil
}

// hasTarget reports whether one application is an export target.
func hasTarget(targets map[string]any, namespace, name string) bool {
	applications, _ := targets["applications"].([]any)
	for _, entry := range applications {
		target, ok := entry.(map[string]any)
		if ok && text(target, "namespace") == namespace && text(target, "name") == name {
			return true
		}
	}
	return false
}

// text reads one string field of a decoded document.
func text(document map[string]any, key string) string {
	value, _ := document[key].(string)
	return value
}
