package e2e

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/argoproj/gitops-engine/pkg/diff"
	"github.com/argoproj/gitops-engine/pkg/health"
	. "github.com/argoproj/gitops-engine/pkg/sync/common"
	"github.com/argoproj/gitops-engine/pkg/utils/kube"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"github.com/argoproj/argo-cd/v3/common"
	applicationpkg "github.com/argoproj/argo-cd/v3/pkg/apiclient/application"
	. "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v3/test/e2e/fixture"
	accountFixture "github.com/argoproj/argo-cd/v3/test/e2e/fixture/account"
	. "github.com/argoproj/argo-cd/v3/test/e2e/fixture/app"
	clusterFixture "github.com/argoproj/argo-cd/v3/test/e2e/fixture/cluster"
	projectFixture "github.com/argoproj/argo-cd/v3/test/e2e/fixture/project"
	repoFixture "github.com/argoproj/argo-cd/v3/test/e2e/fixture/repos"
	"github.com/argoproj/argo-cd/v3/test/e2e/testdata"
	"github.com/argoproj/argo-cd/v3/util/argo"
	"github.com/argoproj/argo-cd/v3/util/errors"
	utilio "github.com/argoproj/argo-cd/v3/util/io"
	"github.com/argoproj/argo-cd/v3/util/settings"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application"
)

const (
	guestbookPath          = "guestbook"
	guestbookPathLocal     = "./testdata/guestbook_local"
	globalWithNoNameSpace  = "global-with-no-namespace"
	guestbookWithNamespace = "guestbook-with-namespace"
	resourceActions        = "resource-actions"
	appLogsRetryCount      = 5
)

// This empty test is here only for clarity, to conform to logs rbac tests structure in account. This exact usecase is covered in the TestAppLogs test
func TestGetLogsAllowNoSwitch(_ *testing.T) {
}

func TestGetLogsDeny(t *testing.T) {
	fixture.SkipOnEnv(t, "OPENSHIFT")

	accountFixture.Given(t).
		Name("test").
		When().
		Create().
		Login().
		SetPermissions([]fixture.ACL{
			{
				Resource: "applications",
				Action:   "create",
				Scope:    "*",
			},
			{
				Resource: "applications",
				Action:   "get",
				Scope:    "*",
			},
			{
				Resource: "applications",
				Action:   "sync",
				Scope:    "*",
			},
			{
				Resource: "projects",
				Action:   "get",
				Scope:    "*",
			},
		}, "app-creator")

	GivenWithSameState(t).
		Path("guestbook-logs").
		When().
		CreateApp().
		Sync().
		Then().
		Expect(HealthIs(health.HealthStatusHealthy)).
		And(func(app *Application) {
			_, err := fixture.RunCliWithRetry(appLogsRetryCount, "app", "logs", app.Name, "--kind", "Deployment", "--group", "", "--name", "guestbook-ui")
			assert.ErrorContains(t, err, "permission denied")
		})
}

func TestGetLogsAllow(t *testing.T) {
	fixture.SkipOnEnv(t, "OPENSHIFT")

	accountFixture.Given(t).
		Name("test").
		When().
		Create().
		Login().
		SetPermissions([]fixture.ACL{
			{
				Resource: "applications",
				Action:   "create",
				Scope:    "*",
			},
			{
				Resource: "applications",
				Action:   "get",
				Scope:    "*",
			},
			{
				Resource: "applications",
				Action:   "sync",
				Scope:    "*",
			},
			{
				Resource: "projects",
				Action:   "get",
				Scope:    "*",
			},
			{
				Resource: "logs",
				Action:   "get",
				Scope:    "*",
			},
		}, "app-creator")

	GivenWithSameState(t).
		Path("guestbook-logs").
		When().
		CreateApp().
		Sync().
		Then().
		Expect(HealthIs(health.HealthStatusHealthy)).
		And(func(app *Application) {
			out, err := fixture.RunCliWithRetry(appLogsRetryCount, "app", "logs", app.Name, "--kind", "Deployment", "--group", "", "--name", "guestbook-ui")
			require.NoError(t, err)
			assert.Contains(t, out, "Hi")
		}).
		And(func(app *Application) {
			out, err := fixture.RunCliWithRetry(appLogsRetryCount, "app", "logs", app.Name, "--kind", "Pod")
			require.NoError(t, err)
			assert.Contains(t, out, "Hi")
		}).
		And(func(app *Application) {
			out, err := fixture.RunCliWithRetry(appLogsRetryCount, "app", "logs", app.Name, "--kind", "Service")
			require.NoError(t, err)
			assert.NotContains(t, out, "Hi")
		})
}

func TestSyncToUnsignedCommit(t *testing.T) {
	fixture.SkipOnEnv(t, "GPG")
	Given(t).
		Project("gpg").
		Path(guestbookPath).
		When().
		IgnoreErrors().
		CreateApp().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationError)).
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		Expect(HealthIs(health.HealthStatusMissing))
}

func TestSyncToSignedCommitWithoutKnownKey(t *testing.T) {
	fixture.SkipOnEnv(t, "GPG")
	Given(t).
		Project("gpg").
		Path(guestbookPath).
		When().
		AddSignedFile("test.yaml", "null").
		IgnoreErrors().
		CreateApp().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationError)).
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		Expect(HealthIs(health.HealthStatusMissing))
}

func TestSyncToSignedCommitWithKnownKey(t *testing.T) {
	fixture.SkipOnEnv(t, "GPG")
	Given(t).
		Project("gpg").
		Path(guestbookPath).
		GPGPublicKeyAdded().
		Sleep(2).
		When().
		AddSignedFile("test.yaml", "null").
		IgnoreErrors().
		CreateApp().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy))
}

func TestSyncToSignedBranchWithKnownKey(t *testing.T) {
	fixture.SkipOnEnv(t, "GPG")
	Given(t).
		Project("gpg").
		Path(guestbookPath).
		Revision("master").
		GPGPublicKeyAdded().
		Sleep(2).
		When().
		AddSignedFile("test.yaml", "null").
		IgnoreErrors().
		CreateApp().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy))
}

func TestSyncToSignedBranchWithUnknownKey(t *testing.T) {
	fixture.SkipOnEnv(t, "GPG")
	Given(t).
		Project("gpg").
		Path(guestbookPath).
		Revision("master").
		Sleep(2).
		When().
		AddSignedFile("test.yaml", "null").
		IgnoreErrors().
		CreateApp().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationError)).
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		Expect(HealthIs(health.HealthStatusMissing))
}

func TestSyncToUnsignedBranch(t *testing.T) {
	fixture.SkipOnEnv(t, "GPG")
	Given(t).
		Project("gpg").
		Revision("master").
		Path(guestbookPath).
		GPGPublicKeyAdded().
		Sleep(2).
		When().
		IgnoreErrors().
		CreateApp().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationError)).
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		Expect(HealthIs(health.HealthStatusMissing))
}

func TestSyncToSignedTagWithKnownKey(t *testing.T) {
	fixture.SkipOnEnv(t, "GPG")
	Given(t).
		Project("gpg").
		Revision("signed-tag").
		Path(guestbookPath).
		GPGPublicKeyAdded().
		Sleep(2).
		When().
		AddSignedTag("signed-tag").
		IgnoreErrors().
		CreateApp().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy))
}

func TestSyncToSignedTagWithUnknownKey(t *testing.T) {
	fixture.SkipOnEnv(t, "GPG")
	Given(t).
		Project("gpg").
		Revision("signed-tag").
		Path(guestbookPath).
		Sleep(2).
		When().
		AddSignedTag("signed-tag").
		IgnoreErrors().
		CreateApp().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationError)).
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		Expect(HealthIs(health.HealthStatusMissing))
}

func TestSyncToUnsignedTag(t *testing.T) {
	fixture.SkipOnEnv(t, "GPG")
	Given(t).
		Project("gpg").
		Revision("unsigned-tag").
		Path(guestbookPath).
		GPGPublicKeyAdded().
		Sleep(2).
		When().
		AddTag("unsigned-tag").
		IgnoreErrors().
		CreateApp().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationError)).
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		Expect(HealthIs(health.HealthStatusMissing))
}

func TestAppCreation(t *testing.T) {
	ctx := Given(t)
	ctx.
		Path(guestbookPath).
		When().
		CreateApp().
		Then().
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		And(func(app *Application) {
			assert.Equal(t, fixture.Name(), app.Name)
			assert.Equal(t, fixture.RepoURL(fixture.RepoURLTypeFile), app.Spec.GetSource().RepoURL)
			assert.Equal(t, guestbookPath, app.Spec.GetSource().Path)
			assert.Equal(t, fixture.DeploymentNamespace(), app.Spec.Destination.Namespace)
			assert.Equal(t, KubernetesInternalAPIServerAddr, app.Spec.Destination.Server)
		}).
		Expect(Event(argo.EventReasonResourceCreated, "create")).
		And(func(_ *Application) {
			// app should be listed
			output, err := fixture.RunCli("app", "list")
			require.NoError(t, err)
			assert.Contains(t, output, fixture.Name())
		}).
		When().
		// ensure that create is idempotent
		CreateApp().
		Then().
		Given().
		Revision("master").
		When().
		// ensure that update replaces spec and merge labels and annotations
		And(func() {
			errors.NewHandler(t).FailOnErr(fixture.AppClientset.ArgoprojV1alpha1().Applications(fixture.TestNamespace()).Patch(t.Context(),
				ctx.GetName(), types.MergePatchType, []byte(`{"metadata": {"labels": { "test": "label" }, "annotations": { "test": "annotation" }}}`), metav1.PatchOptions{}))
		}).
		CreateApp("--upsert").
		Then().
		And(func(app *Application) {
			assert.Equal(t, "label", app.Labels["test"])
			assert.Equal(t, "annotation", app.Annotations["test"])
			assert.Equal(t, "master", app.Spec.GetSource().TargetRevision)
		})
}

func TestAppCreationWithoutForceUpdate(t *testing.T) {
	ctx := Given(t)

	ctx.
		Path(guestbookPath).
		DestName("in-cluster").
		When().
		CreateApp().
		Then().
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		And(func(app *Application) {
			assert.Equal(t, ctx.AppName(), app.Name)
			assert.Equal(t, fixture.RepoURL(fixture.RepoURLTypeFile), app.Spec.GetSource().RepoURL)
			assert.Equal(t, guestbookPath, app.Spec.GetSource().Path)
			assert.Equal(t, fixture.DeploymentNamespace(), app.Spec.Destination.Namespace)
			assert.Equal(t, "in-cluster", app.Spec.Destination.Name)
		}).
		Expect(Event(argo.EventReasonResourceCreated, "create")).
		And(func(_ *Application) {
			// app should be listed
			output, err := fixture.RunCli("app", "list")
			require.NoError(t, err)
			assert.Contains(t, output, fixture.Name())
		}).
		When().
		IgnoreErrors().
		CreateApp("--dest-server", KubernetesInternalAPIServerAddr).
		Then().
		Expect(Error("", "existing application spec is different, use upsert flag to force update"))
}

// Test designed to cover #15126.
// The issue occurs in the controller, when a valuesObject field that contains non-strings (eg, a nested map) gets
// merged/patched.
// Note: Failure is observed by the test timing out, because the controller cannot 'merge' the patch.
func TestPatchValuesObject(t *testing.T) {
	Given(t).
		Timeout(30).
		Path("helm").
		When().
		// app should be auto-synced once created
		CreateFromFile(func(app *Application) {
			app.Spec.Source.Helm = &ApplicationSourceHelm{
				ValuesObject: &runtime.RawExtension{
					// Setup by using nested YAML objects, which is what causes the patch error:
					// "unable to find api field in struct RawExtension for the json field "some""
					Raw: []byte(`{"some": {"foo": "bar"}}`),
				},
			}
		}).
		Then().
		When().
		PatchApp(`[{
					"op": "add",
					"path": "/spec/source/helm/valuesObject",
					"value": {"some":{"foo":"bar","new":"field"}}
					}]`).
		Refresh(RefreshTypeNormal).
		Sync().
		Then().
		Expect(Success("")).
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(NoConditions()).
		And(func(app *Application) {
			// Check that the patch was a success.
			assert.JSONEq(t, `{"some":{"foo":"bar","new":"field"}}`, string(app.Spec.Source.Helm.ValuesObject.Raw))
		})
}

func TestDeleteAppResource(t *testing.T) {
	ctx := Given(t)

	ctx.
		Path(guestbookPath).
		When().
		CreateApp().
		Sync().
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(_ *Application) {
			// app should be listed
			if _, err := fixture.RunCli("app", "delete-resource", fixture.Name(), "--kind", "Service", "--resource-name", "guestbook-ui"); err != nil {
				require.NoError(t, err)
			}
		}).
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		Expect(HealthIs(health.HealthStatusMissing))
}

