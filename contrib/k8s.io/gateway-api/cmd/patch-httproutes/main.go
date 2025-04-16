// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"
)

var newFilter = gatewayv1.HTTPRouteFilter{
	Type: gatewayv1.HTTPRouteFilterRequestMirror,
	RequestMirror: &gatewayv1.HTTPRequestMirrorFilter{
		BackendRef: gatewayv1.BackendObjectReference{
			Name:      "request-mirror",
			Namespace: ptr.To[gatewayv1.Namespace]("datadog"),
			Port:      ptr.To[gatewayv1.PortNumber](8080),
		},
	},
}

// alreadyContainsOurFilter checks if the filter already exists in the [gatewayv1.HTTPRouteFilters] list.
func alreadyContainsOurFilter(filter gatewayv1.HTTPRouteFilter) bool {
	return filter.Type == newFilter.Type &&
		filter.RequestMirror != nil &&
		filter.RequestMirror.BackendRef.Name == newFilter.RequestMirror.BackendRef.Name &&
		filter.RequestMirror.BackendRef.Namespace != nil && *filter.RequestMirror.BackendRef.Namespace == *newFilter.RequestMirror.BackendRef.Namespace &&
		filter.RequestMirror.BackendRef.Port != nil && *filter.RequestMirror.BackendRef.Port == *newFilter.RequestMirror.BackendRef.Port
}

func main() {
	ctx := context.Background()
	log := log.New(os.Stdout, "", 0)

	// Load kubeconfig
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = clientcmd.RecommendedHomeFile
	}
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		log.Fatalf("Failed to load kubeconfig: %v", err)
	}

	// Create dynamic client
	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create dynamic client: %v", err)
	}

	// Define the GVR for Gateway
	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "httproutes",
	}

	// List all Gateways across all namespaces
	routes, err := dynClient.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Fatalf("Failed to list HTTPRoutes: %v", err)
	}

	for _, route := range routes.Items {
		data, err := json.Marshal(route.Object)
		if err != nil {
			log.Printf("Failed to marshal HTTPRoute %s/%s: %v", route.GetNamespace(), route.GetName(), err)
			continue
		}

		var route gatewayv1.HTTPRoute
		if err := yaml.Unmarshal(data, &route); err != nil {
			log.Printf("Failed to unmarshal HTTPRoute %s/%s: %v", route.GetNamespace(), route.GetName(), err)
			continue
		}

		if value, ok := route.Labels["admission.datadoghq.com/enabled"]; ok && value == "false" {
			log.Printf("Skipping HTTPRoute %s/%s due to admission label", route.GetNamespace(), route.GetName())
			continue
		}

		// Flag to determine if patch is needed
		needsPatch := false

		// Iterate over Rules
		for i, rule := range route.Spec.Rules {
			if slices.ContainsFunc(rule.Filters, alreadyContainsOurFilter) {
				continue
			}

			needsPatch = true
			// Add the filter
			route.Spec.Rules[i].Filters = append(route.Spec.Rules[i].Filters)
		}

		if !needsPatch {
			log.Printf("No patch needed for HTTPRoute %s/%s\n", route.GetNamespace(), route.GetName())
			continue
		}

		// Prepare the patch
		patchData, err := json.Marshal(map[string]any{
			"spec": map[string]any{
				"rules": route.Spec.Rules,
			},
		})
		if err != nil {
			log.Printf("Failed to marshal patch for HTTPRoute %s/%s: %v", route.GetNamespace(), route.GetName(), err)
			continue
		}

		// Apply the patch
		_, err = dynClient.Resource(gvr).Namespace(route.GetNamespace()).Patch(
			ctx,
			route.GetName(),
			types.MergePatchType,
			patchData,
			metav1.PatchOptions{},
		)
		if err != nil {
			log.Printf("Failed to patch HTTPRoute %s/%s: %v", route.GetNamespace(), route.GetName(), err)
			continue
		}

		fmt.Printf("Patched Gateway %s/%s\n", route.GetNamespace(), route.GetName())
	}
}
