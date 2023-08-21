package ossm

import (
	"context"
	"fmt"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func removeTokenVolumes(feature *Feature) error {
	tokenVolume := fmt.Sprintf("%s-oauth2-tokens", feature.spec.AppNamespace)

	gvr := schema.GroupVersionResource{
		Group:    "maistra.io",
		Version:  "v2",
		Resource: "servicemeshcontrolplanes",
	}

	smcp, err := feature.dynamicClient.Resource(gvr).Namespace(feature.spec.Mesh.Namespace).Get(context.Background(), feature.spec.Mesh.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	volumes, found, err := unstructured.NestedSlice(smcp.Object, "spec", "gateways", "ingress", "volumes")
	if err != nil {
		return err
	}
	if !found {
		log.Info("no volumes found", "smcp", feature.spec.Mesh.Name, "istio-ns", feature.spec.Mesh.Namespace)
		return nil
	}

	for i, v := range volumes {
		volume, ok := v.(map[string]interface{})
		if !ok {
			fmt.Println("Unexpected type for volume")
			continue
		}

		volumeMount, found, err := unstructured.NestedMap(volume, "volumeMount")
		if err != nil {
			return err
		}
		if !found {
			fmt.Println("No volumeMount found in the volume")
			continue
		}

		if volumeMount["name"] == tokenVolume {
			volumes = append(volumes[:i], volumes[i+1:]...)
			err = unstructured.SetNestedSlice(smcp.Object, volumes, "spec", "gateways", "ingress", "volumes")
			if err != nil {
				return err
			}
			break
		}
	}

	_, err = feature.dynamicClient.Resource(gvr).Namespace(feature.spec.Mesh.Namespace).Update(context.Background(), smcp, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	return nil
}

func removeOAuthClient(feature *Feature) error {
	oauthClientName := fmt.Sprintf("%s-oauth2-client", feature.spec.AppNamespace)
	gvr := schema.GroupVersionResource{
		Group:    "oauth.openshift.io",
		Version:  "v1",
		Resource: "oauthclients",
	}

	if _, err := feature.dynamicClient.Resource(gvr).Get(context.Background(), oauthClientName, metav1.GetOptions{}); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}

		return err
	}

	if err := feature.dynamicClient.Resource(gvr).Delete(context.Background(), oauthClientName, metav1.DeleteOptions{}); err != nil {
		log.Error(err, "failed deleting OAuthClient", "name", oauthClientName)
		return err
	}

	return nil
}