// Fix for issue #2677, support PATCH in HTTP service
func TestPatchHttp(t *testing.T) {
	ctx := Given(t)

	ctx.
		Path(guestbookPath).
		When().
		CreateApp().
		Sync().
		PatchAppHttp(`{"metadata": {"labels": { "test": "patch" }, "annotations": { "test": "patch" }}}`).
		Then().
		And(func(app *Application) {
			assert.Equal(t, "patch", app.Labels["test"])
			assert.Equal(t, "patch", app.Annotations["test"])
		})
}

// demonstrate that we cannot use a standard sync when an immutable field is changed, we must use "force"
func TestImmutableChange(t *testing.T) {
	fixture.SkipOnEnv(t, "OPENSHIFT")
	Given(t).
		Path("secrets").
		When().
		CreateApp().
		PatchFile("secrets.yaml", `[{"op": "add", "path": "/data/new-field", "value": "dGVzdA=="}, {"op": "add", "path": "/immutable", "value": true}]`).
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		PatchFile("secrets.yaml", `[{"op": "add", "path": "/data/new-field", "value": "dGVzdDI="}]`).
		IgnoreErrors().
		Sync().
		DoNotIgnoreErrors().
		Then().
		Expect(OperationPhaseIs(OperationFailed)).
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		Expect(ResourceResultNumbering(1)).
		Expect(ResourceResultMatches(ResourceResult{
			Kind:      "Secret",
			Version:   "v1",
			Namespace: fixture.DeploymentNamespace(),
			Name:      "test-secret",
			SyncPhase: "Sync",
			Status:    "SyncFailed",
			HookPhase: "Failed",
			Message:   `Secret "test-secret" is invalid`,
		})).
		// now we can do this will a force
		Given().
		Force().
		When().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy))
}

func TestInvalidAppProject(t *testing.T) {
	Given(t).
		Path(guestbookPath).
		Project("does-not-exist").
		When().
		IgnoreErrors().
		CreateApp().
		Then().
		// We're not allowed to infer whether the project exists based on this error message. Instead, we get a generic
		// permission denied error.
		Expect(Error("", "is not allowed"))
}

func TestAppDeletion(t *testing.T) {
	Given(t).
		Path(guestbookPath).
		When().
		CreateApp().
		Then().
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		When().
		Delete(true).
		Then().
		Expect(DoesNotExist()).
		Expect(Event(argo.EventReasonResourceDeleted, "delete"))

	output, err := fixture.RunCli("app", "list")
	require.NoError(t, err)
	assert.NotContains(t, output, fixture.Name())
}

func TestAppLabels(t *testing.T) {
	Given(t).
		Path("config-map").
		When().
		CreateApp("-l", "foo=bar").
		Then().
		And(func(_ *Application) {
			assert.Contains(t, errors.NewHandler(t).FailOnErr(fixture.RunCli("app", "list")), fixture.Name())
			assert.Contains(t, errors.NewHandler(t).FailOnErr(fixture.RunCli("app", "list", "-l", "foo=bar")), fixture.Name())
			assert.NotContains(t, errors.NewHandler(t).FailOnErr(fixture.RunCli("app", "list", "-l", "foo=rubbish")), fixture.Name())
		}).
		Given().
		// remove both name and replace labels means nothing will sync
		Name("").
		When().
		IgnoreErrors().
		Sync("-l", "foo=rubbish").
		DoNotIgnoreErrors().
		Then().
		Expect(Error("", "No matching apps found for filter: selector foo=rubbish")).
		// check we can update the app and it is then sync'd
		Given().
		When().
		Sync("-l", "foo=bar")
}

func TestTrackAppStateAndSyncApp(t *testing.T) {
	Given(t).
		Path(guestbookPath).
		When().
		CreateApp().
		Sync().
		Wait().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		Expect(Success(fmt.Sprintf("Service     %s  guestbook-ui  Synced ", fixture.DeploymentNamespace()))).
		Expect(Success(fmt.Sprintf("apps   Deployment  %s  guestbook-ui  Synced", fixture.DeploymentNamespace()))).
		Expect(Event(argo.EventReasonResourceUpdated, "sync")).
		And(func(app *Application) {
			assert.NotNil(t, app.Status.OperationState.SyncResult)
		})
}

func TestAppRollbackSuccessful(t *testing.T) {
	Given(t).
		Path(guestbookPath).
		When().
		CreateApp().
		Sync().
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			assert.NotEmpty(t, app.Status.Sync.Revision)
		}).
		And(func(app *Application) {
			appWithHistory := app.DeepCopy()
			appWithHistory.Status.History = []RevisionHistory{{
				ID:         1,
				Revision:   app.Status.Sync.Revision,
				DeployedAt: metav1.Time{Time: metav1.Now().UTC().Add(-1 * time.Minute)},
				Source:     app.Spec.GetSource(),
			}, {
				ID:         2,
				Revision:   "cdb",
				DeployedAt: metav1.Time{Time: metav1.Now().UTC().Add(-2 * time.Minute)},
				Source:     app.Spec.GetSource(),
			}}
			patch, _, err := diff.CreateTwoWayMergePatch(app, appWithHistory, &Application{})
			require.NoError(t, err)
			app, err = fixture.AppClientset.ArgoprojV1alpha1().Applications(fixture.TestNamespace()).Patch(t.Context(), app.Name, types.MergePatchType, patch, metav1.PatchOptions{})
			require.NoError(t, err)

			// sync app and make sure it reaches InSync state
			_, err = fixture.RunCli("app", "rollback", app.Name, "1")
			require.NoError(t, err)
		}).
		Expect(Event(argo.EventReasonOperationStarted, "rollback")).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			assert.Equal(t, SyncStatusCodeSynced, app.Status.Sync.Status)
			require.NotNil(t, app.Status.OperationState.SyncResult)
			assert.Len(t, app.Status.OperationState.SyncResult.Resources, 2)
			assert.Equal(t, OperationSucceeded, app.Status.OperationState.Phase)
			assert.Len(t, app.Status.History, 3)
		})
}

func TestComparisonFailsIfClusterNotAdded(t *testing.T) {
	Given(t).
		Path(guestbookPath).
		DestServer("https://not-registered-cluster/api").
		When().
		IgnoreErrors().
		CreateApp().
		Then().
		Expect(DoesNotExist())
}

func TestComparisonFailsIfDestinationClusterIsInvalid(t *testing.T) {
	clusterActions := clusterFixture.Given(t).
		Name("temp-cluster").
		Server(KubernetesInternalAPIServerAddr).
		When().
		Create()

	GivenWithSameState(t).
		Path(guestbookPath).
		DestName("temp-cluster").
		When().
		CreateApp().
		Refresh(RefreshTypeNormal).
		Sync().
		Then().
		Expect(Success("")).
		Expect(HealthIs(health.HealthStatusHealthy)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		When().
		And(func() {
			clusterActions.DeleteByName()
		}).
		Refresh(RefreshTypeNormal).
		Then().
		Expect(Success("")).
		Expect(HealthIs(health.HealthStatusUnknown)).
		Expect(SyncStatusIs(SyncStatusCodeUnknown)).
		Expect(Condition(ApplicationConditionInvalidSpecError, "there are no clusters with this name"))
}

func TestComparisonFailsIfInClusterDisabled(t *testing.T) {
	Given(t).
		Path(guestbookPath).
		DestServer(KubernetesInternalAPIServerAddr).
		When().
		CreateApp().
		Refresh(RefreshTypeNormal).
		Sync().
		Then().
		Expect(Success("")).
		Expect(HealthIs(health.HealthStatusHealthy)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		When().
		SetParamInSettingConfigMap("cluster.inClusterEnabled", "false").
		Refresh(RefreshTypeNormal).
		Then().
		Expect(Success("")).
		Expect(HealthIs(health.HealthStatusUnknown)).
		Expect(SyncStatusIs(SyncStatusCodeUnknown)).
		Expect(Condition(ApplicationConditionInvalidSpecError, fmt.Sprintf("cluster %q is disabled", KubernetesInternalAPIServerAddr)))
}

func TestCannotSetInvalidPath(t *testing.T) {
	Given(t).
		Path(guestbookPath).
		When().
		CreateApp().
		IgnoreErrors().
		AppSet("--path", "garbage").
		Then().
		Expect(Error("", "app path does not exist"))
}

func TestManipulateApplicationResources(t *testing.T) {
	Given(t).
		Path(guestbookPath).
		When().
		CreateApp().
		Sync().
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			manifests, err := fixture.RunCli("app", "manifests", app.Name, "--source", "live")
			require.NoError(t, err)
			resources, err := kube.SplitYAML([]byte(manifests))
			require.NoError(t, err)

			index := -1
			for i := range resources {
				if resources[i].GetKind() == kube.DeploymentKind {
					index = i
					break
				}
			}
			assert.Greater(t, index, -1)

			deployment := resources[index]

			closer, client, err := fixture.ArgoCDClientset.NewApplicationClient()
			require.NoError(t, err)
			defer utilio.Close(closer)

			_, err = client.DeleteResource(t.Context(), &applicationpkg.ApplicationResourceDeleteRequest{
				Name:         &app.Name,
				Group:        ptr.To(deployment.GroupVersionKind().Group),
				Kind:         ptr.To(deployment.GroupVersionKind().Kind),
				Version:      ptr.To(deployment.GroupVersionKind().Version),
				Namespace:    ptr.To(deployment.GetNamespace()),
				ResourceName: ptr.To(deployment.GetName()),
			})
			require.NoError(t, err)
		}).
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync))
}

func assetSecretDataHidden(t *testing.T, manifest string) {
	t.Helper()
	secret, err := UnmarshalToUnstructured(manifest)
	require.NoError(t, err)

	_, hasStringData, err := unstructured.NestedMap(secret.Object, "stringData")
	require.NoError(t, err)
	assert.False(t, hasStringData)

	secretData, hasData, err := unstructured.NestedMap(secret.Object, "data")
	require.NoError(t, err)
	assert.True(t, hasData)
	for _, v := range secretData {
		assert.Regexp(t, `[*]*`, v)
	}
	var lastAppliedConfigAnnotation string
	annotations := secret.GetAnnotations()
	if annotations != nil {
		lastAppliedConfigAnnotation = annotations[corev1.LastAppliedConfigAnnotation]
	}
	if lastAppliedConfigAnnotation != "" {
		assetSecretDataHidden(t, lastAppliedConfigAnnotation)
	}
}

func TestAppWithSecrets(t *testing.T) {
	closer, client, err := fixture.ArgoCDClientset.NewApplicationClient()
	require.NoError(t, err)
	defer utilio.Close(closer)

	Given(t).
		Path("secrets").
		When().
		CreateApp().
		Sync().
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			res := errors.NewHandler(t).FailOnErr(client.GetResource(t.Context(), &applicationpkg.ApplicationResourceRequest{
				Namespace:    &app.Spec.Destination.Namespace,
				Kind:         ptr.To(kube.SecretKind),
				Group:        ptr.To(""),
				Name:         &app.Name,
				Version:      ptr.To("v1"),
				ResourceName: ptr.To("test-secret"),
			})).(*applicationpkg.ApplicationResourceResponse)
			assetSecretDataHidden(t, res.GetManifest())

			manifests, err := client.GetManifests(t.Context(), &applicationpkg.ApplicationManifestQuery{Name: &app.Name})
			require.NoError(t, err)

			for _, manifest := range manifests.Manifests {
				assetSecretDataHidden(t, manifest)
			}

			diffOutput := errors.NewHandler(t).FailOnErr(fixture.RunCli("app", "diff", app.Name)).(string)
			assert.Empty(t, diffOutput)

			// make sure resource update error does not print secret details
			_, err = fixture.RunCli("app", "patch-resource", "test-app-with-secrets", "--resource-name", "test-secret",
				"--kind", "Secret", "--patch", `{"op": "add", "path": "/data", "value": "hello"}'`,
				"--patch-type", "application/json-patch+json")
			require.ErrorContains(t, err, fmt.Sprintf("failed to patch Secret %s/test-secret", fixture.DeploymentNamespace()))
			assert.NotContains(t, err.Error(), "username")
			assert.NotContains(t, err.Error(), "password")

			// patch secret and make sure app is out of sync and diff detects the change
			errors.NewHandler(t).FailOnErr(fixture.KubeClientset.CoreV1().Secrets(fixture.DeploymentNamespace()).Patch(t.Context(),
				"test-secret", types.JSONPatchType, []byte(`[
	{"op": "remove", "path": "/data/username"},
	{"op": "add", "path": "/stringData", "value": {"password": "foo"}}
]`), metav1.PatchOptions{}))
		}).
		When().
		Refresh(RefreshTypeNormal).
		Then().
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		And(func(app *Application) {
			diffOutput, err := fixture.RunCli("app", "diff", app.Name)
			require.Error(t, err)
			assert.Contains(t, diffOutput, "username: ++++++++")
			assert.Contains(t, diffOutput, "password: ++++++++++++")

			// local diff should ignore secrets
			diffOutput = errors.NewHandler(t).FailOnErr(fixture.RunCli("app", "diff", app.Name, "--local", "testdata", "--server-side-generate")).(string)
			assert.Empty(t, diffOutput)

			// ignore missing field and make sure diff shows no difference
			app.Spec.IgnoreDifferences = []ResourceIgnoreDifferences{{
				Kind: kube.SecretKind, JSONPointers: []string{"/data"},
			}}
			errors.NewHandler(t).FailOnErr(client.UpdateSpec(t.Context(), &applicationpkg.ApplicationUpdateSpecRequest{Name: &app.Name, Spec: &app.Spec}))
		}).
		When().
		Refresh(RefreshTypeNormal).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			diffOutput := errors.NewHandler(t).FailOnErr(fixture.RunCli("app", "diff", app.Name)).(string)
			assert.Empty(t, diffOutput)
		}).
		// verify not committed secret also ignore during diffing
		When().
		WriteFile("secret3.yaml", `
apiVersion: v1
kind: Secret
metadata:
  name: test-secret3
stringData:
  username: test-username`).
		Then().
		And(func(app *Application) {
			diffOutput := errors.NewHandler(t).FailOnErr(fixture.RunCli("app", "diff", app.Name, "--local", "testdata", "--server-side-generate")).(string)
			assert.Empty(t, diffOutput)
		})
}

