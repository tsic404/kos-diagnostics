// Package fixture builds the world the diagnostics acceptance cases read: a
// degraded application whose reference path ends at a NotReady node, an
// extension object a diagnostic adapter observes, an uninstall blocked behind
// two objects, and the events, definition and credential the bundle classes
// need.
//
// The world is loaded into a fake dynamic client, so every case reads through
// the same interfaces the control plane uses and the evidence chain and the
// bundle are assembled by kosd's own code (KOSD-C-26).
package fixture

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/tsic404/kosd/api"
	"github.com/tsic404/kosd/api/v1beta1"
	"github.com/tsic404/kosd/pkg/controlplane"
	"github.com/tsic404/kosd/pkg/diagnostics"
	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/pkg/identity"
	"github.com/tsic404/kosd/pkg/pages"
	"github.com/tsic404/kosd/pkg/write"

	product "github.com/tsic404/kos-diagnostics"
)

// The world's names and identities. The namespace is not the acceptance
// namespace on purpose: a cluster-scoped object's events live outside it, so
// only a read by object finds them (KOSD-C-64).
const (
	// Namespace holds the application and the objects it renders.
	Namespace = "shop-apps"
	// Application is the degraded application the chain starts from.
	Application = "shop"
	// EndpointName is the endpoint that exposes the application.
	EndpointName = "shop-entry"
	// BackendService is the service the route points at.
	BackendService = "shop-backend"
	// DefinitionName is the application definition the bundle re-renders.
	DefinitionName = "shop-definition"
	// BlockedApplication is the application whose uninstall is blocked.
	BlockedApplication = "legacy"
	// BlockedVolume is the volume whose claim cannot bind; the blocked item
	// names it beside the stalled uninstall (KOSD-C-38).
	BlockedVolume = "data"
	// LockedVolume is a bound, healthy volume whose deletion an extension
	// object holds in place: its health stays as observed and only the failure
	// condition reports the block (KOSD-C-07, KOSD-C-19).
	LockedVolume = "locked"
	// ImportedVolume is a healthy volume re-adopted from an older release:
	// Imported=True reports the adoption, not a failure (KOSD-C-07).
	ImportedVolume = "imported"
	// PausedApplication is a healthy application stopped for a migration or
	// node maintenance: Quiesced=True reports the stop, not a failure
	// (KOSD-C-07).
	PausedApplication = "paused"
	// CertifiedEndpoint is a healthy TLS endpoint: CertificateReady=True
	// reports the served certificate, not a failure (KOSD-C-07).
	CertifiedEndpoint = "certified-entry"
	// PositiveFirstVolume is a healthy volume whose positive condition comes
	// before its failure condition, so a reader that takes the first True
	// condition reports the wrong one (KOSD-C-07).
	PositiveFirstVolume = "positive-first"
	// NodeName is the NotReady node the workload's pod runs on.
	NodeName = "node-a"
	// WidgetGroup is the extension group the diagnostic adapter covers.
	WidgetGroup = "widgets.example.test"
	// WidgetName is the extension object on the chain.
	WidgetName = "shop-widget"
	// WidgetIdentifier is the extension identity the adapter belongs to.
	WidgetIdentifier = "demo.widgets"
	// ComponentName is the system component the adapter reports on.
	ComponentName = "widgets"
	// AdapterID is the declared diagnostic adapter identity.
	AdapterID = "widget-diagnostics"
	// AdapterSource is the data source the adapter's evidence names.
	AdapterSource = "widgets.example.test diagnostics"
	// ConfigValue is the sensitive config value the application submits.
	ConfigValue = "config-secret-7f3a"
	// Credential is the value stored in a Secret; no bundle may carry it
	// (KOSD-C-14).
	Credential = "bundle-credential-2b8e"
	// EventNewest is the newest event inside the bundle's window.
	EventNewest = "shop-backend-unavailable"
	// EventNode is a cluster-scoped object's event, stored outside the
	// application's namespace.
	EventNode = "shop-node-not-ready"
	// EventWithinWindow is an event inside the bundle's window.
	EventWithinWindow = "shop-endpoint-warning"
	// EventOutsideWindow is an event older than the window; the bundle drops
	// it (KOSD-C-64).
	EventOutsideWindow = "shop-old-warning"
)

// Now is the clock every case reads at. It fixes the event window so the
// bundle's event class is deterministic (KOSD-C-64).
var Now = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

