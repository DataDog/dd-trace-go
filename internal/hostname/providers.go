package hostname

import (
	"context"
	"fmt"
	"os"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/hostname/aws"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/hostname/azure"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/hostname/gce"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/hostname/validate"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

var cachedHostname string

// getCached returns the cached hostname if it is still valid, empty string otherwise
func getCached() string {
	return cachedHostname
}

type provider struct {
	name string
	// Should we stop going down the list of providers if this one is successful
	stopIfSuccessful bool
	pf               providerFetch
}

type providerFetch func(ctx context.Context, currentHostname string) (string, error)

var providerCatalog = []provider{
	{
		name:             "configuration",
		stopIfSuccessful: true,
		pf:               fromConfig,
	},
	{
		name:             "fargate",
		stopIfSuccessful: true,
		pf:               fromFargate,
	},
	{
		name:             "gce",
		stopIfSuccessful: true,
		pf:               fromGce,
	},
	{
		name:             "azure",
		stopIfSuccessful: true,
		pf:               fromAzure,
	},
	// The following providers are coupled. Their behavior changes depending on the result of the previous provider.
	// Therefore, 'stopIfSuccessful' is set to false.
	{
		name:             "fqdn",
		stopIfSuccessful: false,
		pf:               fromFQDN,
	},
	{
		name:             "container",
		stopIfSuccessful: false,
		pf:               fromContainer,
	},
	{
		name:             "os",
		stopIfSuccessful: false,
		pf:               fromOS,
	},
	{
		name:             "aws",
		stopIfSuccessful: false,
		pf:               fromEC2,
	},
}

// Get returns the hostname for the tracer
func Get(ctx context.Context) (string, error) {
	if ch := getCached(); ch != "" {
		return ch, nil
	}
	err := LoadHostname(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to reliably determine hostname. You can define one via env var DD_HOSTNAME")
	}
	return cachedHostname, nil
}

// LoadHostname attempts to look up and cache the hostname for this application.
func LoadHostname(ctx context.Context) error {
	var hostname string

	for _, p := range providerCatalog {
		detectedHostname, err := p.pf(ctx, hostname)
		if err != nil {
			log.Debug("Unable to get hostname from provider %s: %v", p.name, err)
			continue
		}
		hostname = detectedHostname
		if p.stopIfSuccessful {
			cachedHostname = hostname
			return nil
		}
	}
	return fmt.Errorf("unable to reliably determine hostname. You can define one via env var DD_HOSTNAME")
}

func fromConfig(ctx context.Context, _ string) (string, error) {
	hn := os.Getenv("DD_HOSTNAME")
	err := validate.ValidHostname(hn)
	if err != nil {
		return "", err
	}

	return "", nil
}

func fromFargate(ctx context.Context, _ string) (string, error) {
	if _, ok := os.LookupEnv("ECS_CONTAINER_METADATA_URI_V4"); !ok {
		return "", fmt.Errorf("not running in fargate")
	}
	launchType, err := aws.GetLaunchType(ctx)
	if err != nil {
		return "", err
	}
	if launchType == "FARGATE" {
		// If we're running on fargate we strip the hostname
		return "", nil
	}
	return "", fmt.Errorf("not running in fargate")
}

func fromGce(ctx context.Context, _ string) (string, error) {
	return gce.GetCanonicalHostname(ctx)
}

func fromAzure(ctx context.Context, _ string) (string, error) {
	return azure.GetHostname(ctx)
}

func fromFQDN(ctx context.Context, _ string) (string, error) {
	//TODO: test this on windows
	fqdn, err := getSystemFQDN()
	if err != nil {
		return "", fmt.Errorf("unable to get FQDN from system: %s", err)
	}
	return fqdn, nil
}

func fromOS(_ context.Context, currentHostname string) (string, error) {
	if currentHostname == "" {
		return os.Hostname()
	}
	return "", fmt.Errorf("skipping OS hostname as a previous provider found a valid hostname")
}

func fromContainer(_ context.Context, _ string) (string, error) {
	//TODO: Impl me
	return "", fmt.Errorf("container hostname detection not implemented")
}

func fromEC2(_ context.Context, _ string) (string, error) {
	//TODO: Impl me
	return "", fmt.Errorf("EC2 hostname detection not implemented")
}
