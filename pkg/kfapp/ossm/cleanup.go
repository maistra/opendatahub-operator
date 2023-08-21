package ossm

import (
	"github.com/hashicorp/go-multierror"
)

func (o *OssmInstaller) CleanupResources() error {
	var cleanupErrors *multierror.Error
	for _, feature := range o.features {
		cleanupErrors = multierror.Append(cleanupErrors, feature.Cleanup())
	}

	return cleanupErrors.ErrorOrNil()
}