// World is one cluster and the extension state a case reads.
type World struct {
	// Dynamic is the fake API server the cases read through.
	Dynamic dynamic.Interface
	// Environment is the extension state the chain and the bundle report.
	Environment diagnostics.Environment
	// Now is the read clock.
	Now time.Time
}

// Start builds the world. The returned value is a complete, readable cluster;
// a case that needs a disabled adapter calls DisableAdapter on it.
func Start(t *testing.T) *World {
	t.Helper()
	world := &World{Now: Now, Environment: environment()}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(), listKinds(), runtimeObjects(world.objects())...)
	client.PrependReactor("list", "events", filterEvents(client))
	world.Dynamic = client
	return world
}

// filterEvents applies the field selector KOSD-C-64 reads events by. The fake
// client ignores selectors, so without this every object query would answer
// with the whole event class.
func filterEvents(client *dynamicfake.FakeDynamicClient) k8stesting.ReactionFunc {
	return func(action k8stesting.Action) (bool, runtime.Object, error) {
		list, ok := action.(k8stesting.ListAction)
		if !ok {
			return false, nil, nil
		}
		selector := list.GetListRestrictions().Fields
		if selector.Empty() {
			return false, nil, nil
		}
		kind, kindFound := selector.RequiresExactMatch("involvedObject.kind")
		name, nameFound := selector.RequiresExactMatch("involvedObject.name")
		if !kindFound || !nameFound {
			return false, nil, nil
		}
		stored, err := client.Tracker().List(list.GetResource(),
			schema.GroupVersionKind{Version: "v1", Kind: "Event"}, list.GetNamespace())
		if err != nil {
			return true, nil, err
		}
		events, ok := stored.(*unstructured.UnstructuredList)
		if !ok {
			return false, nil, nil
		}
		filtered := &unstructured.UnstructuredList{}
		filtered.SetGroupVersionKind(events.GroupVersionKind())
		for _, item := range events.Items {
			involvedKind, _, _ := unstructured.NestedString(item.Object, "involvedObject", "kind")
			involvedName, _, _ := unstructured.NestedString(item.Object, "involvedObject", "name")
			if involvedKind != kind || involvedName != name {
				continue
			}
			filtered.Items = append(filtered.Items, item)
		}
		return true, filtered, nil
	}
}

// OtherPageID is the unrelated product's page the shell renders beside the
// diagnostics page, so a case can show that disabling diagnostics leaves it
// visible (KOSD-C-28).
const OtherPageID = "native"

// Shell is the page shell over the world's live contribution projection.
type Shell struct {
	// Projection is the page contribution set the install result publishes;
	// disabling the diagnostics component withdraws its pages from it
	// (KOSD-C-28).
	Projection *Projection

	world *World
}

// NewShell builds the page shell over the world.
func (w *World) NewShell(t *testing.T) *Shell {
	t.Helper()
	return &Shell{Projection: w.Projection(t), world: w}
}

// Service returns the page shell the host would serve.
func (s *Shell) Service() *pages.Service {
	return pages.New(s.Projection, pages.Config{
		Readings: pages.Router{product.Identifier: s.world.Product()},
		Now:      func() time.Time { return s.world.Now },
	})
}

// Projection is the page contribution set one install pass publishes. The
// diagnostics pages are those of the product declaration, loaded through the
// framework's own validation (KOSD-C-18); withdrawing the component drops them
// while the other product's page stays (KOSD-C-28).
type Projection struct {
	// Diagnostics is the diagnostics product's page set while its component is
	// active.
	Diagnostics []extension.Page
	// Native is another product's page; it is never affected by the
	// diagnostics component.
	Native extension.Page
}

// Pages implements pages.Declarations.
func (p *Projection) Pages() []extension.Page {
	return append(append([]extension.Page(nil), p.Diagnostics...), p.Native)
}

// PageFor implements pages.Declarations.
func (p *Projection) PageFor(id string, role write.Role) (extension.Page, bool) {
	for _, page := range p.Pages() {
		if page.ID == id && page.Allows(role) {
			return page, true
		}
	}
	return extension.Page{}, false
}

// Disable withdraws the diagnostics component's contributions: the install
// result no longer carries its declaration, so its page and export entry go
// with it (KOSD-C-28).
func (p *Projection) Disable() { p.Diagnostics = nil }

