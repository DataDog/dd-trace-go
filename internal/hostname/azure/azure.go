// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/hostname/cachedfetch"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/hostname/httputils"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/hostname/validate"
)

// declare these as vars not const to ease testing
var (
	metadataURL = "http://169.254.169.254"
	timeout     = 300 * time.Millisecond

	// CloudProviderName contains the inventory name of for EC2
	CloudProviderName = "Azure"
)

const hostnameStyleSetting = "azure_hostname_style"

// IsRunningOn returns true if the agent is running on Azure
func IsRunningOn(ctx context.Context) bool {
	if _, err := GetHostAliases(ctx); err == nil {
		return true
	}
	return false
}

var vmIDFetcher = cachedfetch.Fetcher{
	Name: "Azure vmID",
	Attempt: func(ctx context.Context) (interface{}, error) {
		res, err := getResponseWithMaxLength(ctx,
			metadataURL+"/metadata/instance/compute/vmId?api-version=2017-04-02&format=text",
			255)
		if err != nil {
			return nil, fmt.Errorf("Azure HostAliases: unable to query metadata endpoint: %s", err)
		}
		return []string{res}, nil
	},
}

// GetHostAliases returns the VM ID from the Azure Metadata api
func GetHostAliases(ctx context.Context) ([]string, error) {
	return vmIDFetcher.FetchStringSlice(ctx)
}

var resourceGroupNameFetcher = cachedfetch.Fetcher{
	Name: "Azure Cluster Name",
	Attempt: func(ctx context.Context) (interface{}, error) {
		rg, err := getResponse(ctx,
			metadataURL+"/metadata/instance/compute/resourceGroupName?api-version=2017-08-01&format=text")
		if err != nil {
			return "", fmt.Errorf("unable to query metadata endpoint: %s", err)
		}
		return rg, nil
	},
}

func getResponseWithMaxLength(ctx context.Context, endpoint string, maxLength int) (string, error) {
	result, err := getResponse(ctx, endpoint)
	if err != nil {
		return result, err
	}
	if len(result) > maxLength {
		return "", fmt.Errorf("%v gave a response with length > to %v", endpoint, maxLength)
	}
	return result, err
}

func getResponse(ctx context.Context, url string) (string, error) {
	//todo: we assume azure is enabled

	return httputils.Get(ctx, url, map[string]string{"Metadata": "true"}, timeout)
}

// GetHostname returns hostname based on Azure instance metadata.
func GetHostname(ctx context.Context) (string, error) {
	return getHostnameWithConfig(ctx)
}

var instanceMetaFetcher = cachedfetch.Fetcher{
	Name: "Azure Instance Metadata",
	Attempt: func(ctx context.Context) (interface{}, error) {
		metadataJSON, err := getResponse(ctx,
			metadataURL+"/metadata/instance/compute?api-version=2017-08-01")
		if err != nil {
			return "", fmt.Errorf("failed to get Azure instance metadata: %s", err)
		}
		return metadataJSON, nil
	},
}

func getHostnameWithConfig(ctx context.Context) (string, error) {
	style := "os" // TODO: Expose new configuration for other values

	if style == "os" {
		return "", fmt.Errorf("azure_hostname_style is set to 'os'")
	}

	metadataJSON, err := instanceMetaFetcher.FetchString(ctx)
	if err != nil {
		return "", err
	}

	var metadata struct {
		VMID              string
		Name              string
		ResourceGroupName string
		SubscriptionID    string
	}
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return "", fmt.Errorf("failed to parse Azure instance metadata: %s", err)
	}

	var name string
	switch style {
	case "vmid":
		name = metadata.VMID
	case "name":
		name = metadata.Name
	case "name_and_resource_group":
		name = fmt.Sprintf("%s.%s", metadata.Name, metadata.ResourceGroupName)
	case "full":
		name = fmt.Sprintf("%s.%s.%s", metadata.Name, metadata.ResourceGroupName, metadata.SubscriptionID)
	default:
		return "", fmt.Errorf("invalid azure_hostname_style value: %s", style)
	}

	if err := validate.ValidHostname(name); err != nil {
		return "", err
	}

	return name, nil
}

var publicIPv4Fetcher = cachedfetch.Fetcher{
	Name: "Azure Public IP",
	Attempt: func(ctx context.Context) (interface{}, error) {
		publicIPv4, err := getResponse(ctx,
			metadataURL+"/metadata/instance/network/interface/0/ipv4/ipAddress/0/publicIpAddress?api-version=2017-04-02&format=text")
		if err != nil {
			return "", fmt.Errorf("failed to get Azure public ip: %s", err)
		}

		return publicIPv4, nil
	},
}

// GetPublicIPv4 returns the public IPv4 address of the current Azure instance
func GetPublicIPv4(ctx context.Context) (string, error) {
	return publicIPv4Fetcher.FetchString(ctx)
}
