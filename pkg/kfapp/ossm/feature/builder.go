package feature

import (
	"github.com/opendatahub-io/opendatahub-operator/pkg/kfconfig/ossmplugin"
	"github.com/pkg/errors"
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

func (fb *featureBuilder) For(spec *ossmplugin.OssmPluginSpec) *featureBuilder {
	fb.builders = append(fb.builders, func(f *Feature) error {
		f.Spec = spec

		return nil
	})

	return fb
}

func (fb *featureBuilder) WithConfig(config *rest.Config) *featureBuilder {
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

		return nil
	})

	return fb
}

func (fb *featureBuilder) FromPaths(paths ...string) *featureBuilder {
	fb.builders = append(fb.builders, func(f *Feature) error {
		var err error
		var manifests []manifest

		for _, path := range paths {
			manifests, err = LoadManifestsFrom(path)
			if err != nil {
				return errors.WithStack(err)
			}
		}

		f.manifests = manifests

		return nil
	})

	return fb
}

func (fb *featureBuilder) WithData(loader dataLoader) *featureBuilder {
	fb.builders = append(fb.builders, func(f *Feature) error {
		f.loader = loader

		return nil
	})

	return fb
}

func (fb *featureBuilder) Preconditions(preconditions ...precondition) *featureBuilder {
	fb.builders = append(fb.builders, func(f *Feature) error {
		f.preconditions = preconditions

		return nil
	})

	return fb
}

func (fb *featureBuilder) OnDelete(cleanups ...cleanup) *featureBuilder {
	fb.builders = append(fb.builders, func(f *Feature) error {
		f.addCleanup(cleanups...)

		return nil
	})

	return fb
}

func (fb *featureBuilder) AdditionalResources(resources ...resourceCreator) *featureBuilder {
	fb.builders = append(fb.builders, func(f *Feature) error {
		f.resources = resources

		return nil
	})

	return fb
}

func (fb *featureBuilder) Load() (*Feature, error) {
	feature := &Feature{
		Name:   fb.name,
		loader: noopDataLoader,
	}

	for i := range fb.builders {
		if err := fb.builders[i](feature); err != nil {
			return nil, err
		}
	}

	if err := feature.createResourceTracker(); err != nil {
		return nil, err
	}

	return feature, nil
}
