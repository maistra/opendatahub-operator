package ossm_test

import (
	"context"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/opendatahub-operator/pkg/kfapp/ossm"
	"github.com/opendatahub-io/opendatahub-operator/pkg/kfapp/ossm/feature"
	"github.com/opendatahub-io/opendatahub-operator/pkg/kfconfig"
	"github.com/opendatahub-io/opendatahub-operator/pkg/kfconfig/ossmplugin"
	"github.com/opendatahub-io/opendatahub-operator/tests/integration/testenv"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"path"
	"time"
)

const (
	timeout  = 5 * time.Second
	interval = 250 * time.Millisecond
)

var _ = Describe("CRD presence verification", func() {
	var (
		ossmInstaller       *ossm.OssmInstaller
		ossmPluginSpec      *ossmplugin.OssmPluginSpec
		verificationFeature *feature.Feature
	)

	BeforeEach(func() {
		ossmInstaller = newOssmInstaller("default")
		var err error
		ossmPluginSpec, err = ossmInstaller.GetPluginSpec()
		Expect(err).ToNot(HaveOccurred())
	})

	It("should successfully check existing CRD", func() {
		// given example CRD installed into env from /ossm/test/crd/
		crdGroup := "ossm.plugins.kubeflow.org"
		crdVersion := "test-version"
		crdResource := "test-resources"

		var err error
		verificationFeature, err = feature.CreateFeature("CRD verification").
			For(ossmPluginSpec).
			UsingConfig(envTest.Config).
			Preconditions(feature.EnsureCRDIsInstalled(crdGroup, crdVersion, crdResource)).
			Load()
		Expect(err).ToNot(HaveOccurred())

		// when
		err = verificationFeature.Apply()

		// then
		Expect(err).ToNot(HaveOccurred())
	})

	It("should fail to check non-existing CRD", func() {
		// given
		crdGroup := "non-existing-group"
		crdVersion := "non-existing-version"
		crdResource := "non-existing-resource"

		var err error
		verificationFeature, err = feature.CreateFeature("CRD verification").
			For(ossmPluginSpec).
			UsingConfig(envTest.Config).
			Preconditions(feature.EnsureCRDIsInstalled(crdGroup, crdVersion, crdResource)).
			Load()
		Expect(err).ToNot(HaveOccurred())

		// when
		err = verificationFeature.Apply()

		// then
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("server could not find the requested resource"))
	})
})

var _ = Describe("Ensuring service mesh is set up correctly", func() {

	var (
		objectCleaner    *testenv.Cleaner
		ossmInstaller    *ossm.OssmInstaller
		ossmPluginSpec   *ossmplugin.OssmPluginSpec
		serviceMeshCheck *feature.Feature
		name             = "test-name"
		namespace        = "test-namespace"
	)

	BeforeEach(func() {
		ossmInstaller = newOssmInstaller(namespace)
		var err error
		ossmPluginSpec, err = ossmInstaller.GetPluginSpec()
		Expect(err).ToNot(HaveOccurred())

		ossmPluginSpec.Mesh.Name = name
		ossmPluginSpec.Mesh.Namespace = namespace

		serviceMeshCheck, err = feature.CreateFeature("datascience-project-migration").
			For(ossmPluginSpec).
			UsingConfig(envTest.Config).
			Preconditions(feature.EnsureServiceMeshInstalled).Load()

		Expect(err).ToNot(HaveOccurred())

		objectCleaner = testenv.CreateCleaner(envTestClient, envTest.Config, timeout, interval)
	})

	It("should find installed SMCP", func() {
		ns := createNamespace(namespace)
		Expect(envTestClient.Create(context.Background(), ns)).To(Succeed())
		defer objectCleaner.DeleteAll(ns)

		createServiceMeshControlPlane(name, namespace)

		// when
		err := serviceMeshCheck.Apply()

		// then
		Expect(err).ToNot(HaveOccurred())
	})

	It("should fail to find SMCP if not present", func() {
		Expect(serviceMeshCheck.Apply()).To(HaveOccurred())
	})

})

