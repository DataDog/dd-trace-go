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

	"github.com/DataDog/dd-trace-go/v2/instrumentation/env"
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

func main() {
	flag.Parse()
	log := log.New(os.Stderr, "", 0)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(*timeout))
	defer cancel()

	// Load kubeconfig
	kubeconfig := env.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = clientcmd.RecommendedHomeFile
	}

	if *namespace == "" {
		cfg, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
		if err != nil {
			log.Fatalf("Failed to load kubeconfig: %s", err.Error())
		}

		namespace = &cfg.Contexts[cfg.CurrentContext].Namespace
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		log.Fatalf("Failed to load kubeconfig: %s", err.Error())
	}

	// Create dynamic client
	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create dynamic client: %s", err.Error())
	}

	log.Println("Listing gateway.networking.k8s.io/v1/HTTPRoute...")

	// List all HTTPRoute across all namespaces
	routes, err := dynClient.Resource(gvr).List(ctx, metav1.ListOptions{
		LabelSelector: *selector,
	})
	if err != nil {
		log.Fatalf("Failed to list HTTPRoutes: %s", err.Error())
	}

	for _, rawRoute := range routes.Items {
		route, err := parseRoute(rawRoute.Object)
		if err != nil {
			log.Fatalf("Failed to parse HTTPRoute %s/%s: %v\n", rawRoute.GetNamespace(), rawRoute.GetName(), err)
		}

		if !mayModifyRoute(route) {
			log.Printf("No patch needed for HTTPRoute %s/%s\n", route.Namespace, route.Name)
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
			log.Printf("Failed to marshal patch for HTTPRoute %s/%s: %v\n", route.Namespace, route.Name, err)
			continue
		}

		patchData := output.Bytes()

		// Prompt the user if needed
		if *prompt && !promptUser(route, patchData) {
			log.Printf("Skipping HTTPRoute %s/%s\n", route.Namespace, route.Name)
			continue
		}

		// Apply the patch
		_, err = dynClient.Resource(gvr).Namespace(route.Namespace).Patch(
			ctx,
			route.Name,
			types.MergePatchType,
			patchData,
			metav1.PatchOptions{},
		)
		if err != nil {
			log.Printf("Failed to patch HTTPRoute %s/%s: %v\n", route.Namespace, route.Name, err)
			continue
		}

		fmt.Printf("Patched HTTPRoute %s/%s\n", route.Namespace, route.Name)
	}
}

func parseRoute(raw map[string]any) (*gatewayv1.HTTPRoute, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}

	var route gatewayv1.HTTPRoute
	if err := yaml.Unmarshal(data, &route); err != nil {
		return nil, err
	}

	return &route, nil
}

func mayModifyRoute(route *gatewayv1.HTTPRoute) bool {
	if value, ok := route.Labels["admission.datadoghq.com/enabled"]; ok && value == "false" {
		log.Printf("skipping HTTPRoute %s/%s due to label admission.datadoghq.com/enabled=false", route.Namespace, route.Name)
		return false
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

		if route.Namespace != *namespace {
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

	return needsPatch
}

func promptUser(route *gatewayv1.HTTPRoute, patchData []byte) bool {
	fmt.Printf("Found HTTPRoute %s/%s that needs patching\n", route.Namespace, route.Name)
	fmt.Printf("Patch is as follows:\n")
	fmt.Println(string(patchData))
	fmt.Printf("Patch HTTPRoute %s/%s? (y/n): ", route.Namespace, route.Name)
	var response string
	if _, err := fmt.Scanln(&response); err != nil {
		log.Fatalf("Failed to read response: %s", err.Error())
	}
	if resp, err := strconv.ParseBool(response); err != nil || !resp {
		return false
	}

	return true
}

// alreadyContainsOurFilter checks if the filter already exists in the [gatewayv1.HTTPRouteFilters] list.
func alreadyContainsOurFilter(newBackend gatewayv1.BackendObjectReference, filter gatewayv1.HTTPRouteFilter) bool {
	return filter.Type == gatewayv1.HTTPRouteFilterRequestMirror &&
		filter.RequestMirror != nil &&
		filter.RequestMirror.BackendRef.Name == newBackend.Name &&
		filter.RequestMirror.BackendRef.Namespace != nil && *filter.RequestMirror.BackendRef.Namespace == *newBackend.Namespace &&
		filter.RequestMirror.BackendRef.Port != nil && *filter.RequestMirror.BackendRef.Port == *newBackend.Port
}