func TestResourceDiffing(t *testing.T) {
	Given(t).
		Path(guestbookPath).
		When().
		CreateApp().
		Sync().
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(_ *Application) {
			// Patch deployment
			_, err := fixture.KubeClientset.AppsV1().Deployments(fixture.DeploymentNamespace()).Patch(t.Context(),
				"guestbook-ui", types.JSONPatchType, []byte(`[{ "op": "replace", "path": "/spec/template/spec/containers/0/image", "value": "test" }]`), metav1.PatchOptions{})
			require.NoError(t, err)
		}).
		When().
		Refresh(RefreshTypeNormal).
		Then().
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		And(func(app *Application) {
			diffOutput, err := fixture.RunCli("app", "diff", app.Name, "--local", "testdata", "--server-side-generate")
			require.Error(t, err)
			assert.Contains(t, diffOutput, fmt.Sprintf("===== apps/Deployment %s/guestbook-ui ======", fixture.DeploymentNamespace()))
		}).
		Given().
		ResourceOverrides(map[string]ResourceOverride{"apps/Deployment": {
			IgnoreDifferences: OverrideIgnoreDiff{JSONPointers: []string{"/spec/template/spec/containers/0/image"}},
		}}).
		When().
		Refresh(RefreshTypeNormal).
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			diffOutput, err := fixture.RunCli("app", "diff", app.Name, "--local", "testdata", "--server-side-generate")
			require.NoError(t, err)
			assert.Empty(t, diffOutput)
		}).
		Given().
		When().
		// Now we migrate from client-side apply to server-side apply
		// This is necessary, as starting with kubectl 1.26, all previously
		// client-side owned fields have ownership migrated to the manager from
		// the first ssa.
		// More details: https://github.com/kubernetes/kubectl/issues/1337
		PatchApp(`[{
			"op": "add",
			"path": "/spec/syncPolicy",
			"value": { "syncOptions": ["ServerSideApply=true"] }
			}]`).
		Sync().
		And(func() {
			output, err := fixture.RunWithStdin(testdata.SSARevisionHistoryDeployment, "", "kubectl", "apply", "-n", fixture.DeploymentNamespace(), "--server-side=true", "--field-manager=revision-history-manager", "--validate=false", "--force-conflicts", "-f", "-")
			require.NoError(t, err)
			assert.Contains(t, output, "serverside-applied")
		}).
		Refresh(RefreshTypeNormal).
		Then().
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		Given().
		ResourceOverrides(map[string]ResourceOverride{"apps/Deployment": {
			IgnoreDifferences: OverrideIgnoreDiff{
				ManagedFieldsManagers: []string{"revision-history-manager"},
				JSONPointers:          []string{"/spec/template/spec/containers/0/image"},
			},
		}}).
		When().
		Refresh(RefreshTypeNormal).
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Given().
		When().
		Sync().
		PatchApp(`[{
			"op": "add",
			"path": "/spec/syncPolicy",
			"value": { "syncOptions": ["RespectIgnoreDifferences=true"] }
			}]`).
		And(func() {
			deployment, err := fixture.KubeClientset.AppsV1().Deployments(fixture.DeploymentNamespace()).Get(t.Context(), "guestbook-ui", metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, int32(3), *deployment.Spec.RevisionHistoryLimit)
		}).
		And(func() {
			output, err := fixture.RunWithStdin(testdata.SSARevisionHistoryDeployment, "", "kubectl", "apply", "-n", fixture.DeploymentNamespace(), "--server-side=true", "--field-manager=revision-history-manager", "--validate=false", "--force-conflicts", "-f", "-")
			require.NoError(t, err)
			assert.Contains(t, output, "serverside-applied")
		}).
		Then().
		When().Refresh(RefreshTypeNormal).
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(_ *Application) {
			deployment, err := fixture.KubeClientset.AppsV1().Deployments(fixture.DeploymentNamespace()).Get(t.Context(), "guestbook-ui", metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, int32(1), *deployment.Spec.RevisionHistoryLimit)
		}).
		When().Sync().Then().Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(_ *Application) {
			deployment, err := fixture.KubeClientset.AppsV1().Deployments(fixture.DeploymentNamespace()).Get(t.Context(), "guestbook-ui", metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, int32(1), *deployment.Spec.RevisionHistoryLimit)
		})
}

func TestCRDs(t *testing.T) {
	testEdgeCasesApplicationResources(t, "crd-creation", health.HealthStatusHealthy)
}

func TestKnownTypesInCRDDiffing(t *testing.T) {
	dummiesGVR := schema.GroupVersionResource{Group: application.Group, Version: "v1alpha1", Resource: "dummies"}

	Given(t).
		Path("crd-creation").
		When().CreateApp().Sync().Then().
		Expect(OperationPhaseIs(OperationSucceeded)).Expect(SyncStatusIs(SyncStatusCodeSynced)).
		When().
		And(func() {
			dummyResIf := fixture.DynamicClientset.Resource(dummiesGVR).Namespace(fixture.DeploymentNamespace())
			patchData := []byte(`{"spec":{"cpu": "2"}}`)
			errors.NewHandler(t).FailOnErr(dummyResIf.Patch(t.Context(), "dummy-crd-instance", types.MergePatchType, patchData, metav1.PatchOptions{}))
		}).Refresh(RefreshTypeNormal).
		Then().
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		When().
		And(func() {
			require.NoError(t, fixture.SetResourceOverrides(map[string]ResourceOverride{
				"argoproj.io/Dummy": {
					KnownTypeFields: []KnownTypeField{{
						Field: "spec",
						Type:  "core/v1/ResourceList",
					}},
				},
			}))
		}).
		Refresh(RefreshTypeNormal).
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced))
}

func TestDuplicatedClusterResourcesAnnotationTracking(t *testing.T) {
	// This test will fail if the controller fails to fix the tracking annotation for malformed cluster resources
	// (i.e. resources where metadata.namespace is set). Before the bugfix, this test would fail with a diff in the
	// tracking annotation.
	Given(t).
		SetTrackingMethod(string(TrackingMethodAnnotation)).
		Path("duplicated-resources").
		When().
		CreateApp().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		And(func(app *Application) {
			diffOutput, err := fixture.RunCli("app", "diff", app.Name, "--local", "testdata", "--server-side-generate")
			assert.Empty(t, diffOutput)
			require.NoError(t, err)
		})
}

func TestDuplicatedResources(t *testing.T) {
	testEdgeCasesApplicationResources(t, "duplicated-resources", health.HealthStatusHealthy)
}

func TestConfigMap(t *testing.T) {
	testEdgeCasesApplicationResources(t, "config-map", health.HealthStatusHealthy, "my-map  Synced                configmap/my-map created")
}

func testEdgeCasesApplicationResources(t *testing.T, appPath string, statusCode health.HealthStatusCode, message ...string) {
	t.Helper()
	expect := Given(t).
		Path(appPath).
		When().
		CreateApp().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced))
	for i := range message {
		expect = expect.Expect(Success(message[i]))
	}
	expect.
		Expect(HealthIs(statusCode)).
		And(func(app *Application) {
			diffOutput, err := fixture.RunCli("app", "diff", app.Name, "--local", "testdata", "--server-side-generate")
			assert.Empty(t, diffOutput)
			require.NoError(t, err)
		})
}

const actionsConfig = `discovery.lua: return { sample = {} }
definitions:
- name: sample
  action.lua: |
    obj.metadata.labels.sample = 'test'
    return obj`

func TestOldStyleResourceAction(t *testing.T) {
	Given(t).
		Path(guestbookPath).
		ResourceOverrides(map[string]ResourceOverride{"apps/Deployment": {Actions: actionsConfig}}).
		When().
		CreateApp().
		Sync().
		Then().
		And(func(app *Application) {
			closer, client, err := fixture.ArgoCDClientset.NewApplicationClient()
			require.NoError(t, err)
			defer utilio.Close(closer)

			actions, err := client.ListResourceActions(t.Context(), &applicationpkg.ApplicationResourceRequest{
				Name:         &app.Name,
				Group:        ptr.To("apps"),
				Kind:         ptr.To("Deployment"),
				Version:      ptr.To("v1"),
				Namespace:    ptr.To(fixture.DeploymentNamespace()),
				ResourceName: ptr.To("guestbook-ui"),
			})
			require.NoError(t, err)
			assert.Equal(t, []*ResourceAction{{Name: "sample", Disabled: false}}, actions.Actions)

			_, err = client.RunResourceActionV2(t.Context(), &applicationpkg.ResourceActionRunRequestV2{
				Name:         &app.Name,
				Group:        ptr.To("apps"),
				Kind:         ptr.To("Deployment"),
				Version:      ptr.To("v1"),
				Namespace:    ptr.To(fixture.DeploymentNamespace()),
				ResourceName: ptr.To("guestbook-ui"),
				Action:       ptr.To("sample"),
			})
			require.NoError(t, err)

			deployment, err := fixture.KubeClientset.AppsV1().Deployments(fixture.DeploymentNamespace()).Get(t.Context(), "guestbook-ui", metav1.GetOptions{})
			require.NoError(t, err)

			assert.Equal(t, "test", deployment.Labels["sample"])
		})
}

const newStyleActionsConfig = `discovery.lua: return { sample = {} }
definitions:
- name: sample
  action.lua: |
    local os = require("os")

    function deepCopy(object)
      local lookup_table = {}
      local function _copy(obj)
        if type(obj) ~= "table" then
          return obj
        elseif lookup_table[obj] then
          return lookup_table[obj]
        elseif next(obj) == nil then
          return nil
        else
          local new_table = {}
          lookup_table[obj] = new_table
          for key, value in pairs(obj) do
            new_table[_copy(key)] = _copy(value)
          end
          return setmetatable(new_table, getmetatable(obj))
        end
      end
      return _copy(object)
    end

    job = {}
    job.apiVersion = "batch/v1"
    job.kind = "Job"

    job.metadata = {}
    job.metadata.name = obj.metadata.name .. "-123"
    job.metadata.namespace = obj.metadata.namespace

    ownerRef = {}
    ownerRef.apiVersion = obj.apiVersion
    ownerRef.kind = obj.kind
    ownerRef.name = obj.metadata.name
    ownerRef.uid = obj.metadata.uid
    job.metadata.ownerReferences = {}
    job.metadata.ownerReferences[1] = ownerRef

    job.spec = {}
    job.spec.suspend = false
    job.spec.template = {}
    job.spec.template.spec = deepCopy(obj.spec.jobTemplate.spec.template.spec)

    impactedResource = {}
    impactedResource.operation = "create"
    impactedResource.resource = job
    result = {}
    result[1] = impactedResource

    return result`

func TestNewStyleResourceActionPermitted(t *testing.T) {
	Given(t).
		Path(resourceActions).
		ResourceOverrides(map[string]ResourceOverride{"batch/CronJob": {Actions: newStyleActionsConfig}}).
		ProjectSpec(AppProjectSpec{
			SourceRepos:  []string{"*"},
			Destinations: []ApplicationDestination{{Namespace: "*", Server: "*"}},
			NamespaceResourceWhitelist: []metav1.GroupKind{
				{Group: "batch", Kind: "Job"},
				{Group: "batch", Kind: "CronJob"},
			},
		}).
		When().
		CreateApp().
		Sync().
		Wait().
		Then().
		And(func(app *Application) {
			closer, client, err := fixture.ArgoCDClientset.NewApplicationClient()
			require.NoError(t, err)
			defer utilio.Close(closer)

			actions, err := client.ListResourceActions(t.Context(), &applicationpkg.ApplicationResourceRequest{
				Name:         &app.Name,
				Group:        ptr.To("batch"),
				Kind:         ptr.To("CronJob"),
				Version:      ptr.To("v1"),
				Namespace:    ptr.To(fixture.DeploymentNamespace()),
				ResourceName: ptr.To("hello"),
			})
			require.NoError(t, err)
			assert.Equal(t, []*ResourceAction{{Name: "sample", Disabled: false}}, actions.Actions)

			_, err = client.RunResourceActionV2(t.Context(), &applicationpkg.ResourceActionRunRequestV2{
				Name:         &app.Name,
				Group:        ptr.To("batch"),
				Kind:         ptr.To("CronJob"),
				Version:      ptr.To("v1"),
				Namespace:    ptr.To(fixture.DeploymentNamespace()),
				ResourceName: ptr.To("hello"),
				Action:       ptr.To("sample"),
			})
			require.NoError(t, err)

			_, err = fixture.KubeClientset.BatchV1().Jobs(fixture.DeploymentNamespace()).Get(t.Context(), "hello-123", metav1.GetOptions{})
			require.NoError(t, err)
		})
}

