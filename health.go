package diagnostics

import (
	"context"
	"slices"
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/tsic404/kosd/api/v1beta1"
	"github.com/tsic404/kosd/pkg/app"
	"github.com/tsic404/kosd/pkg/backup"
	"github.com/tsic404/kosd/pkg/controlplane"
	diagnostic "github.com/tsic404/kosd/pkg/diagnostics"
	"github.com/tsic404/kosd/pkg/endpoint"
	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/pkg/identity"
	"github.com/tsic404/kosd/pkg/migration"
	"github.com/tsic404/kosd/pkg/pages/cluster"
	"github.com/tsic404/kosd/pkg/provider"
	volumeapi "github.com/tsic404/kosd/pkg/volume"
)

// Degradation is the degradation-reasons item: the rule its value follows and
// the objects it judged (KOSD-C-53).
type Degradation struct {
	// Rule is the aggregation rule the value follows (KOSD-C-53).
	Rule string `json:"rule"`
	// Reasons is one entry per abnormal object — health not Healthy or an
	// active failure condition — ordered by kind, namespace and name.
	Reasons []ReasonView `json:"reasons"`
}

// ReasonView is one degradation reason with the action that resolves it.
type ReasonView struct {
	// Kind is the object kind.
	Kind string `json:"kind"`
	// Namespace is empty for cluster-scoped objects.
	Namespace string `json:"namespace,omitempty"`
	// Name is the object name.
	Name string `json:"name"`
	// Health is the object's health state.
	Health string `json:"health,omitempty"`
	// Condition is the segment or condition the reason sits in.
	Condition string `json:"condition,omitempty"`
	// Reason explains the degradation.
	Reason string `json:"reason,omitempty"`
	// NextAction is what resolves the reason.
	NextAction string `json:"nextAction"`
	// Basis tells whether the reason is observed or inferred (KOSD-C-26).
	Basis string `json:"basis"`
}

// degradationRule is the aggregation KOSD-C-53 fixes: an object is abnormal
// when its health is not Healthy or it carries an active failure condition, and
// an application's reason comes from its evidence chain.
const degradationRule = "健康状态不是 Healthy 或存在失败条件的对象按条件聚合，应用另取证据链结论（KOSD-C-26、KOSD-C-53）"

// degradation aggregates the abnormal objects: applications through their
// evidence chain, every other kind through the failure condition it carries
// (DIAG-C-1, KOSD-C-53).
func (p *Product) degradation(ctx context.Context, session identity.Session) (any, *controlplane.Error) {
	applications, failure := p.applications(ctx, session)
	if failure != nil {
		return nil, failure
	}
	reasons := make([]ReasonView, 0)
	chains := p.chain()
	for _, application := range applications {
		if !abnormal("Application", application.Status.ObjectStatus) {
			continue
		}
		reason := ReasonView{
			Kind: "Application", Namespace: application.Namespace, Name: application.Name,
			Health:     string(application.Status.Health.State),
			Basis:      string(diagnostic.BasisInference),
			NextAction: "查看该应用的证据链定位失败来源",
		}
		chain, err := chains.Chain(ctx, application.Namespace, application.Name)
		switch {
		case err == nil:
			reason.Condition = string(chain.Conclusion.Segment)
			reason.Reason = chain.Conclusion.Summary
			reason.Basis = string(chain.Conclusion.Basis)
			if chain.Conclusion.NextAction != "" {
				reason.NextAction = chain.Conclusion.NextAction
			}
		case apierrors.IsNotFound(err):
			reason.Reason = "应用在读取证据链时已不存在"
		default:
			reason.Reason = "证据链读取失败"
		}
		reasons = append(reasons, reason)
	}
	for _, class := range cluster.StatusKinds() {
		if class == "Application" {
			// Applications already contributed their evidence-chain reason.
			continue
		}
		objects, failure := p.reader().List(ctx, class, "")
		if failure != nil {
			return nil, failure
		}
		for _, object := range objects {
			// Visibility is judged on the stored object, so an object the
			// session may not see never fails its read by being malformed
			// (KOSD-C-59).
			visible, failure := p.reader().Visible(ctx, session, class, object)
			if failure != nil {
				return nil, failure
			}
			if !visible {
				continue
			}
			var shape struct {
				Status v1beta1.ObjectStatus `json:"status"`
			}
			if failure := cluster.Decode(object, &shape); failure != nil {
				return nil, failure
			}
			if !abnormal(class, shape.Status) {
				continue
			}
			reason := ReasonView{
				Kind: class, Namespace: cluster.Text(object, "metadata", "namespace"),
				Name:   cluster.Text(object, "metadata", "name"),
				Health: string(shape.Status.Health.State),
				Basis:  string(diagnostic.BasisFact),
			}
			if condition := failingCondition(class, shape.Status.Conditions); condition != nil {
				reason.Condition = condition.Type
				reason.Reason = condition.Message
				reason.NextAction = nextActionOf(condition.Type)
			} else {
				reason.Reason = "健康状态不是 Healthy，未带活动失败条件"
				reason.NextAction = genericRemedy
			}
			reasons = append(reasons, reason)
		}
	}
	sort.Slice(reasons, func(left, right int) bool {
		if reasons[left].Kind != reasons[right].Kind {
			return reasons[left].Kind < reasons[right].Kind
		}
		if reasons[left].Namespace != reasons[right].Namespace {
			return reasons[left].Namespace < reasons[right].Namespace
		}
		return reasons[left].Name < reasons[right].Name
	})
	return Degradation{Rule: degradationRule, Reasons: reasons}, nil
}