// Projection returns the contribution set the diagnostics declaration publishes
// today. Loading it through Declaration().Load() applies the framework's page
// validation, so an invalid declaration fails the case instead of silently
// contributing no page (KOSD-C-18).
func (w *World) Projection(t *testing.T) *Projection {
	t.Helper()
	loaded, err := product.Declaration().Load()
	if err != nil {
		t.Fatalf("load the diagnostics declaration: %v", err)
	}
	if len(loaded.Problems) > 0 {
		t.Fatalf("the diagnostics declaration names problems: %+v", loaded.Problems)
	}
	if len(loaded.Contributions.Pages) == 0 {
		t.Fatal("the diagnostics declaration contributes no page")
	}
	return &Projection{Diagnostics: loaded.Contributions.Pages, Native: otherPage()}
}

// otherPage is a read-only native view of another product; it must stay
// visible when the diagnostics component is disabled.
func otherPage() extension.Page {
	return extension.Page{
		ID: OtherPageID, Title: "原生视图", Location: "native",
		ReadOnly:       true,
		ReadOnlyReason: "原生视图只能查看，写操作经 kubectl 与 kubeconfig 发起",
		Items: []extension.PageItem{{
			ID: "fields", Label: "原始字段", Source: extension.PageSourceLocal, Store: "native-fields",
		}},
	}
}

// Session is the Owner session every case reads as. The global Owner sees every
// application (KOSD-C-59).
func Session() identity.Session {
	return identity.Session{Username: "owner", Role: write.RoleOwner}
}

// Product returns the diagnostics component's item reader over this world.
func (w *World) Product() *product.Product {
	return &product.Product{
		Dynamic:     w.Dynamic,
		Environment: w.Environment,
		Now:         func() time.Time { return w.Now },
	}
}

// Service returns the kosd diagnostic service over this world. The cases read
// the chain and the bundle through it the way the control plane does
// (KOSD-C-12, KOSD-C-26).
func (w *World) Service() *diagnostics.Service {
	return &diagnostics.Service{
		Source:      diagnostics.ClusterSource{Dynamic: w.Dynamic, Now: func() time.Time { return w.Now }},
		Environment: w.Environment,
		Now:         func() time.Time { return w.Now },
	}
}

// Export sends one request to the control-plane route the page's export entry
// names, over the same cluster the page reads (KOSD-C-12). It returns the
// status, the response headers and the body.
func (w *World) Export(t *testing.T, method, path string, body any) (int, http.Header, []byte) {
	t.Helper()
	server, err := controlplane.New(controlplane.Config{Dynamic: w.Dynamic, Diagnostics: w.Service()})
	if err != nil {
		t.Fatalf("build the control plane: %v", err)
	}
	t.Cleanup(server.Close)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode the export request: %v", err)
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, httpServer.URL+path, payload)
	if err != nil {
		t.Fatalf("build the export request: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read the export response: %v", err)
	}
	return response.StatusCode, response.Header, answer
}

// Document renders one page the way the shell answers it over the API: the
// projection encoded as JSON and decoded into the document a browser receives
// (KOSD-C-12, KOSD-C-53).
func Document(t *testing.T, page pages.Page) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("encode the page: %v", err)
	}
	document := map[string]any{}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decode the page: %v", err)
	}
	return document
}

// DocumentOf decodes one JSON document the API answered.
func DocumentOf(t *testing.T, encoded []byte) map[string]any {
	t.Helper()
	document := map[string]any{}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decode the answer %q: %v", encoded, err)
	}
	return document
}

// DisableAdapter withdraws the extension that provides the diagnostic adapter:
// the install projection no longer carries its adapter, its component or its
// capability (KOSD-C-28).
func (w *World) DisableAdapter() {
	w.Environment.Adapters = nil
	components := make([]diagnostics.Component, 0, len(w.Environment.Components))
	for _, component := range w.Environment.Components {
		if component.Identifier == WidgetIdentifier {
			continue
		}
		components = append(components, component)
	}
	w.Environment.Components = components
	capabilities := make([]diagnostics.Capability, 0, len(w.Environment.Capabilities))
	for _, capability := range w.Environment.Capabilities {
		if capability.Source == WidgetIdentifier {
			continue
		}
		capabilities = append(capabilities, capability)
	}
	w.Environment.Capabilities = capabilities
}

