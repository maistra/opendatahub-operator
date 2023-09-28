package v1

import (
	"fmt"
	operatorv1 "github.com/openshift/api/operator/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"strconv"
	"strings"
)

// ServiceMeshSpec configures Service Mesh.
type ServiceMeshSpec struct {
	// +kubebuilder:validation:Enum=Managed;Removed
	ManagementState operatorv1.ManagementState `json:"managementState,omitempty"`
	// Mesh holds configuration of Service Mesh used by Opendatahub.
	Mesh MeshSpec `json:"mesh,omitempty"`
	// Auth holds configuration of authentication and authorization services
	// used by Service Mesh in Opendatahub.
	Auth AuthSpec `json:"auth,omitempty"`
}

// TODO rework based on operator states?

// InstallationMode defines how the cluster initialization should handle OpenShift Service Mesh installation.
// If not specified `pre-installed` is assumed.
type InstallationMode string

var (
	// PreInstalled indicates that cluster initialization for Openshift Service Mesh will use existing
	// installation and patch Service Mesh Control Plane.
	PreInstalled InstallationMode = "pre-installed"

	// Minimal results in installing Openshift Service Mesh Control Plane
	// in defined namespace with minimal required configuration.
	Minimal InstallationMode = "minimal"
)

type MeshSpec struct {
	// Name is a name Service Mesh Control Plan. Defaults to "basic".
	Name string `json:"name,omitempty" default:"basic"`
	// Namespace is a namespace where Service Mesh is deployed. Defaults to "istio-system".
	Namespace string `json:"namespace,omitempty" default:"istio-system"`
	// InstallationMode defines how the cluster initialization should handle OpenShift Service Mesh installation.
	// If not specified `pre-installed` is assumed.
	// +kubebuilder:validation:Enum=minimal;pre-installed
	InstallationMode InstallationMode `json:"installationMode,omitempty" default:"pre-installed"`
	// Certificate allows to define how to use certificates for the Service Mesh communication.
	Certificate CertSpec `json:"certificate,omitempty"`
}

type CertSpec struct {
	// Name of the certificate to be used by Service Mesh.
	Name string `json:"name,omitempty" default:"opendatahub-dashboard-cert"`
	// Generate indicates if the certificate should be generated. If set to false
	// it will assume certificate with the given name is made available as a secret
	// in Service Mesh namespace.
	Generate bool `json:"generate,omitempty" default:"true"`
}

type AuthSpec struct {
	// Name of the authorization provider used for Service Mesh.
	Name string `json:"name,omitempty" default:"authorino"`
	// Namespace where it is deployed.
	Namespace string `json:"namespace,omitempty" default:"auth-provider"`
	// Authorino holds configuration of Authorino service used as external authorization provider.
	Authorino AuthorinoSpec `json:"authorino,omitempty"`
}

type AuthorinoSpec struct {
	// Name specifies how external authorization provider should be called.
	Name string `json:"name,omitempty" default:"authorino-mesh-authz-provider"`
	// Audiences is a list of the identifiers that the resource server presented
	// with the token identifies as. Audience-aware token authenticators will verify
	// that the token was intended for at least one of the audiences in this list.
	// If no audiences are provided, the audience will default to the audience of the
	// Kubernetes apiserver (kubernetes.default.svc).
	Audiences []string `json:"audiences,omitempty" default:"https://kubernetes.default.svc"`
	// Label narrows amount of AuthConfigs to process by Authorino service.
	Label string `json:"label,omitempty" default:"authorino/topic=odh"`
	// Image allows to define a custom container image to be used when deploying Authorino's instance.
	Image string `json:"image,omitempty" default:"quay.io/kuadrant/authorino:v0.13.0"`
}

// ServiceMeshResourceTracker is a cluster-scoped resource for tracking objects
// created by Service Mesh initialization for Data Science Platform.
// It's primarily used as owner reference for resources created across namespaces so that they can be
// garbage collected by Kubernetes when they're not needed anymore.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type ServiceMeshResourceTracker struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ServiceMeshResourceTrackerSpec   `json:"spec,omitempty"`
	Status ServiceMeshResourceTrackerStatus `json:"status,omitempty"`
}

func (s *ServiceMeshResourceTracker) ToOwnerReference() metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: s.APIVersion,
		Kind:       s.Kind,
		Name:       s.Name,
		UID:        s.UID,
	}
}

// ServiceMeshResourceTrackerSpec defines the desired state of ServiceMeshResourceTracker.
type ServiceMeshResourceTrackerSpec struct {
}

// ServiceMeshResourceTrackerStatus defines the observed state of ServiceMeshResourceTracker.
type ServiceMeshResourceTrackerStatus struct {
}

// +kubebuilder:object:root=true

// ServiceMeshResourceTrackerList contains a list of ServiceMeshResourceTracker.
type ServiceMeshResourceTrackerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceMeshResourceTracker `json:"items"`
}

// IsValid returns true if the spec is a valid and complete spec.
// If false it will also return a string providing a message about why its invalid.
func (s *ServiceMeshSpec) IsValid() (bool, string) {

	if s.Auth.Name != "authorino" {
		return false, "currently only Authorino is available as authorization layer"
	}

	return true, ""
}

func (s *ServiceMeshSpec) SetDefaults() error {
	return setDefaults(s)
}

func setDefaults(obj interface{}) error {
	value := reflect.ValueOf(obj).Elem()
	t := value.Type()

	for i := 0; i < t.NumField(); i++ {
		field := value.Field(i)
		tag := t.Field(i).Tag.Get("default")

		if field.Kind() == reflect.Struct {
			if err := setDefaults(field.Addr().Interface()); err != nil {
				return err
			}
		}

		if tag != "" && field.IsValid() && field.CanSet() && isEmptyValue(field) {
			defaultValue := reflect.ValueOf(tag)
			targetType := field.Type()
			if targetType.Kind() == reflect.Slice && defaultValue.Kind() == reflect.String {
				defaultSlice := strings.Split(defaultValue.String(), ",")
				convertedValue := reflect.MakeSlice(targetType, len(defaultSlice), len(defaultSlice))
				for i := 0; i < len(defaultSlice); i++ {
					convertedValue.Index(i).SetString(defaultSlice[i])
				}
				field.Set(convertedValue)
			} else if defaultValue.Type().ConvertibleTo(targetType) {
				convertedValue := defaultValue.Convert(targetType)
				field.Set(convertedValue)
			} else {
				switch targetType.Kind() {
				case reflect.Bool:
					b, err := strconv.ParseBool(tag)
					if err != nil {
						return fmt.Errorf("invalid boolean default value %s", tag)
					}
					defaultValue = reflect.ValueOf(b)
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
					n, err := strconv.ParseInt(tag, 10, 64)
					if err != nil {
						return fmt.Errorf("invalid integer default value %s", tag)
					}
					defaultValue = reflect.ValueOf(n).Convert(targetType)
				default:
					return fmt.Errorf("unable to convert \"%s\" to %s\n", defaultValue, targetType.Name())
				}

				field.Set(defaultValue)
			}
		}

	}

	return nil
}

func isEmptyValue(value reflect.Value) bool {
	zero := reflect.Zero(value.Type()).Interface()
	return reflect.DeepEqual(value.Interface(), zero)
}