var _ = Describe("Data Science Project Migration", func() {

	var (
		objectCleaner    *testenv.Cleaner
		ossmInstaller    *ossm.OssmInstaller
		ossmPluginSpec   *ossmplugin.OssmPluginSpec
		migrationFeature *feature.Feature
	)

	BeforeEach(func() {
		objectCleaner = testenv.CreateCleaner(envTestClient, envTest.Config, timeout, interval)

		ossmInstaller = newOssmInstaller("default")

		var err error
		ossmPluginSpec, err = ossmInstaller.GetPluginSpec()
		Expect(err).ToNot(HaveOccurred())

		migrationFeature, err = feature.CreateFeature("datascience-project-migration").
			For(ossmPluginSpec).
			UsingConfig(envTest.Config).
			WithResources(feature.MigratedDataScienceProjects).Load()

		Expect(err).ToNot(HaveOccurred())

	})

	It("should migrate single namespace", func() {
		// given
		dataScienceNs := createDataScienceProject("dsp-01")
		regularNs := createNamespace("non-dsp")
		Expect(envTestClient.Create(context.Background(), dataScienceNs)).To(Succeed())
		Expect(envTestClient.Create(context.Background(), regularNs)).To(Succeed())
		defer objectCleaner.DeleteAll(dataScienceNs, regularNs)

		// when
		Expect(migrationFeature.Apply()).ToNot(HaveOccurred())

		// then
		Eventually(findMigratedNamespaces, timeout, interval).Should(
			And(
				HaveLen(1),
				ContainElement("dsp-01"),
			),
		)
	})

	It("should not migrate any non-datascience namespace", func() {
		// given
		regularNs := createNamespace("non-dsp")
		Expect(envTestClient.Create(context.Background(), regularNs)).To(Succeed())
		defer objectCleaner.DeleteAll(regularNs)

		// when
		Expect(migrationFeature.Apply()).ToNot(HaveOccurred())

		// then
		Consistently(findMigratedNamespaces, timeout, interval).Should(BeEmpty()) // we can't wait forever, but this should be good enough
	})

	It("should migrate multiple namespaces", func() {
		// given
		dataScienceNs01 := createDataScienceProject("dsp-01")
		dataScienceNs02 := createDataScienceProject("dsp-02")
		dataScienceNs03 := createDataScienceProject("dsp-03")
		regularNs := createNamespace("non-dsp")
		Expect(envTestClient.Create(context.Background(), dataScienceNs01)).To(Succeed())
		Expect(envTestClient.Create(context.Background(), dataScienceNs02)).To(Succeed())
		Expect(envTestClient.Create(context.Background(), dataScienceNs03)).To(Succeed())
		Expect(envTestClient.Create(context.Background(), regularNs)).To(Succeed())
		defer objectCleaner.DeleteAll(dataScienceNs01, dataScienceNs02, dataScienceNs03, regularNs)

		// when
		Expect(migrationFeature.Apply()).ToNot(HaveOccurred())

		// then
		Eventually(findMigratedNamespaces, timeout, interval).Should(
			And(
				HaveLen(3),
				ContainElements("dsp-01", "dsp-02", "dsp-03"),
			),
		)
	})

})

