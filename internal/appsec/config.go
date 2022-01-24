// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec

import (
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

const (
	enabledEnvVar    = "DD_APPSEC_ENABLED"
	rulesEnvVar      = "DD_APPSEC_RULES"
	wafTimeoutEnvVar = "DD_APPSEC_WAF_TIMEOUT"
)

const defaultWAFTimeout = 4 * time.Millisecond

// config is the AppSec configuration.
type config struct {
	// rules loaded via the env var DD_APPSEC_RULES. When not set, the builtin rules will be used.
	rules []byte
	// Maximum WAF execution time
	wafTimeout time.Duration
}

// isEnabled returns true when appsec is enabled when the environment variable
// DD_APPSEC_ENABLED is set to true.
func isEnabled() (bool, error) {
	enabledStr := os.Getenv(enabledEnvVar)
	if enabledStr == "" {
		return false, nil
	}
	enabled, err := strconv.ParseBool(enabledStr)
	if err != nil {
		return false, fmt.Errorf("could not parse %s value `%s` as a boolean value", enabledEnvVar, enabledStr)
	}
	return enabled, nil
}

func newConfig() (*config, error) {
	cfg := &config{}

	filepath := os.Getenv(rulesEnvVar)
	if filepath != "" {
		rules, err := ioutil.ReadFile(filepath)
		if err != nil {
			if os.IsNotExist(err) {
				log.Error("appsec: could not find the rules file in path %s: %v.", filepath, err)
			}
			return nil, err
		}
		cfg.rules = rules
		log.Info("appsec: starting with the security rules from file %s", filepath)
	} else {
		log.Info("appsec: starting with the default recommended security rules")
		cfg.rules = []byte(staticRecommendedRule)
	}

	cfg.wafTimeout = defaultWAFTimeout
	if wafTimeout := os.Getenv(wafTimeoutEnvVar); wafTimeout != "" {
		if timeout, err := time.ParseDuration(wafTimeout); err == nil {
			if timeout <= 0 {
				log.Error("appsec: unexpected configuration value of %s=%s: expecting a strictly positive duration. Using default value %s.", wafTimeoutEnvVar, wafTimeout, cfg.wafTimeout)
			} else {
				cfg.wafTimeout = timeout
			}
		} else {
			log.Error("appsec: could not parse the value of %s %s as a duration: %v. Using default value %s.", wafTimeoutEnvVar, wafTimeout, err, cfg.wafTimeout)
		}
	}

	return cfg, nil
}
