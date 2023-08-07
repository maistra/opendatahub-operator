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

package ossm

import (
	"context"
	"fmt"
	"github.com/ghodss/yaml"
	configtypes "github.com/opendatahub-io/opendatahub-operator/apis/config"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"os"
	"regexp"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
)

const (
	YamlSeparator = "(?m)^---[ \t]*$"
)

func (o *OssmInstaller) CreateResourceFromFile(filename string, elems ...configtypes.NameValue) error {
	elemsMap := make(map[string]configtypes.NameValue)
	for _, nv := range elems {
		elemsMap[nv.Name] = nv
	}
	c, err := client.New(o.config, client.Options{})
	if err != nil {
		return errors.WithStack(err)
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
			{
				APIVersion: o.tracker.APIVersion,
				Kind:       o.tracker.Kind,
				Name:       o.tracker.Name,
				UID:        o.tracker.UID,
			},
		})

		logrus.Infof("Creating %s", name)

		err := c.Get(context.TODO(), k8stypes.NamespacedName{Name: name, Namespace: namespace}, u.DeepCopy())
		if err == nil {
			log.Info("Object already exists...")
			continue
		}
		if !k8serrors.IsNotFound(err) {
			return errors.WithStack(err)
		}

		err = c.Create(context.TODO(), u)
		if err != nil {
			return errors.WithStack(err)
		}
	}
	return nil
}

func (o *OssmInstaller) PatchResourceFromFile(filename string, elems ...configtypes.NameValue) error {
	elemsMap := make(map[string]configtypes.NameValue)
	for _, nv := range elems {
		elemsMap[nv.Name] = nv
	}

	dynamicClient, err := dynamic.NewForConfig(o.config)
	if err != nil {
		return errors.WithStack(err)
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
			logrus.Error("error unmarshalling yaml")
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
		patchAsJson, err := yaml.YAMLToJSON(data)
		if err != nil {
			logrus.Error("error converting yaml to json")
			return errors.WithStack(err)
		}

		_, err = dynamicClient.Resource(gvr).
			Namespace(p.GetNamespace()).
			Patch(context.Background(), p.GetName(), k8stypes.MergePatchType, patchAsJson, metav1.PatchOptions{})
		if err != nil {
			logrus.Error("error patching resource\n",
				fmt.Sprintf("%+v\n", gvr),
				fmt.Sprintf("%+v\n", p),
				fmt.Sprintf("%+v\n", patchAsJson))
			return errors.WithStack(err)
		}

		if err != nil {
			return errors.WithStack(err)
		}
	}
	return nil
}

func (o *OssmInstaller) CheckForCRD(group string, version string, resource string) error {
	dynamicClient, err := dynamic.NewForConfig(o.config)
	if err != nil {
		log.Error(err, "Failed to initialize dynamic client")
	}

	crdGVR := schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}

	_, err = dynamicClient.Resource(crdGVR).List(context.Background(), metav1.ListOptions{})
	return err
}

func (o *OssmInstaller) CheckSMCPStatus(name string, namespace string) (string, error) {
	dynamicClient, err := dynamic.NewForConfig(o.config)
	if err != nil {
		log.Info("Failed to initialize dynamic client")
		return "", err
	}

	gvr := schema.GroupVersionResource{
		Group:    "maistra.io",
		Version:  "v1",
		Resource: "servicemeshcontrolplanes",
	}

	unstructObj, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		log.Info("Failed to find SMCP")
		return "", err
	}

	log.Info("hi", "obj", unstructObj)

	conditions, found, err := unstructured.NestedSlice(unstructObj.Object, "status", "conditions")
	if err != nil || !found {
		log.Info("status conditions not found or error in parsing of SMCP")
		return "", err
	}

	// Getting status of last condition to check if it is "Ready"
	lastCondition := conditions[len(conditions)-1].(map[string]interface{})
	status := lastCondition["type"].(string)

	return status, err
}

// CreateSMCP uses dynamic client to create a dummy SMCP for testing
func (o *OssmInstaller) CreateSMCP(namespace string, smcpObj *unstructured.Unstructured) error {
	dynamicClient, err := dynamic.NewForConfig(o.config)
	if err != nil {
		log.Info("Failed to initialize dynamic client")
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "maistra.io",
		Version:  "v1",
		Resource: "servicemeshcontrolplanes",
	}

	result, err := dynamicClient.Resource(gvr).Namespace(namespace).Create(context.TODO(), smcpObj, metav1.CreateOptions{})
	if err != nil {
		log.Info("Failed to create SMCP", "err:", err)
		return err
	}

	// Since we don't have maistra operator, we simulate the status
	statusConditions := []interface{}{
		map[string]interface{}{
			"type":   "Ready",
			"status": "True",
		},
	}

	status := map[string]interface{}{
		"conditions": statusConditions,
	}

	if err := unstructured.SetNestedField(result.Object, status, "status"); err != nil {
		log.Info("Failed to set status field", "err:", err)
		return err
	}

	_, err = dynamicClient.Resource(gvr).Namespace(namespace).UpdateStatus(context.TODO(), result, metav1.UpdateOptions{})
	if err != nil {
		log.Info("Failed to update SMCP status", "err:", err)
		return err
	}

	return nil
}