const newStyleActionsConfigMixedOk = `discovery.lua: return { sample = {} }
definitions:
- name: sample
  action.lua: |
    local os = require("os")

    function deepCopy(object)
      local lookup_table = {}
      local function _copy(obj)
        if type(obj) ~= "table" then
          return obj
        elseif lookup_table[obj] then
          return lookup_table[obj]
        elseif next(obj) == nil then
          return nil
        else
          local new_table = {}
          lookup_table[obj] = new_table
          for key, value in pairs(obj) do
            new_table[_copy(key)] = _copy(value)
          end
          return setmetatable(new_table, getmetatable(obj))
        end
      end
      return _copy(object)
    end

    job = {}
    job.apiVersion = "batch/v1"
    job.kind = "Job"

    job.metadata = {}
    job.metadata.name = obj.metadata.name .. "-123"
    job.metadata.namespace = obj.metadata.namespace

    ownerRef = {}
    ownerRef.apiVersion = obj.apiVersion
    ownerRef.kind = obj.kind
    ownerRef.name = obj.metadata.name
    ownerRef.uid = obj.metadata.uid
    job.metadata.ownerReferences = {}
    job.metadata.ownerReferences[1] = ownerRef

    job.spec = {}
    job.spec.suspend = false
    job.spec.template = {}
    job.spec.template.spec = deepCopy(obj.spec.jobTemplate.spec.template.spec)

    impactedResource1 = {}
    impactedResource1.operation = "create"
    impactedResource1.resource = job
    result = {}
    result[1] = impactedResource1

    obj.metadata.labels = {}
    obj.metadata.labels["aKey"] = 'aValue'
    impactedResource2 = {}
    impactedResource2.operation = "patch"
    impactedResource2.resource = obj

    result[2] = impactedResource2

    return result`

func TestNewStyleResourceActionMixedOk(t *testing.T) {
	Given(t).
		Path(resourceActions).
		ResourceOverrides(map[string]ResourceOverride{"batch/CronJob": {Actions: newStyleActionsConfigMixedOk}}).
		ProjectSpec(AppProjectSpec{
			SourceRepos:  []string{"*"},
			Destinations: []ApplicationDestination{{Namespace: "*", Server: "*"}},
			NamespaceResourceWhitelist: []metav1.GroupKind{
				{Group: "batch", Kind: "Job"},
				{Group: "batch", Kind: "CronJob"},
			},
		}).
		When().
		CreateApp().
		Sync().
		Wait().
		Then().
		And(func(app *Application) {
			closer, client, err := fixture.ArgoCDClientset.NewApplicationClient()
			require.NoError(t, err)
			defer utilio.Close(closer)

			actions, err := client.ListResourceActions(t.Context(), &applicationpkg.ApplicationResourceRequest{
				Name:         &app.Name,
				Group:        ptr.To("batch"),
				Kind:         ptr.To("CronJob"),
				Version:      ptr.To("v1"),
				Namespace:    ptr.To(fixture.DeploymentNamespace()),
				ResourceName: ptr.To("hello"),
			})
			require.NoError(t, err)
			assert.Equal(t, []*ResourceAction{{Name: "sample", Disabled: false}}, actions.Actions)

			_, err = client.RunResourceActionV2(t.Context(), &applicationpkg.ResourceActionRunRequestV2{
				Name:         &app.Name,
				Group:        ptr.To("batch"),
				Kind:         ptr.To("CronJob"),
				Version:      ptr.To("v1"),
				Namespace:    ptr.To(fixture.DeploymentNamespace()),
				ResourceName: ptr.To("hello"),
				Action:       ptr.To("sample"),
			})
			require.NoError(t, err)

			// Assert new Job was created
			_, err = fixture.KubeClientset.BatchV1().Jobs(fixture.DeploymentNamespace()).Get(t.Context(), "hello-123", metav1.GetOptions{})
			require.NoError(t, err)
			// Assert the original CronJob was patched
			cronJob, err := fixture.KubeClientset.BatchV1().CronJobs(fixture.DeploymentNamespace()).Get(t.Context(), "hello", metav1.GetOptions{})
			assert.Equal(t, "aValue", cronJob.Labels["aKey"])
			require.NoError(t, err)
		})
}

func TestSyncResourceByLabel(t *testing.T) {
	Given(t).
		Path(guestbookPath).
		When().
		CreateApp().
		Sync().
		Then().
		And(func(app *Application) {
			_, _ = fixture.RunCli("app", "sync", app.Name, "--label", "app.kubernetes.io/instance="+app.Name)
		}).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			_, err := fixture.RunCli("app", "sync", app.Name, "--label", "this-label=does-not-exist")
			assert.ErrorContains(t, err, "\"level\":\"fatal\"")
		})
}

func TestSyncResourceByProject(t *testing.T) {
	Given(t).
		Path(guestbookPath).
		When().
		CreateApp().
		Sync().
		Then().
		And(func(app *Application) {
			_, _ = fixture.RunCli("app", "sync", app.Name, "--project", app.Spec.Project)
		}).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			_, err := fixture.RunCli("app", "sync", app.Name, "--project", "this-project-does-not-exist")
			assert.ErrorContains(t, err, "\"level\":\"fatal\"")
		})
}

func TestLocalManifestSync(t *testing.T) {
	Given(t).
		Path(guestbookPath).
		When().
		CreateApp().
		Sync().
		Then().
		And(func(app *Application) {
			res, _ := fixture.RunCli("app", "manifests", app.Name)
			assert.Contains(t, res, "containerPort: 80")
			assert.Contains(t, res, "image: quay.io/argoprojlabs/argocd-e2e-container:0.2")
		}).
		Given().
		LocalPath(guestbookPathLocal).
		When().
		Sync("--local-repo-root", ".").
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			res, _ := fixture.RunCli("app", "manifests", app.Name)
			assert.Contains(t, res, "containerPort: 81")
			assert.Contains(t, res, "image: quay.io/argoprojlabs/argocd-e2e-container:0.3")
		}).
		Given().
		LocalPath("").
		When().
		Sync().
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			res, _ := fixture.RunCli("app", "manifests", app.Name)
			assert.Contains(t, res, "containerPort: 80")
			assert.Contains(t, res, "image: quay.io/argoprojlabs/argocd-e2e-container:0.2")
		})
}

func TestLocalSync(t *testing.T) {
	Given(t).
		// we've got to use Helm as this uses kubeVersion
		Path("helm").
		When().
		CreateApp().
		Then().
		And(func(app *Application) {
			errors.NewHandler(t).FailOnErr(fixture.RunCli("app", "sync", app.Name, "--local", "testdata/helm"))
		})
}

func TestNoLocalSyncWithAutosyncEnabled(t *testing.T) {
	Given(t).
		Path(guestbookPath).
		When().
		CreateApp().
		Sync().
		Then().
		And(func(app *Application) {
			_, err := fixture.RunCli("app", "set", app.Name, "--sync-policy", "automated")
			require.NoError(t, err)

			_, err = fixture.RunCli("app", "sync", app.Name, "--local", guestbookPathLocal)
			require.Error(t, err)
		})
}

func TestLocalSyncDryRunWithAutosyncEnabled(t *testing.T) {
	Given(t).
		Path(guestbookPath).
		When().
		CreateApp().
		Sync().
		Then().
		And(func(app *Application) {
			_, err := fixture.RunCli("app", "set", app.Name, "--sync-policy", "automated")
			require.NoError(t, err)

			appBefore := app.DeepCopy()
			_, err = fixture.RunCli("app", "sync", app.Name, "--dry-run", "--local-repo-root", ".", "--local", guestbookPathLocal)
			require.NoError(t, err)

			appAfter := app.DeepCopy()
			assert.True(t, reflect.DeepEqual(appBefore, appAfter))
		})
}

func TestSyncAsync(t *testing.T) {
	Given(t).
		Path(guestbookPath).
		Async(true).
		When().
		CreateApp().
		Sync().
		Then().
		Expect(Success("")).
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced))
}

// assertResourceActions verifies if view/modify resource actions are successful/failing for given application
func assertResourceActions(t *testing.T, appName string, successful bool) {
	t.Helper()
	assertError := func(err error, message string) {
		if successful {
			require.NoError(t, err)
		} else {
			assert.ErrorContains(t, err, message)
		}
	}

	closer, cdClient := fixture.ArgoCDClientset.NewApplicationClientOrDie()
	defer utilio.Close(closer)

	deploymentResource, err := fixture.KubeClientset.AppsV1().Deployments(fixture.DeploymentNamespace()).Get(t.Context(), "guestbook-ui", metav1.GetOptions{})
	require.NoError(t, err)

	logs, err := cdClient.PodLogs(t.Context(), &applicationpkg.ApplicationPodLogsQuery{
		Group:        ptr.To("apps"),
		Kind:         ptr.To("Deployment"),
		Name:         &appName,
		Namespace:    ptr.To(fixture.DeploymentNamespace()),
		Container:    ptr.To(""),
		SinceSeconds: ptr.To(int64(0)),
		TailLines:    ptr.To(int64(0)),
		Follow:       ptr.To(false),
	})
	require.NoError(t, err)
	_, err = logs.Recv()
	assertError(err, "EOF")

	expectedError := "Deployment apps guestbook-ui not found as part of application " + appName

	_, err = cdClient.ListResourceEvents(t.Context(), &applicationpkg.ApplicationResourceEventsQuery{
		Name:              &appName,
		ResourceName:      ptr.To("guestbook-ui"),
		ResourceNamespace: ptr.To(fixture.DeploymentNamespace()),
		ResourceUID:       ptr.To(string(deploymentResource.UID)),
	})
	assertError(err, fmt.Sprintf("%s not found as part of application %s", "guestbook-ui", appName))

	_, err = cdClient.GetResource(t.Context(), &applicationpkg.ApplicationResourceRequest{
		Name:         &appName,
		ResourceName: ptr.To("guestbook-ui"),
		Namespace:    ptr.To(fixture.DeploymentNamespace()),
		Version:      ptr.To("v1"),
		Group:        ptr.To("apps"),
		Kind:         ptr.To("Deployment"),
	})
	assertError(err, expectedError)

	_, err = cdClient.RunResourceActionV2(t.Context(), &applicationpkg.ResourceActionRunRequestV2{
		Name:         &appName,
		ResourceName: ptr.To("guestbook-ui"),
		Namespace:    ptr.To(fixture.DeploymentNamespace()),
		Version:      ptr.To("v1"),
		Group:        ptr.To("apps"),
		Kind:         ptr.To("Deployment"),
		Action:       ptr.To("restart"),
	})
	assertError(err, expectedError)

	_, err = cdClient.DeleteResource(t.Context(), &applicationpkg.ApplicationResourceDeleteRequest{
		Name:         &appName,
		ResourceName: ptr.To("guestbook-ui"),
		Namespace:    ptr.To(fixture.DeploymentNamespace()),
		Version:      ptr.To("v1"),
		Group:        ptr.To("apps"),
		Kind:         ptr.To("Deployment"),
	})
	assertError(err, expectedError)
}

func TestPermissions(t *testing.T) {
	appCtx := Given(t)
	projName := "argo-project"
	projActions := projectFixture.
		GivenWithSameState(t).
		Name(projName).
		When().
		Create()

	sourceError := fmt.Sprintf("application repo %s is not permitted in project 'argo-project'", fixture.RepoURL(fixture.RepoURLTypeFile))
	destinationError := fmt.Sprintf("application destination server '%s' and namespace '%s' do not match any of the allowed destinations in project 'argo-project'", KubernetesInternalAPIServerAddr, fixture.DeploymentNamespace())

	appCtx.
		Path("guestbook-logs").
		Project(projName).
		When().
		IgnoreErrors().
		// ensure app is not created if project permissions are missing
		CreateApp().
		Then().
		Expect(Error("", sourceError)).
		Expect(Error("", destinationError)).
		When().
		DoNotIgnoreErrors().
		// add missing permissions, create and sync app
		And(func() {
			projActions.AddDestination("*", "*")
			projActions.AddSource("*")
		}).
		CreateApp().
		Sync().
		Then().
		// make sure application resource actiions are successful
		And(func(app *Application) {
			assertResourceActions(t, app.Name, true)
		}).
		When().
		// remove projet permissions and "refresh" app
		And(func() {
			projActions.UpdateProject(func(proj *AppProject) {
				proj.Spec.Destinations = nil
				proj.Spec.SourceRepos = nil
			})
		}).
		Refresh(RefreshTypeNormal).
		Then().
		// ensure app resource tree is empty when source/destination permissions are missing
		Expect(Condition(ApplicationConditionInvalidSpecError, destinationError)).
		Expect(Condition(ApplicationConditionInvalidSpecError, sourceError)).
		And(func(app *Application) {
			closer, cdClient := fixture.ArgoCDClientset.NewApplicationClientOrDie()
			defer utilio.Close(closer)
			appName, appNs := argo.ParseFromQualifiedName(app.Name, "")
			fmt.Printf("APP NAME: %s\n", appName)
			tree, err := cdClient.ResourceTree(t.Context(), &applicationpkg.ResourcesQuery{ApplicationName: &appName, AppNamespace: &appNs})
			require.NoError(t, err)
			assert.Empty(t, tree.Nodes)
			assert.Empty(t, tree.OrphanedNodes)
		}).
		When().
		// add missing permissions but deny management of Deployment kind
		And(func() {
			projActions.
				AddDestination("*", "*").
				AddSource("*").
				UpdateProject(func(proj *AppProject) {
					proj.Spec.NamespaceResourceBlacklist = []metav1.GroupKind{{Group: "*", Kind: "Deployment"}}
				})
		}).
		Refresh(RefreshTypeNormal).
		Then().
		// make sure application resource actiions are failing
		And(func(_ *Application) {
			assertResourceActions(t, "test-permissions", false)
		})
}