// failureConditionTypes is each kernel kind's own failure-condition set: the
// types its controller reports a failure on (KOSD-C-07). Positive conditions
// also read True — CertificateReady, Imported, Quiesced — so the set, not the
// status alone or array order, decides which condition is a failure.
var failureConditionTypes = map[string][]string{
	"Application": {
		app.ConditionCapabilityMissing,
		app.ConditionCapabilityUnsatisfied,
		app.ConditionDefinitionMissing,
		app.ConditionInvalidConfig,
		app.ConditionInvalidPackage,
		app.ConditionNamespaceMissing,
		app.ConditionRetrying,
		app.ConditionFailed,
		app.ConditionUninstallBlocked,
		app.ConditionIrreversibleUpgrade,
		app.ConditionMigrationRequired,
		app.ConditionDrifted,
		app.ConditionUpgradeFailed,
		app.ConditionStorageFull,
	},
	"Volume": {
		volumeapi.ConditionInvalidSpec,
		volumeapi.ConditionCapabilityMissing,
		volumeapi.ConditionStorageMissing,
		volumeapi.ConditionClaimBlocked,
		volumeapi.ConditionLost,
		volumeapi.ConditionDeletionBlocked,
	},
	// ApplicationPaused is the pause notice of a Healthy endpoint, not a
	// failure, so it stays out of the endpoint's failure set (KOSD-C-07).
	"Endpoint": {
		endpoint.ConditionInvalidSpec,
		endpoint.ConditionConflict,
		endpoint.ConditionRouteBlocked,
	},
	"Provider": {
		provider.ConditionUpgradeBlocked,
		provider.ConditionDeprovisionBlocked,
	},
	"SystemComponent": {
		extension.ConditionDisableBlocked,
		extension.ConditionDeprovisionBlocked,
	},
	// ScheduleJobFailed is how a policy reports a failed scheduled trigger
	// while its health stays Healthy, so it is a failure condition here.
	"BackupPolicy": {
		backup.ConditionBackupFailed,
		backup.ConditionDrillMismatch,
		backup.ConditionScheduleJobFailed,
	},
	"RestoreJob": {
		backup.ConditionPreflightFailed,
		backup.ConditionVerificationFailed,
	},
	"VolumeMigration": {
		migration.ConditionInvalidSpec,
		migration.ConditionFailed,
	},
}

// abnormal reports an object the degradation item shows: its health is not
// Healthy, or it carries an active failure condition (KOSD-C-53).
func abnormal(kind string, status v1beta1.ObjectStatus) bool {
	return unhealthy(status) || failingCondition(kind, status.Conditions) != nil
}

// unhealthy reports an object whose health is not Healthy (KOSD-C-08).
func unhealthy(status v1beta1.ObjectStatus) bool {
	return status.Health.State != "" && status.Health.State != v1beta1.HealthStateHealthy
}

// failingCondition returns the first active failure condition of one kind. A
// failure condition keeps its own type and reports the failure with True;
// conditions outside the kind's failure set never stand in for one, and the
// first failure in the list wins over an earlier positive condition (KOSD-C-07).
func failingCondition(kind string, conditions []metav1.Condition) *metav1.Condition {
	for index := range conditions {
		condition := &conditions[index]
		if condition.Status != metav1.ConditionTrue {
			continue
		}
		if !slices.Contains(failureConditionTypes[kind], condition.Type) {
			continue
		}
		return condition
	}
	return nil
}

// genericRemedy is the suggestion for a condition this product has no specific
// remedy for: it names the object's own state, never a chain this page does not
// carry for the object's kind.
const genericRemedy = "查看该对象的条件与状态，按失败原因处理"

// nextActionOf suggests what resolves one condition. The suggestion is the
// condition type's known remedy; an unknown or missing condition gets the
// object's own state as the starting point, because this page's evidence item
// carries application chains only and cannot be offered as a reading for other
// kinds (DIAG-C-1).
func nextActionOf(conditionType string) string {
	switch conditionType {
	case app.ConditionCapabilityMissing, app.ConditionCapabilityUnsatisfied:
		return "安装提供所需能力的组件或 Provider，然后重试"
	case app.ConditionDefinitionMissing:
		return "安装或同步应用定义后重试"
	case app.ConditionNamespaceMissing:
		return "创建应用声明的命名空间后重试"
	case volumeapi.ConditionClaimBlocked:
		return "检查声明与存储类的绑定条件"
	case volumeapi.ConditionLost:
		return "检查绑定节点与后端数据是否恢复"
	case volumeapi.ConditionDeletionBlocked:
		return "先移除仍引用该对象的扩展对象"
	default:
		return genericRemedy
	}
}
