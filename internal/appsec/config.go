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
	enabledStr := os.Getenv("DD_APPSEC_ENABLED")
	if enabledStr == "" {
		return false, nil
	}
	enabled, err := strconv.ParseBool(enabledStr)
	if err != nil {
		return false, fmt.Errorf("could not parse DD_APPSEC_ENABLED value `%s` as a boolean value", enabledStr)
	}
	return enabled, nil
}

func newConfig() (*config, error) {
	cfg := &config{}

	filepath := os.Getenv("DD_APPSEC_RULES")
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
	}

	cfg.wafTimeout = 4 * time.Millisecond
	if wafTimeout := os.Getenv("DD_APPSEC_WAF_TIMEOUT"); wafTimeout != "" {
		timeout, err := time.ParseDuration(wafTimeout)
		if err != nil {
			cfg.wafTimeout = timeout
		} else {
			log.Error("appsec: could not parse the value of DD_APPSEC_WAF_TIMEOUT %s as a duration: %v. Using default value %s.", wafTimeout, err, cfg.wafTimeout)
		}
	}

	return cfg, nil
}
