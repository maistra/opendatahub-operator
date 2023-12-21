package features_test

import (
	"context"
	"fmt"
	"time"

	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dscv1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/dscinitialization/v1"
	featurev1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/features/v1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature"
	"github.com/opendatahub-io/opendatahub-operator/v2/tests/envtestutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	timeout  = 5 * time.Second
	interval = 250 * time.Millisecond
)

var _ = Describe("preconditions", func() {

	Context("namespace existence", func() {

		var (
			objectCleaner *envtestutil.Cleaner
			testFeature   *feature.Feature
			namespace     string
		)

		BeforeEach(func() {
			objectCleaner = envtestutil.CreateCleaner(envTestClient, envTest.Config, timeout, interval)

			testFeatureName := "test-ns-creation"
			namespace = envtestutil.AppendRandomNameTo(testFeatureName)

			dsciSpec := newDSCInitializationSpec(namespace)
			var err error
			testFeature, err = feature.CreateFeature(testFeatureName).
				For(dsciSpec).
				UsingConfig(envTest.Config).
				Load()
			Expect(err).ToNot(HaveOccurred())
		})

		It("should create namespace if it does not exist", func() {
			// given
			_, err := getNamespace(namespace)
			Expect(errors.IsNotFound(err)).To(BeTrue())
			defer objectCleaner.DeleteAll(&v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})

			// when
			err = feature.CreateNamespaceIfNotExists(namespace)(testFeature)

			// then
			Expect(err).ToNot(HaveOccurred())
		})

		It("should not try to create namespace if it does already exist", func() {
			// given
			ns := createNamespace(namespace)
			Expect(envTestClient.Create(context.Background(), ns)).To(Succeed())
			defer objectCleaner.DeleteAll(ns)

			// when
			err := feature.CreateNamespaceIfNotExists(namespace)(testFeature)

			// then
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("ensuring custom resource definitions are installed", func() {

		var (
			dsciSpec            *dscv1.DSCInitializationSpec
			verificationFeature *feature.Feature
		)

		BeforeEach(func() {
			dsciSpec = newDSCInitializationSpec("default")
		})

		It("should successfully check for existing CRD", func() {
			// given example CRD installed into env
			name := "test-resources.openshift.io"

			var err error
			verificationFeature, err = feature.CreateFeature("CRD verification").
				For(dsciSpec).
				UsingConfig(envTest.Config).
				PreConditions(feature.EnsureCRDIsInstalled(name)).
				Load()
			Expect(err).ToNot(HaveOccurred())

			// when
			err = verificationFeature.Apply()

			// then
			Expect(err).ToNot(HaveOccurred())
		})

		It("should fail to check non-existing CRD", func() {
			// given
			name := "non-existing-resource.non-existing-group.io"

			var err error
			verificationFeature, err = feature.CreateFeature("CRD verification").
				For(dsciSpec).
				UsingConfig(envTest.Config).
				PreConditions(feature.EnsureCRDIsInstalled(name)).
				Load()
			Expect(err).ToNot(HaveOccurred())

			// when
			err = verificationFeature.Apply()

			// then
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("\"non-existing-resource.non-existing-group.io\" not found"))
		})
	})

})

var _ = Describe("feature trackers", func() {
	Context("ensuring feature trackers indicate status and phase", func() {

		var (
			dsciSpec            *dscv1.DSCInitializationSpec
			verificationFeature *feature.Feature
		)

		BeforeEach(func() {
			dsciSpec = newDSCInitializationSpec("default")
		})

		It("should indicate successful installation in FeatureTracker", func() {
			// given example CRD installed into env
			name := "test-resources.openshift.io"

			var err error
			verificationFeature, err = feature.CreateFeature("crd-verification").
				For(dsciSpec).
				UsingConfig(envTest.Config).
				PreConditions(feature.EnsureCRDIsInstalled(name)).
				Load()
			Expect(err).ToNot(HaveOccurred())

			// when
			err = verificationFeature.Apply()

			// then
			Expect(err).ToNot(HaveOccurred())

			featureTracker, err := getFeatureTracker("default-crd-verification")
			Expect(err).ToNot(HaveOccurred())
			Expect(*featureTracker.Status.Conditions).ToNot(BeEmpty())

			availableCondition := conditionsv1.FindStatusCondition(*featureTracker.Status.Conditions, conditionsv1.ConditionAvailable)
			Expect(availableCondition).ToNot(BeNil())
			Expect(availableCondition.Status).To(Equal(v1.ConditionTrue))
			Expect(availableCondition.Reason).To(Equal(featurev1.ConditionPhaseFeatureCreated))
		})

		It("should indicate failure in preconditions", func() {
			// given
			name := "non-existing-resource.non-existing-group.io"

			var err error
			verificationFeature, err = feature.CreateFeature("crd-verification").
				For(dsciSpec).
				UsingConfig(envTest.Config).
				PreConditions(feature.EnsureCRDIsInstalled(name)).
				Load()
			Expect(err).ToNot(HaveOccurred())

			// when
			err = verificationFeature.Apply()

			// then
			Expect(err).To(HaveOccurred())

			featureTracker, err := getFeatureTracker("default-crd-verification")
			Expect(err).ToNot(HaveOccurred())
			Expect(*featureTracker.Status.Conditions).ToNot(BeEmpty())

			degradedCondition := conditionsv1.FindStatusCondition(*featureTracker.Status.Conditions, conditionsv1.ConditionDegraded)
			Expect(degradedCondition).ToNot(BeNil())
			Expect(degradedCondition.Status).To(Equal(v1.ConditionTrue))
			Expect(degradedCondition.Reason).To(Equal(featurev1.ConditionPhasePreCondition))
		})

		It("should indicate failure in post-conditions", func() {
			var err error
			verificationFeature, err = feature.CreateFeature("post-condition-failure").
				For(dsciSpec).
				UsingConfig(envTest.Config).
				PostConditions(FailPostCondition()).
				Load()
			Expect(err).ToNot(HaveOccurred())

			// when
			err = verificationFeature.Apply()

			// then
			Expect(err).To(HaveOccurred())

			featureTracker, err := getFeatureTracker("default-post-condition-failure")
			Expect(err).ToNot(HaveOccurred())
			Expect(*featureTracker.Status.Conditions).ToNot(BeEmpty())

			degradedCondition := conditionsv1.FindStatusCondition(*featureTracker.Status.Conditions, conditionsv1.ConditionDegraded)
			Expect(degradedCondition).ToNot(BeNil())
			Expect(degradedCondition.Status).To(Equal(v1.ConditionTrue))
			Expect(degradedCondition.Reason).To(Equal(featurev1.ConditionPhasePostCondition))
		})
	})
})

func createNamespace(name string) *v1.Namespace {
	return &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func newDSCInitializationSpec(ns string) *dscv1.DSCInitializationSpec {
	spec := dscv1.DSCInitializationSpec{}
	spec.ApplicationsNamespace = ns

	return &spec
}

func getNamespace(namespace string) (*v1.Namespace, error) {
	ns := createNamespace(namespace)
	err := envTestClient.Get(context.Background(), types.NamespacedName{Name: namespace}, ns)

	return ns, err
}

func getFeatureTracker(name string) (*featurev1.FeatureTracker, error) {
	tracker := &featurev1.FeatureTracker{}
	err := envTestClient.Get(context.Background(), client.ObjectKey{
		Name: name,
	}, tracker)

	return tracker, err
}

func FailPostCondition() feature.Action {
	return func(f *feature.Feature) error {
		return fmt.Errorf("dummy error")
	}
}
