// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package osinfo

import (
	"encoding/xml"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func productVersion() (string, error) {
	if compat, err := unix.SysctlUint32("kern.system_version_compat"); err == nil && compat == 0 {
		return unix.Sysctl("kern.osproductversion")
	}

	// In compatibility mode both the sysctl and SystemVersion.plist can report
	// 10.16. Apple provides this symlink to the actual system version file.
	f, err := os.Open("/System/Library/CoreServices/.SystemVersionPlatform.plist")
	if err != nil {
		return "", err
	}
	defer f.Close()

	var plist struct {
		Dict struct {
			Entries []struct {
				XMLName xml.Name
				Text    string `xml:",chardata"`
			} `xml:",any"`
		} `xml:"dict"`
	}
	if err := xml.NewDecoder(f).Decode(&plist); err != nil {
		return "", err
	}
	entries := plist.Dict.Entries
	for i := 0; i+1 < len(entries); i += 2 {
		key, value := entries[i], entries[i+1]
		if key.XMLName.Local == "key" && key.Text == "ProductVersion" && value.XMLName.Local == "string" {
			return value.Text, nil
		}
	}
	return "", errors.New("ProductVersion missing from system version file")
}
