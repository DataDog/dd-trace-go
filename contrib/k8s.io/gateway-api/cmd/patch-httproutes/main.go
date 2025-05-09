// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"slices"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"
)

var (
	selector  = flag.String("selector", "", "Label selector to filter HTTPRoute")
	prompt    = flag.Bool("prompt", false, "Prompt before applying changes to each HTTPRoute")
	timeout   = flag.Duration("timeout", 1*time.Minute, "Timeout for the operation")
	service   = flag.String("service", "request-mirror", "Service name to mirror requests to")
	port      = flag.Int("port", 8080, "Service port to mirror requests to")
	namespace = flag.String("namespace", "", "Namespace where the request-mirror is (defaults to current context)")
)

var gvr = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "httproutes",
}

// alreadyContainsOurFilter checks if the filter already exists in the [gatewayv1.HTTPRouteFilters] list.
func alreadyContainsOurFilter(newBackend gatewayv1.BackendObjectReference, filter gatewayv1.HTTPRouteFilter) bool {
	return filter.Type == gatewayv1.HTTPRouteFilterRequestMirror &&
		filter.RequestMirror != nil &&
		filter.RequestMirror.BackendRef.Name == newBackend.Name &&
		filter.RequestMirror.BackendRef.Namespace != nil && *filter.RequestMirror.BackendRef.Namespace == *newBackend.Namespace &&
		filter.RequestMirror.BackendRef.Port != nil && *filter.RequestMirror.BackendRef.Port == *newBackend.Port
}

func main() {
	flag.Parse()
	log := log.New(os.Stdout, "", 0)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(*timeout))
	defer cancel()

	// Load kubeconfig
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = clientcmd.RecommendedHomeFile
	}

	if *namespace == "" {
		cfg, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
		if err != nil {
			log.Fatalf("Failed to load kubeconfig: %v", err)
		}

		namespace = &cfg.Contexts[cfg.CurrentContext].Namespace
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

	log.Println("Listing gateway.networking.k8s.io/v1/HTTPRoute...")

	// List all HTTPRoute across all namespaces
	routes, err := dynClient.Resource(gvr).List(ctx, metav1.ListOptions{
		LabelSelector: *selector,
	})
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
			newFilter := gatewayv1.HTTPRouteFilter{
				Type: gatewayv1.HTTPRouteFilterRequestMirror,
				RequestMirror: &gatewayv1.HTTPRequestMirrorFilter{
					BackendRef: gatewayv1.BackendObjectReference{
						Name: gatewayv1.ObjectName(*service),
						Port: ptr.To(gatewayv1.PortNumber(*port)),
					},
				},
			}

			if route.GetNamespace() != *namespace {
				newFilter.RequestMirror.BackendRef.Namespace = ptr.To(gatewayv1.Namespace(*namespace))
			}

			if slices.ContainsFunc(rule.Filters, func(filter gatewayv1.HTTPRouteFilter) bool {
				return alreadyContainsOurFilter(newFilter.RequestMirror.BackendRef, filter)
			}) {
				continue
			}

			needsPatch = true
			// Add the filter
			route.Spec.Rules[i].Filters = append(route.Spec.Rules[i].Filters, newFilter)
		}

		if !needsPatch {
			log.Printf("No patch needed for HTTPRoute %s/%s\n", route.GetNamespace(), route.GetName())
			continue
		}

		// Prepare the patch
		var output bytes.Buffer
		encoder := json.NewEncoder(&output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(map[string]any{
			"spec": map[string]any{
				"rules": route.Spec.Rules,
			},
		}); err != nil {
			log.Printf("Failed to marshal patch for HTTPRoute %s/%s: %v", route.GetNamespace(), route.GetName(), err)
			continue
		}

		patchData := output.Bytes()

		// Prompt the user if needed
		if *prompt {
			fmt.Printf("Found HTTPRoute %s/%s that needs patching\n", route.GetNamespace(), route.GetName())
			fmt.Printf("Patch is as follows:\n")
			fmt.Println(string(patchData))
			fmt.Printf("Patch HTTPRoute %s/%s? (y/n): ", route.GetNamespace(), route.GetName())
			var response string
			if _, err := fmt.Scanln(&response); err != nil {
				log.Fatalf("Failed to read response: %v", err)
			}
			if resp, err := strconv.ParseBool(response); err != nil || !resp {
				fmt.Printf("Skipping HTTPRoute %s/%s\n", route.GetNamespace(), route.GetName())
				continue
			}
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

		fmt.Printf("Patched HTTPRoute %s/%s\n", route.GetNamespace(), route.GetName())
	}
}