// Snapshot renders every stored object as one sorted document, so a case can
// prove a read changed no object field by field (DIAG-TC-1).
func (w *World) Snapshot(t *testing.T) string {
	t.Helper()
	var document strings.Builder
	for _, resource := range sortedClasses() {
		list, err := w.Dynamic.Resource(resource).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			t.Fatalf("snapshot %v: %v", resource, err)
		}
		items := list.Items
		sort.Slice(items, func(left, right int) bool {
			if items[left].GetNamespace() != items[right].GetNamespace() {
				return items[left].GetNamespace() < items[right].GetNamespace()
			}
			return items[left].GetName() < items[right].GetName()
		})
		for index := range items {
			encoded, err := json.Marshal(items[index].Object)
			if err != nil {
				t.Fatalf("encode %s %s: %v", resource.Resource, items[index].GetName(), err)
			}
			document.Write(encoded)
			document.WriteByte('\n')
		}
	}
	return document.String()
}

// sortedClasses lists every stored class in a stable order, so a snapshot of
// two reads of the same state is byte-identical.
func sortedClasses() []schema.GroupVersionResource {
	classes := make([]schema.GroupVersionResource, 0, len(listKinds()))
	for resource := range listKinds() {
		classes = append(classes, resource)
	}
	sort.Slice(classes, func(left, right int) bool {
		if classes[left].Group != classes[right].Group {
			return classes[left].Group < classes[right].Group
		}
		if classes[left].Version != classes[right].Version {
			return classes[left].Version < classes[right].Version
		}
		return classes[left].Resource < classes[right].Resource
	})
	return classes
}

// environment is the extension state the chain and the bundle report.
func environment() diagnostics.Environment {
	return diagnostics.Environment{
		Adapters: []diagnostics.Adapter{{
			ID: AdapterID, Group: WidgetGroup, Kind: "Widget",
			Version: "v1", Resource: "widgets",
			Segment: diagnostics.LinkWorkload, Source: AdapterSource,
			Components: []string{WidgetIdentifier},
		}},
		Capabilities: []diagnostics.Capability{
			{Name: "demo.widgets/diagnostics", Version: "v1", Source: WidgetIdentifier},
			{Name: "storage.volume", Version: "v1", Source: diagnostics.KernelSource},
		},
		Components: []diagnostics.Component{
			{
				Name: ComponentName, Identifier: WidgetIdentifier, Version: "1.0.0",
				FrameworkVersion: ">=1.0 <2.0", Phase: "Ready", Health: "Healthy",
			},
			{
				Name: "diagnostics", Identifier: "kos-diagnostics", Version: "1.0.0",
				FrameworkVersion: ">=1.0 <2.0", Phase: "Ready", Health: "Healthy",
			},
		},
	}
}

// listKinds names the list type of every class the world stores.
func listKinds() map[schema.GroupVersionResource]string {
	lists := map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "services"}:                              "ServiceList",
		{Version: "v1", Resource: "pods"}:                                  "PodList",
		{Version: "v1", Resource: "nodes"}:                                 "NodeList",
		{Version: "v1", Resource: "events"}:                                "EventList",
		{Version: "v1", Resource: "secrets"}:                               "SecretList",
		{Group: "apps", Version: "v1", Resource: "deployments"}:            "DeploymentList",
		{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}: "IngressList",
		{Group: WidgetGroup, Version: "v1", Resource: "widgets"}:           "WidgetList",
	}
	// The degradation item aggregates every kernel kind that carries a status,
	// so every one of their classes is listable here.
	for _, kind := range api.Kinds() {
		resource, known := api.ResourceName(kind)
		if !known {
			continue
		}
		lists[schema.GroupVersionResource{
			Group: api.Group, Version: v1beta1.GroupVersion.Version, Resource: resource,
		}] = kind + "List"
	}
	return lists
}

// runtimeObjects converts the fixtures into the runtime objects the fake client
// stores.
func runtimeObjects(objects []*unstructured.Unstructured) []runtime.Object {
	converted := make([]runtime.Object, 0, len(objects))
	for _, object := range objects {
		converted = append(converted, object)
	}
	return converted
}

// objects is the whole world.
func (w *World) objects() []*unstructured.Unstructured {
	return []*unstructured.Unstructured{
		{Object: applicationDefinition()},
		{Object: application()},
		{Object: endpoint()},
		{Object: ingress()},
		{Object: backendService()},
		{Object: deployment()},
		{Object: pod()},
		{Object: node()},
		{Object: widget()},
		{Object: blockedApplication()},
		{Object: volume()},
		{Object: lockedVolume()},
		{Object: importedVolume()},
		{Object: positiveFirstVolume()},
		{Object: certifiedEndpoint()},
		{Object: quiescedApplication()},
		{Object: restoreJob()},
		{Object: event(EventNewest, Namespace, "Application", Application, w.Now.Add(-5*time.Minute))},
		{Object: event(EventWithinWindow, Namespace, "Application", Application, w.Now.Add(-30*time.Minute))},
		{Object: event(EventOutsideWindow, Namespace, "Application", Application, w.Now.Add(-2*time.Hour))},
		{Object: event(EventNode, "default", "Node", NodeName, w.Now.Add(-20*time.Minute))},
		{Object: secret()},
	}
}