func TestPermissionWithScopedRepo(t *testing.T) {
	projName := "argo-project"
	fixture.EnsureCleanState(t)
	projectFixture.
		Given(t).
		Name(projName).
		Destination("*,*").
		When().
		Create().
		AddSource("*")

	repoFixture.GivenWithSameState(t).
		When().
		Path(fixture.RepoURL(fixture.RepoURLTypeFile)).
		Project(projName).
		Create()

	GivenWithSameState(t).
		Project(projName).
		RepoURLType(fixture.RepoURLTypeFile).
		Path("two-nice-pods").
		When().
		PatchFile("pod-1.yaml", `[{"op": "add", "path": "/metadata/annotations", "value": {"argocd.argoproj.io/sync-options": "Prune=false"}}]`).
		CreateApp().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		When().
		DeleteFile("pod-1.yaml").
		Refresh(RefreshTypeHard).
		IgnoreErrors().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		Expect(ResourceSyncStatusIs("Pod", "pod-1", SyncStatusCodeOutOfSync))
}

func TestPermissionDeniedWithScopedRepo(t *testing.T) {
	projName := "argo-project"
	projectFixture.
		Given(t).
		Name(projName).
		Destination("*,*").
		When().
		Create()

	repoFixture.GivenWithSameState(t).
		When().
		Path(fixture.RepoURL(fixture.RepoURLTypeFile)).
		Create()

	GivenWithSameState(t).
		Project(projName).
		RepoURLType(fixture.RepoURLTypeFile).
		Path("two-nice-pods").
		When().
		PatchFile("pod-1.yaml", `[{"op": "add", "path": "/metadata/annotations", "value": {"argocd.argoproj.io/sync-options": "Prune=false"}}]`).
		IgnoreErrors().
		CreateApp().
		Then().
		Expect(Error("", "is not permitted in project"))
}

func TestPermissionDeniedWithNegatedNamespace(t *testing.T) {
	projName := "argo-project"
	projectFixture.
		Given(t).
		Name(projName).
		Destination("*,!*test-permission-denied-with-negated-namespace*").
		When().
		Create()

	repoFixture.GivenWithSameState(t).
		When().
		Path(fixture.RepoURL(fixture.RepoURLTypeFile)).
		Project(projName).
		Create()

	GivenWithSameState(t).
		Project(projName).
		RepoURLType(fixture.RepoURLTypeFile).
		Path("two-nice-pods").
		When().
		PatchFile("pod-1.yaml", `[{"op": "add", "path": "/metadata/annotations", "value": {"argocd.argoproj.io/sync-options": "Prune=false"}}]`).
		IgnoreErrors().
		CreateApp().
		Then().
		Expect(Error("", "do not match any of the allowed destinations in project"))
}

func TestPermissionDeniedWithNegatedServer(t *testing.T) {
	projName := "argo-project"
	projectFixture.
		Given(t).
		Name(projName).
		Destination("!https://kubernetes.default.svc,*").
		When().
		Create()

	repoFixture.GivenWithSameState(t).
		When().
		Path(fixture.RepoURL(fixture.RepoURLTypeFile)).
		Project(projName).
		Create()

	GivenWithSameState(t).
		Project(projName).
		RepoURLType(fixture.RepoURLTypeFile).
		Path("two-nice-pods").
		When().
		PatchFile("pod-1.yaml", `[{"op": "add", "path": "/metadata/annotations", "value": {"argocd.argoproj.io/sync-options": "Prune=false"}}]`).
		IgnoreErrors().
		CreateApp().
		Then().
		Expect(Error("", "do not match any of the allowed destinations in project"))
}

// make sure that if we deleted a resource from the app, it is not pruned if annotated with Prune=false
func TestSyncOptionPruneFalse(t *testing.T) {
	Given(t).
		Path("two-nice-pods").
		When().
		PatchFile("pod-1.yaml", `[{"op": "add", "path": "/metadata/annotations", "value": {"argocd.argoproj.io/sync-options": "Prune=false"}}]`).
		CreateApp().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		When().
		DeleteFile("pod-1.yaml").
		Refresh(RefreshTypeHard).
		IgnoreErrors().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		Expect(ResourceSyncStatusIs("Pod", "pod-1", SyncStatusCodeOutOfSync))
}

// make sure that if we have an invalid manifest, we can add it if we disable validation, we get a server error rather than a client error
func TestSyncOptionValidateFalse(t *testing.T) {
	Given(t).
		Path("crd-validation").
		When().
		CreateApp().
		Then().
		Expect(Success("")).
		When().
		IgnoreErrors().
		Sync().
		Then().
		// client error. K8s API changed error message w/ 1.25, so for now, we need to check both
		Expect(ErrorRegex("error validating data|of type int32", "")).
		When().
		PatchFile("deployment.yaml", `[{"op": "add", "path": "/metadata/annotations", "value": {"argocd.argoproj.io/sync-options": "Validate=false"}}]`).
		Sync().
		Then().
		// server error
		Expect(Error("cannot be handled as a Deployment", ""))
}

// make sure that, if we have a resource that needs pruning, but we're ignoring it, the app is in-sync
func TestCompareOptionIgnoreExtraneous(t *testing.T) {
	Given(t).
		Prune(false).
		Path("two-nice-pods").
		When().
		PatchFile("pod-1.yaml", `[{"op": "add", "path": "/metadata/annotations", "value": {"argocd.argoproj.io/compare-options": "IgnoreExtraneous"}}]`).
		CreateApp().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		When().
		DeleteFile("pod-1.yaml").
		Refresh(RefreshTypeHard).
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			assert.Len(t, app.Status.Resources, 2)
			statusByName := map[string]SyncStatusCode{}
			for _, r := range app.Status.Resources {
				statusByName[r.Name] = r.Status
			}
			assert.Equal(t, SyncStatusCodeOutOfSync, statusByName["pod-1"])
			assert.Equal(t, SyncStatusCodeSynced, statusByName["pod-2"])
		}).
		When().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced))
}

func TestSourceNamespaceCanBeMigratedToManagedNamespaceWithoutBeingPrunedOrOutOfSync(t *testing.T) {
	Given(t).
		Prune(true).
		Path("guestbook-with-plain-namespace-manifest").
		When().
		PatchFile("guestbook-ui-namespace.yaml", fmt.Sprintf(`[{"op": "replace", "path": "/metadata/name", "value": %q}]`, fixture.DeploymentNamespace())).
		CreateApp().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		When().
		PatchApp(`[{
				"op": "add",
				"path": "/spec/syncPolicy",
				"value": { "prune": true, "syncOptions": ["PrunePropagationPolicy=foreground"], "managedNamespaceMetadata": { "labels": { "foo": "bar" } } }
				}]`).
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			assert.Equal(t, &ManagedNamespaceMetadata{Labels: map[string]string{"foo": "bar"}}, app.Spec.SyncPolicy.ManagedNamespaceMetadata)
		}).
		When().
		DeleteFile("guestbook-ui-namespace.yaml").
		Refresh(RefreshTypeHard).
		Sync().
		Wait().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced))
}

func TestSelfManagedApps(t *testing.T) {
	Given(t).
		Path("self-managed-app").
		When().
		PatchFile("resources.yaml", fmt.Sprintf(`[{"op": "replace", "path": "/spec/source/repoURL", "value": %q}]`, fixture.RepoURL(fixture.RepoURLTypeFile))).
		CreateApp().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(a *Application) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second*3)
			defer cancel()

			reconciledCount := 0
			var lastReconciledAt *metav1.Time
			for event := range fixture.ArgoCDClientset.WatchApplicationWithRetry(ctx, a.Name, a.ResourceVersion) {
				reconciledAt := event.Application.Status.ReconciledAt
				if reconciledAt == nil {
					reconciledAt = &metav1.Time{}
				}
				if lastReconciledAt != nil && !lastReconciledAt.Equal(reconciledAt) {
					reconciledCount = reconciledCount + 1
				}
				lastReconciledAt = reconciledAt
			}

			assert.Less(t, reconciledCount, 3, "Application was reconciled too many times")
		})
}

func TestExcludedResource(t *testing.T) {
	Given(t).
		ResourceOverrides(map[string]ResourceOverride{"apps/Deployment": {Actions: actionsConfig}}).
		Path(guestbookPath).
		ResourceFilter(settings.ResourcesFilter{
			ResourceExclusions: []settings.FilteredResource{{Kinds: []string{kube.DeploymentKind}}},
		}).
		When().
		CreateApp().
		Sync().
		Refresh(RefreshTypeNormal).
		Then().
		Expect(Condition(ApplicationConditionExcludedResourceWarning, "Resource apps/Deployment guestbook-ui is excluded in the settings"))
}

func TestRevisionHistoryLimit(t *testing.T) {
	Given(t).
		Path("config-map").
		When().
		CreateApp().
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			assert.Len(t, app.Status.History, 1)
		}).
		When().
		AppSet("--revision-history-limit", "1").
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			assert.Len(t, app.Status.History, 1)
		})
}

func TestOrphanedResource(t *testing.T) {
	fixture.SkipOnEnv(t, "OPENSHIFT")
	Given(t).
		ProjectSpec(AppProjectSpec{
			SourceRepos:       []string{"*"},
			Destinations:      []ApplicationDestination{{Namespace: "*", Server: "*"}},
			OrphanedResources: &OrphanedResourcesMonitorSettings{Warn: ptr.To(true)},
		}).
		Path(guestbookPath).
		When().
		CreateApp().
		Sync().
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(NoConditions()).
		When().
		And(func() {
			errors.NewHandler(t).FailOnErr(fixture.KubeClientset.CoreV1().ConfigMaps(fixture.DeploymentNamespace()).Create(t.Context(), &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "orphaned-configmap",
				},
			}, metav1.CreateOptions{}))
		}).
		Refresh(RefreshTypeNormal).
		Then().
		Expect(Condition(ApplicationConditionOrphanedResourceWarning, "Application has 1 orphaned resources")).
		And(func(app *Application) {
			output, err := fixture.RunCli("app", "resources", app.Name)
			require.NoError(t, err)
			assert.Contains(t, output, "orphaned-configmap")
		}).
		Given().
		ProjectSpec(AppProjectSpec{
			SourceRepos:       []string{"*"},
			Destinations:      []ApplicationDestination{{Namespace: "*", Server: "*"}},
			OrphanedResources: &OrphanedResourcesMonitorSettings{Warn: ptr.To(true), Ignore: []OrphanedResourceKey{{Group: "Test", Kind: "ConfigMap"}}},
		}).
		When().
		Refresh(RefreshTypeNormal).
		Then().
		Expect(Condition(ApplicationConditionOrphanedResourceWarning, "Application has 1 orphaned resources")).
		And(func(app *Application) {
			output, err := fixture.RunCli("app", "resources", app.Name)
			require.NoError(t, err)
			assert.Contains(t, output, "orphaned-configmap")
		}).
		Given().
		ProjectSpec(AppProjectSpec{
			SourceRepos:       []string{"*"},
			Destinations:      []ApplicationDestination{{Namespace: "*", Server: "*"}},
			OrphanedResources: &OrphanedResourcesMonitorSettings{Warn: ptr.To(true), Ignore: []OrphanedResourceKey{{Kind: "ConfigMap"}}},
		}).
		When().
		Refresh(RefreshTypeNormal).
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(NoConditions()).
		And(func(app *Application) {
			output, err := fixture.RunCli("app", "resources", app.Name)
			require.NoError(t, err)
			assert.NotContains(t, output, "orphaned-configmap")
		}).
		Given().
		ProjectSpec(AppProjectSpec{
			SourceRepos:       []string{"*"},
			Destinations:      []ApplicationDestination{{Namespace: "*", Server: "*"}},
			OrphanedResources: &OrphanedResourcesMonitorSettings{Warn: ptr.To(true), Ignore: []OrphanedResourceKey{{Kind: "ConfigMap", Name: "orphaned-configmap"}}},
		}).
		When().
		Refresh(RefreshTypeNormal).
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(NoConditions()).
		And(func(app *Application) {
			output, err := fixture.RunCli("app", "resources", app.Name)
			require.NoError(t, err)
			assert.NotContains(t, output, "orphaned-configmap")
		}).
		Given().
		ProjectSpec(AppProjectSpec{
			SourceRepos:       []string{"*"},
			Destinations:      []ApplicationDestination{{Namespace: "*", Server: "*"}},
			OrphanedResources: nil,
		}).
		When().
		Refresh(RefreshTypeNormal).
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(NoConditions())
}

