package v1

import (
	operatorv1 "github.com/openshift/api/operator/v1"
)

// ServiceMeshSpec configures Service Mesh.
type ServiceMeshSpec struct {
	// +kubebuilder:validation:Enum=Managed;Removed
	// +kubebuilder:default=Removed
	ManagementState operatorv1.ManagementState `json:"managementState,omitempty"`
	// Mesh holds configuration of Service Mesh used by Opendatahub.
	Mesh MeshSpec `json:"mesh,omitempty"`
	// Auth holds configuration of authentication and authorization services
	// used by Service Mesh in Opendatahub.
	Auth AuthSpec `json:"auth,omitempty"`
}

type MeshSpec struct {
	// Name is a name Service Mesh Control Plan. Defaults to "basic".
	// +kubebuilder:default=basic
	Name string `json:"name,omitempty"`
	// Namespace is a namespace where Service Mesh is deployed. Defaults to "istio-system".
	// +kubebuilder:default=istio-system
	Namespace string `json:"namespace,omitempty"`
	// MetricsCollection specifies if metrics from components on the Mesh namespace
	// should be collected. Setting the value to "Istio" will collect metrics from the
	// control plane and any proxies on the Mesh namespace (like gateway pods). Setting
	// to "None" will disable metrics collection.
	// +kubebuilder:validation:Enum=Istio;None
	// +kubebuilder:default=Istio
	MetricsCollection string `json:"monitoring,omitempty"`
	// IdentityType specifies the identity implementation used in the Mesh. For ROSA
	// clusters, you would need to set this to ThirdParty.
	// +kubebuilder:validation:Enum=Kubernetes;ThirdParty
	// +kubebuilder:default=Kubernetes
	IdentityType string `json:"identityType,omitempty"`
	// Certificate allows to define how to use certificates for the Service Mesh communication.
	Certificate CertificateSpec `json:"certificate,omitempty"`
}

type AuthSpec struct {
	// Namespace where it is deployed.
	// +kubebuilder:default=auth-provider
	Namespace string `json:"namespace,omitempty"`
	// Authorino holds configuration of Authorino service used as external authorization provider.
	Authorino AuthorinoSpec `json:"authorino,omitempty"`
}

type AuthorinoSpec struct {
	// Name specifies how external authorization provider should be called.
	// +kubebuilder:default=authorino-mesh-authz-provider
	Name string `json:"name,omitempty"`
	// Audiences is a list of the identifiers that the resource server presented
	// with the token identifies as. Audience-aware token authenticators will verify
	// that the token was intended for at least one of the audiences in this list.
	// If no audiences are provided, the audience will default to the audience of the
	// Kubernetes apiserver (kubernetes.default.svc).
	// +kubebuilder:default={"https://kubernetes.default.svc"}
	Audiences []string `json:"audiences,omitempty"`
	// Label narrows amount of AuthConfigs to process by Authorino service.
	// +kubebuilder:default=authorino/topic=odh
	Label string `json:"label,omitempty"`
	// Image allows to define a custom container image to be used when deploying Authorino's instance.
	// +kubebuilder:default="quay.io/kuadrant/authorino:v0.13.0"
	Image string `json:"image,omitempty"`
}