// definitionTemplate is the package the world installs.
const definitionTemplate = `apiVersion: v1
kind: Service
metadata:
  name: {{ .Name }}-backend
  namespace: {{ .Namespace }}
spec:
  selector:
    app: {{ .Name }}
  ports:
  - port: 80
    targetPort: 8080
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Name }}
  namespace: {{ .Namespace }}
spec:
  replicas: 2
  selector:
    matchLabels:
      app: {{ .Name }}
  template:
    metadata:
      labels:
        app: {{ .Name }}
    spec:
      containers:
      - name: app
        image: nginx:1.27
        env:
        - name: PASSWORD
          value: {{ .Config.password }}
---
apiVersion: widgets.example.test/v1
kind: Widget
metadata:
  name: {{ .Name }}-widget
  namespace: {{ .Namespace }}
---
apiVersion: v1
kind: Secret
metadata:
  name: {{ .Name }}-credential
  namespace: {{ .Namespace }}
type: Opaque
stringData:
  password: {{ .Config.password }}
`

// applicationDefinition is the package the world's application renders and the
// bundle re-renders (KOSD-C-32).
func applicationDefinition() map[string]any {
	return map[string]any{
		"apiVersion": api.Group + "/" + v1beta1.GroupVersion.Version,
		"kind":       "ApplicationDefinition",
		"metadata":   map[string]any{"name": DefinitionName},
		"spec": map[string]any{
			"package": map[string]any{"version": "1.0.0"},
			"config": []any{map[string]any{
				"name": "password", "type": "string", "sensitive": true,
			}},
			"template": definitionTemplate,
		},
	}
}

// application is the degraded application: Ready with health Degraded and the
// four objects its definition rendered.
func application() map[string]any {
	return map[string]any{
		"apiVersion": api.Group + "/" + v1beta1.GroupVersion.Version,
		"kind":       "Application",
		"metadata": map[string]any{
			"name": Application, "namespace": Namespace,
			"labels": map[string]any{"managed-by": "acceptance"},
		},
		"spec": map[string]any{
			"definitionRef": DefinitionName,
			"namespace":     Namespace,
			"config":        map[string]any{"password": ConfigValue},
		},
		"status": map[string]any{
			"phase":  "Ready",
			"health": map[string]any{"state": "Degraded"},
			"conditions": []any{map[string]any{
				"type": "Reconciled", "status": "True", "reason": "Applied",
				"message": "desired state applied", "lastTransitionTime": "2026-11-12T09:00:00Z",
			}},
			"rendered": []any{
				map[string]any{"version": "v1", "kind": "Service", "namespace": Namespace, "name": BackendService},
				map[string]any{"group": "apps", "version": "v1", "kind": "Deployment", "namespace": Namespace, "name": Application},
				map[string]any{"group": WidgetGroup, "version": "v1", "kind": "Widget", "namespace": Namespace, "name": WidgetName},
				map[string]any{"version": "v1", "kind": "Secret", "namespace": Namespace, "name": Application + "-credential"},
			},
		},
	}
}

// endpoint is the admitted http.host endpoint of the application.
func endpoint() map[string]any {
	return map[string]any{
		"apiVersion": api.Group + "/" + v1beta1.GroupVersion.Version,
		"kind":       "Endpoint",
		"metadata": map[string]any{
			"name": EndpointName, "namespace": Namespace,
			"labels": map[string]any{"managed-by": "acceptance"},
		},
		"spec": map[string]any{"kind": "http.host", "targetApp": Application, "host": "shop.example.test"},
		"status": map[string]any{
			"phase":  "Ready",
			"health": map[string]any{"state": "Healthy"},
			"conditions": []any{map[string]any{
				"type": "Reconciled", "status": "True", "reason": "Applied",
				"message": "route object applied", "lastTransitionTime": "2026-11-12T09:00:00Z",
			}},
		},
	}
}

