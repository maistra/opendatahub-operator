package feature

import (
	v1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/dscinitialization/v1"
	"github.com/pkg/errors"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type partialBuilder func(f *Feature) error

type featureBuilder struct {
	name     string
	builders []partialBuilder
}

func CreateFeature(name string) *featureBuilder {
	return &featureBuilder{name: name}
}

func (fb *featureBuilder) For(spec *v1.DSCInitializationSpec) *featureBuilder {
	createSpec := func(f *Feature) error {
		f.Spec = &Spec{
			AppNamespace:    spec.ApplicationsNamespace,
			ServiceMeshSpec: &spec.ServiceMesh,
		}

		return nil
	}

	// Ensures creation of .Spec object is always invoked first
	fb.builders = append([]partialBuilder{createSpec}, fb.builders...)

	return fb
}

func (fb *featureBuilder) UsingConfig(config *rest.Config) *featureBuilder {
	fb.builders = append(fb.builders, func(f *Feature) error {
		var err error
		f.clientset, err = kubernetes.NewForConfig(config)
		if err != nil {
			return err
		}

		f.dynamicClient, err = dynamic.NewForConfig(config)
		if err != nil {
			return err
		}

		f.client, err = client.New(config, client.Options{})
		if err != nil {
			return errors.WithStack(err)
		}

		if err := apiextv1.AddToScheme(f.client.Scheme()); err != nil {
			return err
		}

		return nil
	})

	return fb
}

func (fb *featureBuilder) Manifests(paths ...string) *featureBuilder {
	fb.builders = append(fb.builders, func(f *Feature) error {
		var err error
		var manifests []manifest

		for _, path := range paths {
			manifests, err = loadManifestsFrom(path)
			if err != nil {
				return errors.WithStack(err)
			}

			f.manifests = append(f.manifests, manifests...)
		}

		return nil
	})

	return fb
}

func (fb *featureBuilder) WithData(loader ...action) *featureBuilder {
	fb.builders = append(fb.builders, func(f *Feature) error {
		f.loaders = append(f.loaders, loader...)

		return nil
	})

	return fb
}

func (fb *featureBuilder) Preconditions(preconditions ...action) *featureBuilder {
	fb.builders = append(fb.builders, func(f *Feature) error {
		f.preconditions = append(f.preconditions, preconditions...)

		return nil
	})

	return fb
}

func (fb *featureBuilder) Postconditions(postconditions ...action) *featureBuilder {
	fb.builders = append(fb.builders, func(f *Feature) error {
		f.postconditions = append(f.postconditions, postconditions...)

		return nil
	})

	return fb
}

func (fb *featureBuilder) OnDelete(cleanups ...action) *featureBuilder {
	fb.builders = append(fb.builders, func(f *Feature) error {
		f.addCleanup(cleanups...)

		return nil
	})

	return fb
}

func (fb *featureBuilder) WithResources(resources ...action) *featureBuilder {
	fb.builders = append(fb.builders, func(f *Feature) error {
		f.resources = resources

		return nil
	})

	return fb
}

func (fb *featureBuilder) EnabledIf(enabled func(f *Feature) bool) *featureBuilder {
	fb.builders = append(fb.builders, func(f *Feature) error {
		f.Enabled = enabled(f)

		return nil

	})
	return fb
}

func (fb *featureBuilder) Load() (*Feature, error) {
	feature := &Feature{
		Name:    fb.name,
		Enabled: true,
	}

	for i := range fb.builders {
		if err := fb.builders[i](feature); err != nil {
			return nil, err
		}
	}

	if feature.Enabled {
		if err := feature.createResourceTracker(); err != nil {
			return feature, err
		}
	}

	return feature, nil
}
