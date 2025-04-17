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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	gatewayapi "github.com/DataDog/dd-trace-go/contrib/k8s.io/gateway-api/v2"
)

func main() {
	ctx := context.Background()

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
		Resource: "gateways",
	}

	// List all Gateways across all namespaces
	gatewayList, err := dynClient.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Fatalf("Failed to list Gateways: %v", err)
	}

	for _, gw := range gatewayList.Items {
		// Marshal and unmarshal to Gateway struct
		data, err := json.Marshal(gw.Object)
		if err != nil {
			log.Printf("Failed to marshal Gateway %s/%s: %v", gw.GetNamespace(), gw.GetName(), err)
			continue
		}

		var gateway gatewayv1.Gateway
		if err := yaml.Unmarshal(data, &gateway); err != nil {
			log.Printf("Failed to unmarshal Gateway %s/%s: %v", gw.GetNamespace(), gw.GetName(), err)
			continue
		}

		if value, ok := gateway.Labels["admission.datadoghq.com/enabled"]; ok && value == "false" {
			log.Printf("Skipping Gateway %s/%s due to admission label", gw.GetNamespace(), gw.GetName())
			continue
		}

		// Flag to determine if patch is needed
		needsPatch := false

		// Iterate over listeners
		for i, listener := range gateway.Spec.Listeners {
			nsFromPtr := listener.AllowedRoutes.Namespaces.From

			nsFrom := gatewayv1.NamespacesFromSame
			if nsFromPtr != nil {
				nsFrom = *nsFromPtr
			}

			switch nsFrom {
			case gatewayv1.NamespacesFromAll:
				continue
			case gatewayv1.NamespacesFromSame:
				// Transform the selector to match the current namespace
				gateway.Spec.Listeners[i].AllowedRoutes.Namespaces.Selector = &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"kubernetes.io/metadata.name": gw.GetNamespace(),
					},
				}
				fallthrough
			case gatewayv1.NamespacesFromSelector:
				// Add our label in there
				if gateway.Spec.Listeners[i].AllowedRoutes.Namespaces.Selector.MatchLabels == nil {
					gateway.Spec.Listeners[i].AllowedRoutes.Namespaces.Selector.MatchLabels = make(map[string]string)
				}

				if gateway.Spec.Listeners[i].AllowedRoutes.Namespaces.Selector.MatchLabels[gatewayapi.RequestMirrorLabelKey] != gatewayapi.RequestMirrorLabelValue {
					needsPatch = true
					gateway.Spec.Listeners[i].AllowedRoutes.Namespaces.Selector.MatchLabels[gatewayapi.RequestMirrorLabelKey] = gatewayapi.RequestMirrorLabelValue
				}
			}
		}

		if !needsPatch {
			fmt.Printf("No patch needed for Gateway %s/%s\n", gw.GetNamespace(), gw.GetName())
			continue
		}

		// Prepare the patch
		patchData, err := json.Marshal(map[string]any{
			"spec": map[string]any{
				"listeners": gateway.Spec.Listeners,
			},
		})
		if err != nil {
			log.Printf("Failed to marshal patch for Gateway %s/%s: %v", gw.GetNamespace(), gw.GetName(), err)
			continue
		}

		// Apply the patch
		_, err = dynClient.Resource(gvr).Namespace(gw.GetNamespace()).Patch(
			ctx,
			gw.GetName(),
			types.MergePatchType,
			patchData,
			metav1.PatchOptions{},
		)
		if err != nil {
			log.Printf("Failed to patch Gateway %s/%s: %v", gw.GetNamespace(), gw.GetName(), err)
			continue
		}

		fmt.Printf("Patched Gateway %s/%s\n", gw.GetNamespace(), gw.GetName())
	}
}