// ingress is the route object the endpoint owns (KOSD-C-43).
func ingress() map[string]any {
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata":   map[string]any{"name": EndpointName, "namespace": Namespace},
		"spec": map[string]any{
			"rules": []any{map[string]any{
				"host": "shop.example.test",
				"http": map[string]any{"paths": []any{map[string]any{
					"path":     "/",
					"pathType": "Prefix",
					"backend": map[string]any{"service": map[string]any{
						"name": BackendService,
						"port": map[string]any{"number": int64(80)},
					}},
				}}},
			}},
		},
	}
}

// backendService is the backend the route points at; its selector reaches the
// single pod, which is not ready.
func backendService() map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata":   map[string]any{"name": BackendService, "namespace": Namespace},
		"spec": map[string]any{
			"selector": map[string]any{"app": Application},
			"ports":    []any{map[string]any{"port": int64(80), "targetPort": int64(8080)}},
		},
	}
}

// deployment is the workload the definition renders: two desired replicas, none
// ready, and an env value that drifted from the render.
func deployment() map[string]any {
	return map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": Application, "namespace": Namespace},
		"spec": map[string]any{
			"replicas": int64(2),
			"selector": map[string]any{"matchLabels": map[string]any{"app": Application}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": Application}},
				"spec": map[string]any{"containers": []any{map[string]any{
					"name": "app", "image": "nginx:1.27",
					"env": []any{map[string]any{"name": "PASSWORD", "value": "placeholder"}},
				}}},
			},
		},
		"status": map[string]any{"replicas": int64(2), "readyReplicas": int64(0), "unavailableReplicas": int64(2)},
	}
}

// pod is the workload's only instance, scheduled on the NotReady node.
func pod() map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name": "shop-9d7c", "namespace": Namespace,
			"labels": map[string]any{"app": Application},
		},
		"spec": map[string]any{
			"nodeName":   NodeName,
			"containers": []any{map[string]any{"name": "app", "image": "nginx:1.27"}},
		},
		"status": map[string]any{
			"phase": "Running",
			"conditions": []any{map[string]any{
				"type": "Ready", "status": "False", "reason": "ContainersNotReady",
				"message": "containers with unready status: [app]",
			}},
		},
	}
}

// node is the node the pod runs on, not ready.
func node() map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata":   map[string]any{"name": NodeName},
		"status": map[string]any{
			"conditions": []any{map[string]any{
				"type": "Ready", "status": "False", "reason": "NodeNotReady",
				"message": "node is not ready",
			}},
			"nodeInfo": map[string]any{"kubeletVersion": "v1.37.0", "osImage": "kind"},
		},
	}
}

// widget is the extension object on the chain.
func widget() map[string]any {
	return map[string]any{
		"apiVersion": WidgetGroup + "/v1",
		"kind":       "Widget",
		"metadata":   map[string]any{"name": WidgetName, "namespace": Namespace},
		"spec":       map[string]any{},
		"status": map[string]any{
			"phase":  "Ready",
			"health": map[string]any{"state": "Healthy"},
			"conditions": []any{map[string]any{
				"type": "Reconciled", "status": "True", "reason": "Applied", "message": "widget accepted",
			}},
		},
	}
}

// blockedApplication is the uninstall stuck behind two objects (KOSD-C-33).
func blockedApplication() map[string]any {
	return map[string]any{
		"apiVersion": api.Group + "/" + v1beta1.GroupVersion.Version,
		"kind":       "Application",
		"metadata":   map[string]any{"name": BlockedApplication, "namespace": Namespace},
		"spec":       map[string]any{"namespace": Namespace},
		"status": map[string]any{
			"phase":  "Uninstalling",
			"health": map[string]any{"state": "Unavailable"},
			"conditions": []any{map[string]any{
				"type": "Reconciled", "status": "True", "reason": "Applied",
				"message": "uninstall requested", "lastTransitionTime": "2026-11-12T09:00:00Z",
			}},
			"uninstall": map[string]any{
				"blocked": []any{
					map[string]any{"group": "apps", "version": "v1", "kind": "Deployment", "namespace": Namespace, "name": BlockedApplication},
					map[string]any{"version": "v1", "kind": "Volume", "namespace": Namespace, "name": "data"},
				},
				"blockedSince": "2026-11-12T09:00:00Z",
				"stalled":      true,
			},
		},
	}
}

