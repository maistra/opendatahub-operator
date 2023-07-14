package ossm

import kftypesv3 "github.com/opendatahub-io/opendatahub-operator/apis/apps"

// Below are the functions which are not used/executed at this point.
// They're here to satisfy the Plugin interface.

func (o *OssmInstaller) Apply(resources kftypesv3.ResourceEnum) error {
	// Plugins invoked within k8s (as a platform) won't be participating in Apply
	// This is responsibility of PackageManagers - in this case kustomize
	return nil
}

func (o *OssmInstaller) Delete(resources kftypesv3.ResourceEnum) error {
	// Plugins invoked within k8s (as a platform) won't be participating in Delete
	// This is responsibility of PackageManagers - in this case kustomize
	return nil
}

func (o *OssmInstaller) Dump(resources kftypesv3.ResourceEnum) error {
	// Plugins invoked within k8s (as a platform) won't be participating in Dump
	// This is responsibility of PackageManagers - in this case kustomize
	return nil
}
