package hostname

import (
	"context"
	"fmt"
	"os"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/hostname/azure"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/hostname/gce"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/hostname/validate"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

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
}

// Get returns the hostname for the tracer
func Get(ctx context.Context) (string, error) {
	now := time.Now()
	if ch := getCached(now); ch != "" {
		return ch, nil
	}

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
			cachedAt = now
			return hostname, nil
		}
	}

	return "", fmt.Errorf("unable to reliably determine hostname. You can define one ")
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
	// TODO: how can we tell if we're in fargate without asking the user to set an env-var
	return "", fmt.Errorf("not running in fargate")
}

func fromGce(ctx context.Context, _ string) (string, error) {
	return gce.GetCanonicalHostname(ctx)
}

func fromAzure(ctx context.Context, _ string) (string, error) {
	return azure.GetHostname(ctx)
}

func fromFQDN(ctx context.Context, _ string) (string, error) {
	// TODO: implement
	//if !osHostnameUsable(ctx) {
	//	return "", fmt.Errorf("FQDN hostname is not usable")
	//}
	//
	//if config.Datadog.GetBool("hostname_fqdn") {
	//	fqdn, err := fqdnHostname()
	//	if err == nil {
	//		return fqdn, nil
	//	}
	//	return "", fmt.Errorf("Unable to get FQDN from system: %s", err)
	//}
	return "", fmt.Errorf("'hostname_fqdn' configuration is not enabled")
}

var cachedHostname string
var cachedAt time.Time
var cacheExpiration = 5 * time.Minute //TODO: the agent never expires the hostname once it's been found. should we do the same?

// getCached returns the cached hostname if it is still valid, empty string otherwise
func getCached(now time.Time) string {
	if now.Sub(cachedAt) > cacheExpiration {
		cachedHostname = ""
	}
	return cachedHostname
}