func TestNotPermittedResources(t *testing.T) {
	ctx := Given(t)

	pathType := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sample-ingress",
			Labels: map[string]string{
				common.LabelKeyAppInstance: ctx.GetName(),
			},
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path: "/",
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: "guestbook-ui",
									Port: networkingv1.ServiceBackendPort{Number: 80},
								},
							},
							PathType: &pathType,
						}},
					},
				},
			}},
		},
	}
	defer func() {
		log.Infof("Ingress 'sample-ingress' deleted from %s", fixture.TestNamespace())
		require.NoError(t, fixture.KubeClientset.NetworkingV1().Ingresses(fixture.TestNamespace()).Delete(t.Context(), "sample-ingress", metav1.DeleteOptions{}))
	}()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "guestbook-ui",
			Labels: map[string]string{
				common.LabelKeyAppInstance: ctx.GetName(),
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{
				Port:       80,
				TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: 80},
			}},
			Selector: map[string]string{
				"app": "guestbook-ui",
			},
		},
	}

	ctx.ProjectSpec(AppProjectSpec{
		SourceRepos:  []string{"*"},
		Destinations: []ApplicationDestination{{Namespace: fixture.DeploymentNamespace(), Server: "*"}},
		NamespaceResourceBlacklist: []metav1.GroupKind{
			{Group: "", Kind: "Service"},
		},
	}).
		And(func() {
			errors.NewHandler(t).FailOnErr(fixture.KubeClientset.NetworkingV1().Ingresses(fixture.TestNamespace()).Create(t.Context(), ingress, metav1.CreateOptions{}))
			errors.NewHandler(t).FailOnErr(fixture.KubeClientset.CoreV1().Services(fixture.DeploymentNamespace()).Create(t.Context(), svc, metav1.CreateOptions{}))
		}).
		Path(guestbookPath).
		When().
		CreateApp().
		Then().
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		And(func(app *Application) {
			statusByKind := make(map[string]ResourceStatus)
			for _, res := range app.Status.Resources {
				statusByKind[res.Kind] = res
			}
			_, hasIngress := statusByKind[kube.IngressKind]
			assert.False(t, hasIngress, "Ingress is prohibited not managed object and should be even visible to user")
			serviceStatus := statusByKind[kube.ServiceKind]
			assert.Equal(t, SyncStatusCodeUnknown, serviceStatus.Status, "Service is prohibited managed resource so should be set to Unknown")
			deploymentStatus := statusByKind[kube.DeploymentKind]
			assert.Equal(t, SyncStatusCodeOutOfSync, deploymentStatus.Status)
		}).
		When().
		Delete(true).
		Then().
		Expect(DoesNotExist())

	// Make sure prohibited resources are not deleted during application deletion
	errors.NewHandler(t).FailOnErr(fixture.KubeClientset.NetworkingV1().Ingresses(fixture.TestNamespace()).Get(t.Context(), "sample-ingress", metav1.GetOptions{}))
	errors.NewHandler(t).FailOnErr(fixture.KubeClientset.CoreV1().Services(fixture.DeploymentNamespace()).Get(t.Context(), "guestbook-ui", metav1.GetOptions{}))
}

func TestSyncWithInfos(t *testing.T) {
	expectedInfo := make([]*Info, 2)
	expectedInfo[0] = &Info{Name: "name1", Value: "val1"}
	expectedInfo[1] = &Info{Name: "name2", Value: "val2"}

	Given(t).
		Path(guestbookPath).
		When().
		CreateApp().
		Then().
		And(func(app *Application) {
			_, err := fixture.RunCli("app", "sync", app.Name,
				"--info", fmt.Sprintf("%s=%s", expectedInfo[0].Name, expectedInfo[0].Value),
				"--info", fmt.Sprintf("%s=%s", expectedInfo[1].Name, expectedInfo[1].Value))
			require.NoError(t, err)
		}).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			assert.ElementsMatch(t, app.Status.OperationState.Operation.Info, expectedInfo)
		})
}

// Given: argocd app create does not provide --dest-namespace
//
//	Manifest contains resource console which does not require namespace
//
// Expect: no app.Status.Conditions
func TestCreateAppWithNoNameSpaceForGlobalResource(t *testing.T) {
	Given(t).
		Path(globalWithNoNameSpace).
		When().
		CreateWithNoNameSpace().
		Then().
		And(func(app *Application) {
			app, err := fixture.AppClientset.ArgoprojV1alpha1().Applications(fixture.TestNamespace()).Get(t.Context(), app.Name, metav1.GetOptions{})
			require.NoError(t, err)
			assert.Empty(t, app.Status.Conditions)
		})
}

// Given: argocd app create does not provide --dest-namespace
//
//	Manifest contains resource deployment, and service which requires namespace
//	Deployment and service do not have namespace in manifest
//
// Expect: app.Status.Conditions for deployment ans service which does not have namespace in manifest
func TestCreateAppWithNoNameSpaceWhenRequired(t *testing.T) {
	Given(t).
		Path(guestbookPath).
		When().
		CreateWithNoNameSpace().
		Refresh(RefreshTypeNormal).
		Then().
		And(func(app *Application) {
			updatedApp, err := fixture.AppClientset.ArgoprojV1alpha1().Applications(fixture.TestNamespace()).Get(t.Context(), app.Name, metav1.GetOptions{})
			require.NoError(t, err)

			assert.Len(t, updatedApp.Status.Conditions, 2)
			assert.Equal(t, ApplicationConditionInvalidSpecError, updatedApp.Status.Conditions[0].Type)
			assert.Equal(t, ApplicationConditionInvalidSpecError, updatedApp.Status.Conditions[1].Type)
		})
}

// Given: argocd app create does not provide --dest-namespace
//
//	Manifest contains resource deployment, and service which requires namespace
//	Some deployment and service has namespace in manifest
//	Some deployment and service does not have namespace in manifest
//
// Expect: app.Status.Conditions for deployment and service which does not have namespace in manifest
func TestCreateAppWithNoNameSpaceWhenRequired2(t *testing.T) {
	Given(t).
		Path(guestbookWithNamespace).
		When().
		CreateWithNoNameSpace().
		Refresh(RefreshTypeNormal).
		Then().
		And(func(app *Application) {
			updatedApp, err := fixture.AppClientset.ArgoprojV1alpha1().Applications(fixture.TestNamespace()).Get(t.Context(), app.Name, metav1.GetOptions{})
			require.NoError(t, err)

			assert.Len(t, updatedApp.Status.Conditions, 2)
			assert.Equal(t, ApplicationConditionInvalidSpecError, updatedApp.Status.Conditions[0].Type)
			assert.Equal(t, ApplicationConditionInvalidSpecError, updatedApp.Status.Conditions[1].Type)
		})
}

func TestCreateAppWithInClusterDisabled(t *testing.T) {
	Given(t).
		Path(guestbookPath).
		DestServer(KubernetesInternalAPIServerAddr).
		When().
		SetParamInSettingConfigMap("cluster.inClusterEnabled", "false").
		IgnoreErrors().
		CreateApp().
		Then().
		// RPC error messages are quoted: time="2024-12-18T04:13:58Z" level=fatal msg="<Quoted value>"
		Expect(Error("", fmt.Sprintf(`cluster \"%s\" is disabled`, KubernetesInternalAPIServerAddr)))
}

func TestListResource(t *testing.T) {
	fixture.SkipOnEnv(t, "OPENSHIFT")
	Given(t).
		ProjectSpec(AppProjectSpec{
			SourceRepos:       []string{"*"},
			Destinations:      []ApplicationDestination{{Namespace: "*", Server: "*"}},
			OrphanedResources: &OrphanedResourcesMonitorSettings{Warn: ptr.To(true)},
		}).
		Path(guestbookPath).
		When().
		CreateApp().
		Sync().
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(NoConditions()).
		When().
		And(func() {
			errors.NewHandler(t).FailOnErr(fixture.KubeClientset.CoreV1().ConfigMaps(fixture.DeploymentNamespace()).Create(t.Context(), &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "orphaned-configmap",
				},
			}, metav1.CreateOptions{}))
		}).
		Refresh(RefreshTypeNormal).
		Then().
		Expect(Condition(ApplicationConditionOrphanedResourceWarning, "Application has 1 orphaned resources")).
		And(func(app *Application) {
			output, err := fixture.RunCli("app", "resources", app.Name)
			require.NoError(t, err)
			assert.Contains(t, output, "orphaned-configmap")
			assert.Contains(t, output, "guestbook-ui")
		}).
		And(func(app *Application) {
			output, err := fixture.RunCli("app", "resources", app.Name, "--orphaned=true")
			require.NoError(t, err)
			assert.Contains(t, output, "orphaned-configmap")
			assert.NotContains(t, output, "guestbook-ui")
		}).
		And(func(app *Application) {
			output, err := fixture.RunCli("app", "resources", app.Name, "--orphaned=false")
			require.NoError(t, err)
			assert.NotContains(t, output, "orphaned-configmap")
			assert.Contains(t, output, "guestbook-ui")
		}).
		Given().
		ProjectSpec(AppProjectSpec{
			SourceRepos:       []string{"*"},
			Destinations:      []ApplicationDestination{{Namespace: "*", Server: "*"}},
			OrphanedResources: nil,
		}).
		When().
		Refresh(RefreshTypeNormal).
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(NoConditions())
}

// Given application is set with --sync-option CreateNamespace=true
//
//	application --dest-namespace does not exist
//
// Verity application --dest-namespace is created
//
//	application sync successful
//	when application is deleted, --dest-namespace is not deleted
func TestNamespaceAutoCreation(t *testing.T) {
	fixture.SkipOnEnv(t, "OPENSHIFT")
	updatedNamespace := getNewNamespace(t)
	defer func() {
		if !t.Skipped() {
			_, err := fixture.Run("", "kubectl", "delete", "namespace", updatedNamespace)
			require.NoError(t, err)
		}
	}()
	Given(t).
		Timeout(30).
		Path("guestbook").
		When().
		CreateApp("--sync-option", "CreateNamespace=true").
		Then().
		And(func(_ *Application) {
			// Make sure the namespace we are about to update to does not exist
			_, err := fixture.Run("", "kubectl", "get", "namespace", updatedNamespace)
			assert.ErrorContains(t, err, "not found")
		}).
		When().
		AppSet("--dest-namespace", updatedNamespace).
		Sync().
		Then().
		Expect(Success("")).
		Expect(OperationPhaseIs(OperationSucceeded)).Expect(ResourceHealthWithNamespaceIs("Deployment", "guestbook-ui", updatedNamespace, health.HealthStatusHealthy)).
		Expect(ResourceHealthWithNamespaceIs("Deployment", "guestbook-ui", updatedNamespace, health.HealthStatusHealthy)).
		Expect(ResourceSyncStatusWithNamespaceIs("Deployment", "guestbook-ui", updatedNamespace, SyncStatusCodeSynced)).
		Expect(ResourceSyncStatusWithNamespaceIs("Deployment", "guestbook-ui", updatedNamespace, SyncStatusCodeSynced)).
		When().
		Delete(true).
		Then().
		Expect(Success("")).
		And(func(_ *Application) {
			// Verify delete app does not delete the namespace auto created
			output, err := fixture.Run("", "kubectl", "get", "namespace", updatedNamespace)
			require.NoError(t, err)
			assert.Contains(t, output, updatedNamespace)
		})
}

func TestFailedSyncWithRetry(t *testing.T) {
	Given(t).
		Path("hook").
		When().
		PatchFile("hook.yaml", `[{"op": "replace", "path": "/metadata/annotations", "value": {"argocd.argoproj.io/hook": "PreSync"}}]`).
		// make hook fail
		PatchFile("hook.yaml", `[{"op": "replace", "path": "/spec/containers/0/command", "value": ["false"]}]`).
		CreateApp().
		IgnoreErrors().
		Sync("--retry-limit=1", "--retry-backoff-duration=1s").
		Then().
		Expect(OperationPhaseIs(OperationFailed)).
		Expect(OperationMessageContains("retried 1 times"))
}