// volume is a volume whose claim cannot bind: the blocked item lists it with
// the condition that holds it, beside the stalled uninstall (KOSD-C-33,
// KOSD-C-38).
func volume() map[string]any {
	return map[string]any{
		"apiVersion": api.Group + "/" + v1beta1.GroupVersion.Version,
		"kind":       "Volume",
		"metadata":   map[string]any{"name": BlockedVolume, "namespace": Namespace},
		"spec": map[string]any{
			"mobility": "node-local", "capacity": "1Gi", "storageClass": "local",
		},
		"status": map[string]any{
			"health": map[string]any{"state": "Degraded"},
			"conditions": []any{map[string]any{
				"type": "ClaimBlocked", "status": "True", "reason": "NoProvider",
				"message": "没有可用的存储 Provider", "lastTransitionTime": "2026-11-12T09:00:00Z",
			}},
		},
	}
}

// lockedVolume is the reachable counterexample of an object whose health is an
// observed value (Healthy) while an active failure condition holds it: the
// kernel keeps the axis as observed and reports the block on
// DeletionBlocked=True with Reconciled=False (KOSD-C-07, KOSD-C-19).
func lockedVolume() map[string]any {
	return map[string]any{
		"apiVersion": api.Group + "/" + v1beta1.GroupVersion.Version,
		"kind":       "Volume",
		"metadata":   map[string]any{"name": LockedVolume, "namespace": Namespace},
		"spec": map[string]any{
			"mobility": "cluster-movable", "capacity": "2Gi", "storageClass": "shared",
		},
		"status": map[string]any{
			"phase":  "Bound",
			"health": map[string]any{"state": "Healthy"},
			"conditions": []any{
				map[string]any{
					"type": "Reconciled", "status": "False", "reason": "DeletionBlocked",
					"message": "删除被扩展对象引用阻塞", "lastTransitionTime": "2026-11-12T09:00:00Z",
				},
				map[string]any{
					"type": "DeletionBlocked", "status": "True", "reason": "DeletionBlocked",
					"message":            "删除被扩展对象引用阻塞：引用者 Widget shop-apps/shop-widget",
					"lastTransitionTime": "2026-11-12T09:00:00Z",
				},
			},
		},
	}
}

// importedVolume is a healthy volume whose claim was re-adopted from an older
// release: Imported=True reports the adoption, not a failure (KOSD-C-07).
func importedVolume() map[string]any {
	return map[string]any{
		"apiVersion": api.Group + "/" + v1beta1.GroupVersion.Version,
		"kind":       "Volume",
		"metadata":   map[string]any{"name": ImportedVolume, "namespace": Namespace},
		"spec": map[string]any{
			"mobility": "cluster-movable", "capacity": "2Gi", "storageClass": "shared",
		},
		"status": map[string]any{
			"phase":  "Bound",
			"health": map[string]any{"state": "Healthy"},
			"conditions": []any{
				map[string]any{
					"type": "Reconciled", "status": "True", "reason": "Applied",
					"message": "desired state applied", "lastTransitionTime": "2026-11-12T09:00:00Z",
				},
				map[string]any{
					"type": "Imported", "status": "True", "reason": "Imported",
					"message": "claim carried the release marker and was imported", "lastTransitionTime": "2026-11-12T09:00:00Z",
				},
			},
		},
	}
}

// positiveFirstVolume is a healthy volume whose positive condition precedes
// the failure condition in the list, so the reported condition must come from
// the failure set rather than array order (KOSD-C-07).
func positiveFirstVolume() map[string]any {
	return map[string]any{
		"apiVersion": api.Group + "/" + v1beta1.GroupVersion.Version,
		"kind":       "Volume",
		"metadata":   map[string]any{"name": PositiveFirstVolume, "namespace": Namespace},
		"spec": map[string]any{
			"mobility": "cluster-movable", "capacity": "2Gi", "storageClass": "shared",
		},
		"status": map[string]any{
			"phase":  "Bound",
			"health": map[string]any{"state": "Healthy"},
			"conditions": []any{
				map[string]any{
					"type": "Reconciled", "status": "True", "reason": "Applied",
					"message": "desired state applied", "lastTransitionTime": "2026-11-12T09:00:00Z",
				},
				map[string]any{
					"type": "Imported", "status": "True", "reason": "Imported",
					"message": "claim carried the release marker and was imported", "lastTransitionTime": "2026-11-12T09:00:00Z",
				},
				map[string]any{
					"type": "DeletionBlocked", "status": "True", "reason": "DeletionBlocked",
					"message": "删除被扩展对象引用阻塞", "lastTransitionTime": "2026-11-12T09:00:00Z",
				},
			},
		},
	}
}