var _ = Describe("Cleanup operations", func() {

	Context("setting oauth for control plane", func() {

		var (
			objectCleaner  *testenv.Cleaner
			ossmInstaller  *ossm.OssmInstaller
			ossmPluginSpec *ossmplugin.OssmPluginSpec
			namespace      = "test"
			name           = "minimal"
			projectDir     string
		)

		BeforeEach(func() {
			objectCleaner = testenv.CreateCleaner(envTestClient, envTest.Config, timeout, interval)

			ossmInstaller = newOssmInstaller(namespace)

			var err error
			ossmPluginSpec, err = ossmInstaller.GetPluginSpec()
			Expect(err).ToNot(HaveOccurred())

			ossmPluginSpec.Mesh.Name = name
			ossmPluginSpec.Mesh.Namespace = namespace

			projectDir, err = findProjectRoot()
			Expect(err).ToNot(HaveOccurred())

		})

		It("should remove mounted secret volumes", func() {
			// given
			ns := createNamespace(namespace)
			Expect(envTestClient.Create(context.Background(), ns)).To(Succeed())
			defer objectCleaner.DeleteAll(ns)

			createServiceMeshControlPlane(name, namespace)

			controlPlaneWithSecretVolumes, err := feature.CreateFeature("control-plane-with-secret-volumes").
				For(ossmPluginSpec).
				Manifests(path.Join(projectDir, "pkg/kfapp/ossm/templates/control-plane/base/control-plane-ingress.patch.tmpl")).
				UsingConfig(envTest.Config).
				Load()

			Expect(err).ToNot(HaveOccurred())

			// when
			Expect(controlPlaneWithSecretVolumes.Apply()).ToNot(HaveOccurred())
			// Testing removal function on its own relying on feature setup
			err = feature.RemoveTokenVolumes(controlPlaneWithSecretVolumes)

			// then
			serviceMeshControlPlane, err := getServiceMeshControlPlane(envTest.Config, namespace, name)
			Expect(err).ToNot(HaveOccurred())

			volumes, found, err := unstructured.NestedSlice(serviceMeshControlPlane.Object, "spec", "gateways", "ingress", "volumes")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(volumes).To(BeEmpty())
		})

	})

})

func createServiceMeshControlPlane(name, namespace string) {
	serviceMeshControlPlane := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "maistra.io/v2",
			"kind":       "ServiceMeshControlPlane",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{},
		},
	}
	createErr := createSMCPInCluster(envTest.Config, serviceMeshControlPlane, namespace)
	Expect(createErr).ToNot(HaveOccurred())
}

func createDataScienceProject(name string) *v1.Namespace {
	namespace := createNamespace(name)
	namespace.Labels = map[string]string{
		"opendatahub.io/dashboard": "true",
	}
	return namespace
}

func createNamespace(name string) *v1.Namespace {
	return &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func findMigratedNamespaces() []string {
	namespaces := &v1.NamespaceList{}
	var ns []string
	if err := envTestClient.List(context.Background(), namespaces); err != nil && !errors.IsNotFound(err) {
		Fail(err.Error())
	}
	for _, namespace := range namespaces.Items {
		if _, ok := namespace.ObjectMeta.Annotations["opendatahub.io/service-mesh"]; ok {
			ns = append(ns, namespace.Name)
		}
	}
	return ns
}

func newOssmInstaller(ns string) *ossm.OssmInstaller {
	config := kfconfig.KfConfig{}
	config.SetNamespace(ns)
	config.Spec.Plugins = append(config.Spec.Plugins, kfconfig.Plugin{
		Name: "KfOssmPlugin",
		Kind: "KfOssmPlugin",
	})
	return ossm.NewOssmInstaller(&config, envTest.Config)
}

// createSMCPInCluster uses dynamic client to create a dummy SMCP resource for testing
func createSMCPInCluster(cfg *rest.Config, smcpObj *unstructured.Unstructured, namespace string) error {
	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "maistra.io",
		Version:  "v2",
		Resource: "servicemeshcontrolplanes",
	}

	result, err := dynamicClient.Resource(gvr).Namespace(namespace).Create(context.TODO(), smcpObj, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	statusConditions := []interface{}{
		map[string]interface{}{
			"type":   "Ready",
			"status": "True",
		},
	}

	// Since we don't have actual service mesh operator deployed, we simulate the status
	status := map[string]interface{}{
		"conditions": statusConditions,
		"readiness": map[string]interface{}{
			"components": map[string]interface{}{
				"pending": []interface{}{},
				"ready":   []interface{}{},
				"unready": []interface{}{},
			},
		},
	}

	if err := unstructured.SetNestedField(result.Object, status, "status"); err != nil {
		return err
	}

	_, err = dynamicClient.Resource(gvr).Namespace(namespace).UpdateStatus(context.TODO(), result, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	return nil
}

func getServiceMeshControlPlane(cfg *rest.Config, namespace, name string) (*unstructured.Unstructured, error) {
	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	gvr := schema.GroupVersionResource{
		Group:    "maistra.io",
		Version:  "v2",
		Resource: "servicemeshcontrolplanes",
	}

	smcp, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return smcp, nil
}
