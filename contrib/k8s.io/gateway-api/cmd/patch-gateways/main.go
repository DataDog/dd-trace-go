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
	v1 "k8s.io/api/core/v1"
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
	selector  = flag.String("selector", "", "Label selector to filter Gateways")
	prompt    = flag.Bool("prompt", false, "Prompt before applying changes to each Gateway")
	timeout   = flag.Duration("timeout", time.Minute, "Timeout for the operation")
	namespace = flag.String("namespace", "", "Namespace where the request-mirror is (defaults to current context)")
)

var gvr = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "gateways",
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
		log.Fatalf("Failed to load kubeconfig: %s (Please set KUBECONFIG manually)", err.Error())
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create dynamic client: %s", err.Error())
	}

	log.Printf("Adding access to namespace %s to gateway.networking.k8s.io/v1/Gateway...\n", *namespace)

	// List all Gateways across all namespaces
	gatewayList, err := dynClient.Resource(gvr).List(ctx, metav1.ListOptions{LabelSelector: *selector})
	if err != nil {
		log.Fatalf("Failed to list Gateways: %s", err.Error())
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
		for i := range gateway.Spec.Listeners {
			if mayPatchListener(gw.GetNamespace(), &gateway.Spec.Listeners[i]) {
				needsPatch = true
			}
		}

		if !needsPatch {
			fmt.Printf("No patch needed for Gateway %s/%s\n", gw.GetNamespace(), gw.GetName())
			continue
		}

		// Prepare the patch
		var output bytes.Buffer
		encoder := json.NewEncoder(&output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(map[string]any{
			"spec": map[string]any{
				"listeners": gateway.Spec.Listeners,
			},
		}); err != nil {
			log.Printf("Failed to marshal patch for Gateway %s/%s: %v", gw.GetNamespace(), gw.GetName(), err)
			continue
		}

		patchData := output.Bytes()

		// Prompt the user if needed
		if *prompt {
			fmt.Printf("Found Gateway %s/%s that needs patching\n", gw.GetNamespace(), gw.GetName())
			fmt.Printf("Patch is as follows:\n")
			fmt.Println(string(patchData))
			fmt.Printf("Patch Gateway %s/%s? (y/n): ", gw.GetNamespace(), gw.GetName())
			var response string
			if _, err := fmt.Scanln(&response); err != nil {
				log.Fatalf("Failed to read response: %s", err.Error())
			}
			if resp, err := strconv.ParseBool(response); err != nil || !resp {
				fmt.Printf("Skipping Gateway %s/%s\n", gw.GetNamespace(), gw.GetName())
				continue
			}
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

func mayPatchListener(gwNamespace string, listener *gatewayv1.Listener) bool {
	nsFromPtr := listener.AllowedRoutes.Namespaces.From

	nsFrom := gatewayv1.NamespacesFromSame
	if nsFromPtr != nil {
		nsFrom = *nsFromPtr
	}

	switch nsFrom {
	case gatewayv1.NamespacesFromAll:
		return false
	case gatewayv1.NamespacesFromSame:
		if gwNamespace == *namespace {
			return false
		}

		// Transform the selector to match the current namespace
		listener.AllowedRoutes.Namespaces.From = ptr.To(gatewayv1.NamespacesFromSelector)
		listener.AllowedRoutes.Namespaces.Selector = &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{
					Key:      v1.LabelMetadataName,
					Operator: metav1.LabelSelectorOpIn,
					Values:   []string{*namespace},
				},
			},
		}
		fallthrough
	case gatewayv1.NamespacesFromSelector:
		if listener.AllowedRoutes.Namespaces.Selector.MatchLabels == nil {
			listener.AllowedRoutes.Namespaces.Selector.MatchLabels = make(map[string]string)
		}

		if listener.AllowedRoutes.Namespaces.Selector.MatchLabels[v1.LabelMetadataName] == *namespace {
			return false
		}

		// Add the current namespace to the selector
		var preExistingMatcher *metav1.LabelSelectorRequirement
		for j, expression := range listener.AllowedRoutes.Namespaces.Selector.MatchExpressions {
			if expression.Key == v1.LabelMetadataName && expression.Operator == metav1.LabelSelectorOpIn {
				preExistingMatcher = &listener.AllowedRoutes.Namespaces.Selector.MatchExpressions[j]
				if slices.Contains(expression.Values, *namespace) {
					return false
				}
			}
		}

		if preExistingMatcher != nil {
			preExistingMatcher.Values = append(preExistingMatcher.Values, *namespace)
			return true
		}

		listener.AllowedRoutes.Namespaces.Selector.MatchExpressions = append(listener.AllowedRoutes.Namespaces.Selector.MatchExpressions, metav1.LabelSelectorRequirement{
			Key:      v1.LabelMetadataName,
			Operator: metav1.LabelSelectorOpIn,
			Values:   []string{*namespace},
		})

		return true
	default:
		log.Fatalf("Unknown namespace selector: %s", nsFrom)
	}

	return false
}
