/*
Copyright (c) 2016-2017 Bitnami
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package io

import (
	"context"
	"fmt"
	"github.com/ghodss/yaml"
	"github.com/go-logr/logr"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/common"
	"github.com/pkg/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"os"
	"path/filepath"
	"regexp"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
)

const (
	YamlSeparator  = "(?m)^---[ \t]*$"
	gatewayPattern = `ISTIO_GATEWAY=(.*)/odh-gateway`
)

type NameValue struct {
	Name  string
	Value string
}

func CreateResourceFromFile(client client.Client, log logr.Logger, ownerRef metav1.OwnerReference, filename string, elems ...NameValue) error {
	elemsMap := make(map[string]NameValue)
	for _, nv := range elems {
		elemsMap[nv.Name] = nv
	}

	data, err := os.ReadFile(filename)
	if err != nil {
		return errors.WithStack(err)
	}
	splitter := regexp.MustCompile(YamlSeparator)
	objectStrings := splitter.Split(string(data), -1)
	for _, str := range objectStrings {
		if strings.TrimSpace(str) == "" {
			continue
		}
		u := &unstructured.Unstructured{}
		if err := yaml.Unmarshal([]byte(str), u); err != nil {
			return errors.WithStack(err)
		}

		name := u.GetName()
		namespace := u.GetNamespace()
		if namespace == "" {
			if val, exists := elemsMap["namespace"]; exists {
				u.SetNamespace(val.Value)
			} else {
				u.SetNamespace("default")
			}
		}

		u.SetOwnerReferences([]metav1.OwnerReference{
			ownerRef,
		})

		log.Info("Creating resource", "name", name)

		err := client.Get(context.TODO(), k8stypes.NamespacedName{Name: name, Namespace: namespace}, u.DeepCopy())
		if err == nil {
			log.Info("Object already exists...")
			continue
		}
		if !k8serrors.IsNotFound(err) {
			return errors.WithStack(err)
		}

		err = client.Create(context.TODO(), u)
		if err != nil {
			return errors.WithStack(err)
		}
	}
	return nil
}

func PatchResourceFromFile(dynamicClient dynamic.Interface, log logr.Logger, filename string, elems ...NameValue) error {
	elemsMap := make(map[string]NameValue)
	for _, nv := range elems {
		elemsMap[nv.Name] = nv
	}

	data, err := os.ReadFile(filename)
	if err != nil {
		return errors.WithStack(err)
	}
	splitter := regexp.MustCompile(YamlSeparator)
	objectStrings := splitter.Split(string(data), -1)
	for _, str := range objectStrings {
		if strings.TrimSpace(str) == "" {
			continue
		}
		p := &unstructured.Unstructured{}
		if err := yaml.Unmarshal([]byte(str), p); err != nil {
			log.Error(err, "error unmarshalling yaml")
			return errors.WithStack(err)
		}

		// Adding `namespace:` to Namespace resource doesn't make sense
		if p.GetKind() != "Namespace" {
			namespace := p.GetNamespace()
			if namespace == "" {
				if val, exists := elemsMap["namespace"]; exists {
					p.SetNamespace(val.Value)
				} else {
					p.SetNamespace("default")
				}
			}
		}

		gvr := schema.GroupVersionResource{
			Group:    strings.ToLower(p.GroupVersionKind().Group),
			Version:  p.GroupVersionKind().Version,
			Resource: strings.ToLower(p.GroupVersionKind().Kind) + "s",
		}

		// Convert the patch from YAML to JSON
		patchAsJSON, err := yaml.YAMLToJSON(data)
		if err != nil {
			log.Error(err, "error converting yaml to json")
			return errors.WithStack(err)
		}

		_, err = dynamicClient.Resource(gvr).
			Namespace(p.GetNamespace()).
			Patch(context.TODO(), p.GetName(), k8stypes.MergePatchType, patchAsJSON, metav1.PatchOptions{})
		if err != nil {
			log.Error(err, "error patching resource",
				"gvr", fmt.Sprintf("%+v\n", gvr),
				"patch", fmt.Sprintf("%+v\n", p),
				"json", fmt.Sprintf("%+v\n", patchAsJSON))
			return errors.WithStack(err)
		}

		if err != nil {
			return errors.WithStack(err)
		}
	}
	return nil
}

func OverwriteGatewayName(namespace string, kfnotebookControllerServiceMeshPath string) error {
	ossmEnv := filepath.Join(kfnotebookControllerServiceMeshPath, "ossm.env")
	replacement := fmt.Sprintf("ISTIO_GATEWAY=%s/odh-gateway", namespace)

	return common.ReplaceInFile(ossmEnv, map[string]string{gatewayPattern: replacement})
}