// certifiedEndpoint is a healthy TLS endpoint: CertificateReady=True reports
// the served certificate, not a failure (KOSD-C-07, KOSD-C-43).
func certifiedEndpoint() map[string]any {
	return map[string]any{
		"apiVersion": api.Group + "/" + v1beta1.GroupVersion.Version,
		"kind":       "Endpoint",
		"metadata": map[string]any{
			"name": CertifiedEndpoint, "namespace": Namespace,
			"labels": map[string]any{"managed-by": "acceptance"},
		},
		"spec": map[string]any{"kind": "http.host", "targetApp": PausedApplication, "host": "tls.example.test"},
		"status": map[string]any{
			"phase":  "Ready",
			"health": map[string]any{"state": "Healthy"},
			"conditions": []any{
				map[string]any{
					"type": "Reconciled", "status": "True", "reason": "Applied",
					"message": "route object applied", "lastTransitionTime": "2026-11-12T09:00:00Z",
				},
				map[string]any{
					"type": "CertificateReady", "status": "True", "reason": "CertificateServed",
					"message": "certificate served", "lastTransitionTime": "2026-11-12T09:00:00Z",
				},
			},
		},
	}
}

// quiescedApplication is a healthy application stopped for a migration or node
// maintenance: Quiesced=True reports the stop, not a failure (KOSD-C-07).
func quiescedApplication() map[string]any {
	return map[string]any{
		"apiVersion": api.Group + "/" + v1beta1.GroupVersion.Version,
		"kind":       "Application",
		"metadata": map[string]any{
			"name": PausedApplication, "namespace": Namespace,
			"labels": map[string]any{"managed-by": "acceptance"},
		},
		"spec": map[string]any{"namespace": Namespace},
		"status": map[string]any{
			"phase":  "Ready",
			"health": map[string]any{"state": "Healthy"},
			"conditions": []any{
				map[string]any{
					"type": "Reconciled", "status": "True", "reason": "Applied",
					"message": "desired state applied", "lastTransitionTime": "2026-11-12T09:00:00Z",
				},
				map[string]any{
					"type": "Quiesced", "status": "True", "reason": "Migration",
					"message": "卷迁移进行中", "lastTransitionTime": "2026-11-12T09:00:00Z",
				},
			},
		},
	}
}

// restoreJob is a non-application object that carries a failed condition, so
// the degradation item's condition aggregation has an input beyond applications
// (KOSD-C-53).
func restoreJob() map[string]any {
	return map[string]any{
		"apiVersion": api.Group + "/" + v1beta1.GroupVersion.Version,
		"kind":       "RestoreJob",
		"metadata":   map[string]any{"name": "restore-1", "namespace": Namespace},
		"spec":       map[string]any{},
		"status": map[string]any{
			"health": map[string]any{"state": "Unavailable"},
			"conditions": []any{
				map[string]any{
					"type": "Reconciled", "status": "False", "reason": "VerificationFailed",
					"message": "恢复校验未通过", "lastTransitionTime": "2026-11-12T09:00:00Z",
				},
				map[string]any{
					"type": "VerificationFailed", "status": "True", "reason": "VerificationFailed",
					"message": "restore source missing", "lastTransitionTime": "2026-11-12T09:00:00Z",
				},
			},
		},
	}
}

// event builds one event: the kind, namespace and name of the object it is
// about, stamped at when.
func event(name, namespace, kind, involved string, when time.Time) map[string]any {
	stamp := when.UTC().Format(time.RFC3339)
	involvedObject := map[string]any{"kind": kind, "name": involved}
	if namespace != "default" {
		involvedObject["namespace"] = namespace
	}
	return map[string]any{
		"apiVersion":     "v1",
		"kind":           "Event",
		"metadata":       map[string]any{"name": name, "namespace": namespace},
		"involvedObject": involvedObject,
		"type":           "Warning",
		"reason":         "DiagnosticCase",
		"message":        "the case's fixture event",
		"count":          int64(1),
		"firstTimestamp": stamp,
		"lastTimestamp":  stamp,
	}
}

// secret is a credential store no bundle may read (KOSD-C-14).
func secret() map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]any{"name": Application + "-credential", "namespace": Namespace},
		"type":       "Opaque",
		"data": map[string]any{
			"password": base64.StdEncoding.EncodeToString([]byte(Credential)),
		},
	}
}