func TestCreateDisableValidation(t *testing.T) {
	Given(t).
		Path("baddir").
		When().
		CreateApp("--validate=false").
		Then().
		And(func(app *Application) {
			_, err := fixture.RunCli("app", "create", app.Name, "--upsert", "--validate=false", "--repo", fixture.RepoURL(fixture.RepoURLTypeFile),
				"--path", "baddir2", "--project", app.Spec.Project, "--dest-server", KubernetesInternalAPIServerAddr, "--dest-namespace", fixture.DeploymentNamespace())
			require.NoError(t, err)
		}).
		When().
		AppSet("--path", "baddir3", "--validate=false")
}

func TestCreateFromPartialFile(t *testing.T) {
	partialApp := `metadata:
  labels:
    labels.local/from-file: file
    labels.local/from-args: file
  annotations:
    annotations.local/from-file: file
  finalizers:
  - resources-finalizer.argocd.argoproj.io
spec:
  syncPolicy:
    automated:
      prune: true
`

	path := "helm-values"
	Given(t).
		When().
		// app should be auto-synced once created
		CreateFromPartialFile(partialApp, "--path", path, "-l", "labels.local/from-args=args", "--helm-set", "foo=foo").
		Then().
		Expect(Success("")).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(NoConditions()).
		And(func(app *Application) {
			assert.Equal(t, map[string]string{"labels.local/from-file": "file", "labels.local/from-args": "args"}, app.Labels)
			assert.Equal(t, map[string]string{"annotations.local/from-file": "file"}, app.Annotations)
			assert.Equal(t, []string{ResourcesFinalizerName}, app.Finalizers)
			assert.Equal(t, path, app.Spec.GetSource().Path)
			assert.Equal(t, []HelmParameter{{Name: "foo", Value: "foo"}}, app.Spec.GetSource().Helm.Parameters)
		})
}

// Ensure actions work when using a resource action that modifies status and/or spec
func TestCRDStatusSubresourceAction(t *testing.T) {
	actions := `
discovery.lua: |
  actions = {}
  actions["update-spec"] = {["disabled"] = false}
  actions["update-status"] = {["disabled"] = false}
  actions["update-both"] = {["disabled"] = false}
  return actions
definitions:
- name: update-both
  action.lua: |
    obj.spec = {}
    obj.spec.foo = "update-both"
    obj.status = {}
    obj.status.bar = "update-both"
    return obj
- name: update-spec
  action.lua: |
    obj.spec = {}
    obj.spec.foo = "update-spec"
    return obj
- name: update-status
  action.lua: |
    obj.status = {}
    obj.status.bar = "update-status"
    return obj
`
	Given(t).
		Path("crd-subresource").
		And(func() {
			require.NoError(t, fixture.SetResourceOverrides(map[string]ResourceOverride{
				"argoproj.io/StatusSubResource": {
					Actions: actions,
				},
				"argoproj.io/NonStatusSubResource": {
					Actions: actions,
				},
			}))
		}).
		When().CreateApp().Sync().Then().
		Expect(OperationPhaseIs(OperationSucceeded)).Expect(SyncStatusIs(SyncStatusCodeSynced)).
		When().
		Refresh(RefreshTypeNormal).
		Then().
		// tests resource actions on a CRD using status subresource
		And(func(app *Application) {
			_, err := fixture.RunCli("app", "actions", "run", app.Name, "--kind", "StatusSubResource", "update-both")
			require.NoError(t, err)
			text := errors.NewHandler(t).FailOnErr(fixture.Run(".", "kubectl", "-n", app.Spec.Destination.Namespace, "get", "statussubresources", "status-subresource", "-o", "jsonpath={.spec.foo}")).(string)
			assert.Equal(t, "update-both", text)
			text = errors.NewHandler(t).FailOnErr(fixture.Run(".", "kubectl", "-n", app.Spec.Destination.Namespace, "get", "statussubresources", "status-subresource", "-o", "jsonpath={.status.bar}")).(string)
			assert.Equal(t, "update-both", text)

			_, err = fixture.RunCli("app", "actions", "run", app.Name, "--kind", "StatusSubResource", "update-spec")
			require.NoError(t, err)
			text = errors.NewHandler(t).FailOnErr(fixture.Run(".", "kubectl", "-n", app.Spec.Destination.Namespace, "get", "statussubresources", "status-subresource", "-o", "jsonpath={.spec.foo}")).(string)
			assert.Equal(t, "update-spec", text)

			_, err = fixture.RunCli("app", "actions", "run", app.Name, "--kind", "StatusSubResource", "update-status")
			require.NoError(t, err)
			text = errors.NewHandler(t).FailOnErr(fixture.Run(".", "kubectl", "-n", app.Spec.Destination.Namespace, "get", "statussubresources", "status-subresource", "-o", "jsonpath={.status.bar}")).(string)
			assert.Equal(t, "update-status", text)
		}).
		// tests resource actions on a CRD *not* using status subresource
		And(func(app *Application) {
			_, err := fixture.RunCli("app", "actions", "run", app.Name, "--kind", "NonStatusSubResource", "update-both")
			require.NoError(t, err)
			text := errors.NewHandler(t).FailOnErr(fixture.Run(".", "kubectl", "-n", app.Spec.Destination.Namespace, "get", "nonstatussubresources", "non-status-subresource", "-o", "jsonpath={.spec.foo}")).(string)
			assert.Equal(t, "update-both", text)
			text = errors.NewHandler(t).FailOnErr(fixture.Run(".", "kubectl", "-n", app.Spec.Destination.Namespace, "get", "nonstatussubresources", "non-status-subresource", "-o", "jsonpath={.status.bar}")).(string)
			assert.Equal(t, "update-both", text)

			_, err = fixture.RunCli("app", "actions", "run", app.Name, "--kind", "NonStatusSubResource", "update-spec")
			require.NoError(t, err)
			text = errors.NewHandler(t).FailOnErr(fixture.Run(".", "kubectl", "-n", app.Spec.Destination.Namespace, "get", "nonstatussubresources", "non-status-subresource", "-o", "jsonpath={.spec.foo}")).(string)
			assert.Equal(t, "update-spec", text)

			_, err = fixture.RunCli("app", "actions", "run", app.Name, "--kind", "NonStatusSubResource", "update-status")
			require.NoError(t, err)
			text = errors.NewHandler(t).FailOnErr(fixture.Run(".", "kubectl", "-n", app.Spec.Destination.Namespace, "get", "nonstatussubresources", "non-status-subresource", "-o", "jsonpath={.status.bar}")).(string)
			assert.Equal(t, "update-status", text)
		})
}

func TestAppLogs(t *testing.T) {
	t.SkipNow() // Too flaky. https://github.com/argoproj/argo-cd/issues/13834
	fixture.SkipOnEnv(t, "OPENSHIFT")
	Given(t).
		Path("guestbook-logs").
		When().
		CreateApp().
		Sync().
		Then().
		Expect(HealthIs(health.HealthStatusHealthy)).
		And(func(app *Application) {
			out, err := fixture.RunCliWithRetry(appLogsRetryCount, "app", "logs", app.Name, "--kind", "Deployment", "--group", "", "--name", "guestbook-ui")
			require.NoError(t, err)
			assert.Contains(t, out, "Hi")
		}).
		And(func(app *Application) {
			out, err := fixture.RunCliWithRetry(appLogsRetryCount, "app", "logs", app.Name, "--kind", "Pod")
			require.NoError(t, err)
			assert.Contains(t, out, "Hi")
		}).
		And(func(app *Application) {
			out, err := fixture.RunCliWithRetry(appLogsRetryCount, "app", "logs", app.Name, "--kind", "Service")
			require.NoError(t, err)
			assert.NotContains(t, out, "Hi")
		})
}

func TestAppWaitOperationInProgress(t *testing.T) {
	ctx := Given(t)
	ctx.
		And(func() {
			require.NoError(t, fixture.SetResourceOverrides(map[string]ResourceOverride{
				"batch/Job": {
					HealthLua: `return { status = 'Running' }`,
				},
				"apps/Deployment": {
					HealthLua: `return { status = 'Suspended' }`,
				},
			}))
		}).
		Async(true).
		Path("hook-and-deployment").
		When().
		CreateApp().
		Sync().
		Then().
		// stuck in running state
		Expect(OperationPhaseIs(OperationRunning)).
		When().
		And(func() {
			_, err := fixture.RunCli("app", "wait", ctx.AppName(), "--suspended")
			require.NoError(t, err)
		})
}

func TestSyncOptionReplace(t *testing.T) {
	Given(t).
		Path("config-map").
		When().
		PatchFile("config-map.yaml", `[{"op": "add", "path": "/metadata/annotations", "value": {"argocd.argoproj.io/sync-options": "Replace=true"}}]`).
		CreateApp().
		Sync().
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			assert.Equal(t, "configmap/my-map created", app.Status.OperationState.SyncResult.Resources[0].Message)
		}).
		When().
		Sync().
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			assert.Equal(t, "configmap/my-map replaced", app.Status.OperationState.SyncResult.Resources[0].Message)
		})
}

func TestSyncOptionReplaceFromCLI(t *testing.T) {
	Given(t).
		Path("config-map").
		Replace().
		When().
		CreateApp().
		Sync().
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			assert.Equal(t, "configmap/my-map created", app.Status.OperationState.SyncResult.Resources[0].Message)
		}).
		When().
		Sync().
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			assert.Equal(t, "configmap/my-map replaced", app.Status.OperationState.SyncResult.Resources[0].Message)
		})
}

func TestDiscoverNewCommit(t *testing.T) {
	var sha string
	Given(t).
		Path("config-map").
		When().
		CreateApp().
		Sync().
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			sha = app.Status.Sync.Revision
			assert.NotEmpty(t, sha)
		}).
		When().
		PatchFile("config-map.yaml", `[{"op": "replace", "path": "/data/foo", "value": "hello"}]`).
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		// make sure new commit is not discovered immediately after push
		And(func(app *Application) {
			assert.Equal(t, sha, app.Status.Sync.Revision)
		}).
		When().
		// make sure new commit is not discovered after refresh is requested
		Refresh(RefreshTypeNormal).
		Then().
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		And(func(app *Application) {
			assert.NotEqual(t, sha, app.Status.Sync.Revision)
		})
}

func TestDisableManifestGeneration(t *testing.T) {
	Given(t).
		Path("guestbook").
		When().
		CreateApp().
		Refresh(RefreshTypeHard).
		Then().
		And(func(app *Application) {
			assert.Equal(t, ApplicationSourceTypeKustomize, app.Status.SourceType)
		}).
		When().
		And(func() {
			require.NoError(t, fixture.SetEnableManifestGeneration(map[ApplicationSourceType]bool{
				ApplicationSourceTypeKustomize: false,
			}))
		}).
		Refresh(RefreshTypeHard).
		Then().
		And(func(_ *Application) {
			// Wait for refresh to complete
			time.Sleep(1 * time.Second)
		}).
		And(func(app *Application) {
			assert.Equal(t, ApplicationSourceTypeDirectory, app.Status.SourceType)
		})
}

func TestSwitchTrackingMethod(t *testing.T) {
	ctx := Given(t)

	ctx.
		SetTrackingMethod(string(TrackingMethodAnnotation)).
		Path("deployment").
		When().
		CreateApp().
		Sync().
		Refresh(RefreshTypeNormal).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		And(func() {
			// Add resource with tracking annotation. This should put the
			// application OutOfSync.
			errors.NewHandler(t).FailOnErr(fixture.KubeClientset.CoreV1().ConfigMaps(fixture.DeploymentNamespace()).Create(t.Context(), &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "other-configmap",
					Annotations: map[string]string{
						common.AnnotationKeyAppInstance: fmt.Sprintf("%s:/ConfigMap:%s/other-configmap", fixture.Name(), fixture.DeploymentNamespace()),
					},
				},
			}, metav1.CreateOptions{}))
		}).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		And(func() {
			// Delete resource to bring application back in sync
			errors.NewHandler(t).FailOnErr(nil, fixture.KubeClientset.CoreV1().ConfigMaps(fixture.DeploymentNamespace()).Delete(t.Context(), "other-configmap", metav1.DeleteOptions{}))
		}).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		SetTrackingMethod(string(TrackingMethodLabel)).
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		And(func() {
			// Add a resource with a tracking annotation. This should not
			// affect the application, because we now use the tracking method
			// "label".
			errors.NewHandler(t).FailOnErr(fixture.KubeClientset.CoreV1().ConfigMaps(fixture.DeploymentNamespace()).Create(t.Context(), &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "other-configmap",
					Annotations: map[string]string{
						common.AnnotationKeyAppInstance: fmt.Sprintf("%s:/ConfigMap:%s/other-configmap", fixture.Name(), fixture.DeploymentNamespace()),
					},
				},
			}, metav1.CreateOptions{}))
		}).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		And(func() {
			// Add a resource with the tracking label. The app should become
			// OutOfSync.
			errors.NewHandler(t).FailOnErr(fixture.KubeClientset.CoreV1().ConfigMaps(fixture.DeploymentNamespace()).Create(t.Context(), &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "extra-configmap",
					Labels: map[string]string{
						common.LabelKeyAppInstance: fixture.Name(),
					},
				},
			}, metav1.CreateOptions{}))
		}).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		And(func() {
			// Delete resource to bring application back in sync
			errors.NewHandler(t).FailOnErr(nil, fixture.KubeClientset.CoreV1().ConfigMaps(fixture.DeploymentNamespace()).Delete(t.Context(), "extra-configmap", metav1.DeleteOptions{}))
		}).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy))
}

