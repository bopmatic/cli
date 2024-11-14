/* Copyright © 2022-2024 Bopmatic, LLC. All Rights Reserved.
 *
 * See LICENSE file at the root of this package for license terms
 */
package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	_ "embed"
)

func getConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("Could not find user home directory: %w", err)
	}

	return filepath.Join(homeDir, ".config", "bopmatic"), nil
}

func getConfigApiKeyPath() (string, error) {
	configPath, err := getConfigPath()
	if err != nil {
		return "", err
	}

	return filepath.Join(configPath, "apikey"), nil
}

func configMain(args []string) {
	configPath, err := getConfigPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	err = os.MkdirAll(configPath, 0700)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not create config directory %v: %v\n",
			configPath, err)
		os.Exit(1)
	}

	haveExisting := true
	apiKeyPath, _ := getConfigApiKeyPath()

	_, err = os.Stat(apiKeyPath)
	if os.IsNotExist(err) {
		haveExisting = false
	} else if err != nil {
		fmt.Fprintf(os.Stderr, "Could not read %v: %v", apiKeyPath, err)
		os.Exit(1)
	}

	shouldReplace := "N"
	if haveExisting {
		fmt.Printf("Your %v is already installed; replace? (Y/N) [N]: ",
			apiKeyPath)
		fmt.Scanf("%s", &shouldReplace)
		shouldReplace = strings.ToUpper(shouldReplace)
		shouldReplace = strings.TrimSpace(shouldReplace)
	} else {
		shouldReplace = "Y"
	}
	if len(shouldReplace) > 0 && shouldReplace[0] == 'Y' {
		apiKeyVal := ""
		apiKeyVal, err = getNewApiKey()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create new api key: %v\n", err)
			os.Exit(1)
		}
		_ = os.Remove(apiKeyPath)
		err = ioutil.WriteFile(apiKeyPath, []byte(apiKeyVal), 0400)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not install %v: %v\n", apiKeyPath,
				err)
			os.Exit(1)
		}
	}

	upgradeBuildContainer([]string{})
}