func TestSwitchTrackingLabel(t *testing.T) {
	ctx := Given(t)

	require.NoError(t, fixture.SetTrackingMethod(string(TrackingMethodLabel)))
	ctx.
		Path("deployment").
		When().
		CreateApp().
		Sync().
		Refresh(RefreshTypeNormal).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		And(func() {
			// Add extra resource that carries the default tracking label
			// We expect the app to go out of sync.
			errors.NewHandler(t).FailOnErr(fixture.KubeClientset.CoreV1().ConfigMaps(fixture.DeploymentNamespace()).Create(t.Context(), &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "other-configmap",
					Labels: map[string]string{
						common.LabelKeyAppInstance: fixture.Name(),
					},
				},
			}, metav1.CreateOptions{}))
		}).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		And(func() {
			// Delete resource to bring application back in sync
			errors.NewHandler(t).FailOnErr(nil, fixture.KubeClientset.CoreV1().ConfigMaps(fixture.DeploymentNamespace()).Delete(t.Context(), "other-configmap", metav1.DeleteOptions{}))
		}).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		// Change tracking label
		SetTrackingLabel("argocd.tracking").
		Sync().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		And(func() {
			// Create resource with the new tracking label, the application
			// is expected to go out of sync
			errors.NewHandler(t).FailOnErr(fixture.KubeClientset.CoreV1().ConfigMaps(fixture.DeploymentNamespace()).Create(t.Context(), &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "other-configmap",
					Labels: map[string]string{
						"argocd.tracking": fixture.Name(),
					},
				},
			}, metav1.CreateOptions{}))
		}).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		And(func() {
			// Delete resource to bring application back in sync
			errors.NewHandler(t).FailOnErr(nil, fixture.KubeClientset.CoreV1().ConfigMaps(fixture.DeploymentNamespace()).Delete(t.Context(), "other-configmap", metav1.DeleteOptions{}))
		}).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		And(func() {
			// Add extra resource that carries the default tracking label
			// We expect the app to stay in sync, because the configured
			// label is different.
			errors.NewHandler(t).FailOnErr(fixture.KubeClientset.CoreV1().ConfigMaps(fixture.DeploymentNamespace()).Create(t.Context(), &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "other-configmap",
					Labels: map[string]string{
						common.LabelKeyAppInstance: fixture.Name(),
					},
				},
			}, metav1.CreateOptions{}))
		}).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy))
}

func TestAnnotationTrackingExtraResources(t *testing.T) {
	ctx := Given(t)

	require.NoError(t, fixture.SetTrackingMethod(string(TrackingMethodAnnotation)))
	ctx.
		Path("deployment").
		When().
		CreateApp().
		Sync().
		Refresh(RefreshTypeNormal).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		And(func() {
			// Add a resource with an annotation that is not referencing the
			// resource.
			errors.NewHandler(t).FailOnErr(fixture.KubeClientset.CoreV1().ConfigMaps(fixture.DeploymentNamespace()).Create(t.Context(), &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "extra-configmap",
					Annotations: map[string]string{
						common.AnnotationKeyAppInstance: fmt.Sprintf("%s:apps/Deployment:%s/guestbook-cm", fixture.Name(), fixture.DeploymentNamespace()),
					},
				},
			}, metav1.CreateOptions{}))
		}).
		Refresh(RefreshTypeNormal).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		Sync("--prune").
		And(func() {
			// The extra configmap must not be pruned, because it's not tracked
			cm, err := fixture.KubeClientset.CoreV1().ConfigMaps(fixture.DeploymentNamespace()).Get(t.Context(), "extra-configmap", metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, "extra-configmap", cm.Name)
		}).
		And(func() {
			// Add a resource with an annotation that is self-referencing the
			// resource.
			errors.NewHandler(t).FailOnErr(fixture.KubeClientset.CoreV1().ConfigMaps(fixture.DeploymentNamespace()).Create(t.Context(), &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "other-configmap",
					Annotations: map[string]string{
						common.AnnotationKeyAppInstance: fmt.Sprintf("%s:/ConfigMap:%s/other-configmap", fixture.Name(), fixture.DeploymentNamespace()),
					},
				},
			}, metav1.CreateOptions{}))
		}).
		Refresh(RefreshTypeNormal).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		Sync("--prune").
		And(func() {
			// The extra configmap must be pruned now, because it's tracked
			cm, err := fixture.KubeClientset.CoreV1().ConfigMaps(fixture.DeploymentNamespace()).Get(t.Context(), "other-configmap", metav1.GetOptions{})
			require.Error(t, err)
			require.Empty(t, cm.Name)
		}).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		And(func() {
			// Add a cluster-scoped resource that is not referencing itself
			errors.NewHandler(t).FailOnErr(fixture.KubeClientset.RbacV1().ClusterRoles().Create(t.Context(), &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name: "e2e-test-clusterrole",
					Annotations: map[string]string{
						common.AnnotationKeyAppInstance: fmt.Sprintf("%s:rbac.authorization.k8s.io/ClusterRole:%s/e2e-other-clusterrole", fixture.Name(), fixture.DeploymentNamespace()),
					},
					Labels: map[string]string{
						fixture.TestingLabel: "true",
					},
				},
			}, metav1.CreateOptions{}))
		}).
		Refresh(RefreshTypeNormal).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		And(func() {
			// Add a cluster-scoped resource that is referencing itself
			errors.NewHandler(t).FailOnErr(fixture.KubeClientset.RbacV1().ClusterRoles().Create(t.Context(), &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name: "e2e-other-clusterrole",
					Annotations: map[string]string{
						common.AnnotationKeyAppInstance: fmt.Sprintf("%s:rbac.authorization.k8s.io/ClusterRole:%s/e2e-other-clusterrole", fixture.Name(), fixture.DeploymentNamespace()),
					},
					Labels: map[string]string{
						fixture.TestingLabel: "true",
					},
				},
			}, metav1.CreateOptions{}))
		}).
		Refresh(RefreshTypeNormal).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		When().
		Sync("--prune").
		And(func() {
			// The extra configmap must be pruned now, because it's tracked and does not exist in git
			cr, err := fixture.KubeClientset.RbacV1().ClusterRoles().Get(t.Context(), "e2e-other-clusterrole", metav1.GetOptions{})
			require.Error(t, err)
			require.Empty(t, cr.Name)
		}).
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy))
}

func TestCreateConfigMapsAndWaitForUpdate(t *testing.T) {
	Given(t).
		Path("config-map").
		When().
		CreateApp().
		Sync().
		Then().
		And(func(app *Application) {
			_, err := fixture.RunCli("app", "set", app.Name, "--sync-policy", "automated")
			require.NoError(t, err)
		}).
		When().
		AddFile("other-configmap.yaml", `
apiVersion: v1
kind: ConfigMap
metadata:
  name: other-map
  annotations:
    argocd.argoproj.io/sync-wave: "1"
data:
  foo2: bar2`).
		AddFile("yet-another-configmap.yaml", `
apiVersion: v1
kind: ConfigMap
metadata:
  name: yet-another-map
  annotations:
    argocd.argoproj.io/sync-wave: "2"
data:
  foo3: bar3`).
		PatchFile("kustomization.yaml", `[{"op": "add", "path": "/resources/-", "value": "other-configmap.yaml"}, {"op": "add", "path": "/resources/-", "value": "yet-another-configmap.yaml"}]`).
		Refresh(RefreshTypeNormal).
		Wait().
		Then().
		Expect(OperationPhaseIs(OperationSucceeded)).
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		Expect(HealthIs(health.HealthStatusHealthy)).
		Expect(ResourceHealthWithNamespaceIs("ConfigMap", "other-map", fixture.DeploymentNamespace(), health.HealthStatusHealthy)).
		Expect(ResourceSyncStatusWithNamespaceIs("ConfigMap", "other-map", fixture.DeploymentNamespace(), SyncStatusCodeSynced)).
		Expect(ResourceHealthWithNamespaceIs("ConfigMap", "yet-another-map", fixture.DeploymentNamespace(), health.HealthStatusHealthy)).
		Expect(ResourceSyncStatusWithNamespaceIs("ConfigMap", "yet-another-map", fixture.DeploymentNamespace(), SyncStatusCodeSynced))
}

func TestInstallationID(t *testing.T) {
	ctx := Given(t)
	ctx.
		SetTrackingMethod(string(TrackingMethodAnnotation)).
		And(func() {
			_, err := fixture.KubeClientset.CoreV1().ConfigMaps(fixture.DeploymentNamespace()).Create(
				t.Context(), &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-configmap",
						Annotations: map[string]string{
							common.AnnotationKeyAppInstance: fmt.Sprintf("%s:/ConfigMap:%s/test-configmap", ctx.AppName(), fixture.DeploymentNamespace()),
						},
					},
				}, metav1.CreateOptions{})
			require.NoError(t, err)
		}).
		Path(guestbookPath).
		Prune(false).
		When().IgnoreErrors().CreateApp().Sync().
		Then().Expect(OperationPhaseIs(OperationSucceeded)).Expect(SyncStatusIs(SyncStatusCodeOutOfSync)).
		And(func(app *Application) {
			var cm *ResourceStatus
			for i := range app.Status.Resources {
				if app.Status.Resources[i].Kind == "ConfigMap" && app.Status.Resources[i].Name == "test-configmap" {
					cm = &app.Status.Resources[i]
					break
				}
			}
			require.NotNil(t, cm)
			assert.Equal(t, SyncStatusCodeOutOfSync, cm.Status)
		}).
		When().SetInstallationID("test").Sync().
		Then().
		Expect(SyncStatusIs(SyncStatusCodeSynced)).
		And(func(app *Application) {
			require.Len(t, app.Status.Resources, 2)
			svc, err := fixture.KubeClientset.CoreV1().Services(fixture.DeploymentNamespace()).Get(t.Context(), "guestbook-ui", metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, "test", svc.Annotations[common.AnnotationInstallationID])

			deploy, err := fixture.KubeClientset.AppsV1().Deployments(fixture.DeploymentNamespace()).Get(t.Context(), "guestbook-ui", metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, "test", deploy.Annotations[common.AnnotationInstallationID])
		})
}

func TestDeletionConfirmation(t *testing.T) {
	ctx := Given(t)
	ctx.
		And(func() {
			_, err := fixture.KubeClientset.CoreV1().ConfigMaps(fixture.DeploymentNamespace()).Create(
				t.Context(), &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-configmap",
						Labels: map[string]string{
							common.LabelKeyAppInstance: ctx.AppName(),
						},
						Annotations: map[string]string{
							AnnotationSyncOptions: "Prune=confirm",
						},
					},
				}, metav1.CreateOptions{})
			require.NoError(t, err)
		}).
		Path(guestbookPath).
		Async(true).
		When().
		PatchFile("guestbook-ui-deployment.yaml", `[{ "op": "add", "path": "/metadata/annotations", "value": { "argocd.argoproj.io/sync-options": "Delete=confirm" }}]`).
		CreateApp().Sync().
		Then().Expect(OperationPhaseIs(OperationRunning)).
		When().ConfirmDeletion().
		Then().Expect(OperationPhaseIs(OperationSucceeded)).
		When().Delete(true).
		Then().
		And(func(app *Application) {
			assert.NotNil(t, app.DeletionTimestamp)
		}).
		When().ConfirmDeletion().
		Then().Expect(DoesNotExist())
}

func TestLastTransitionTimeUnchangedError(t *testing.T) {
	// Ensure that, if the health status hasn't changed, the lastTransitionTime is not updated.

	ctx := Given(t)
	ctx.
		Path(guestbookPath).
		When().
		And(func() {
			// Manually create an application with an outdated reconciledAt field
			manifest := fmt.Sprintf(`
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: %s
spec:
  project: default
  source:
    repoURL: %s
    path: guestbook
    targetRevision: HEAD
  destination:
    server: https://non-existent-cluster
    namespace: default
status:
  reconciledAt: "2023-01-01T00:00:00Z"
  health:
    status: Unknown
    lastTransitionTime: "2023-01-01T00:00:00Z"
`, ctx.AppName(), fixture.RepoURL(fixture.RepoURLTypeFile))
			_, err := fixture.RunWithStdin(manifest, "", "kubectl", "apply", "-n", fixture.ArgoCDNamespace, "-f", "-")
			require.NoError(t, err)
		}).
		Refresh(RefreshTypeNormal).
		Then().
		And(func(app *Application) {
			// Verify the health status is still Unknown
			assert.Equal(t, health.HealthStatusUnknown, app.Status.Health.Status)

			// Verify the lastTransitionTime has not been updated
			assert.Equal(t, "2023-01-01T00:00:00Z", app.Status.Health.LastTransitionTime.UTC().Format(time.RFC3339))
		})
}
