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

	"github.com/bopmatic/sdk/golang/util"
)

func getConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("Could not find user home directory: %w", err)
	}

	return filepath.Join(homeDir, ".config", "bopmatic"), nil
}

func getConfigCertPath() (string, error) {
	configPath, err := getConfigPath()
	if err != nil {
		return "", err
	}

	return filepath.Join(configPath, "user.cert.pem"), nil
}

func getConfigKeyPath() (string, error) {
	configPath, err := getConfigPath()
	if err != nil {
		return "", err
	}

	return filepath.Join(configPath, "user.key.pem"), nil
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
	certPath, _ := getConfigCertPath()
	keyPath, _ := getConfigKeyPath()
	apiKeyPath, _ := getConfigApiKeyPath()

	for _, f := range []string{certPath, keyPath, apiKeyPath} {
		_, err = os.Stat(f)
		if os.IsNotExist(err) {
			haveExisting = false
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "Could not read %v: %v", f, err)
			os.Exit(1)
		}

		shouldReplace := "N"
		if haveExisting {
			fmt.Printf("Your %v is already installed; replace? (Y/N) [N]: ", f)
			fmt.Scanf("%s", &shouldReplace)
			shouldReplace = strings.ToUpper(shouldReplace)
			shouldReplace = strings.TrimSpace(shouldReplace)
		} else {
			shouldReplace = "Y"
		}
		if len(shouldReplace) == 0 || shouldReplace[0] != 'Y' {
			continue
		}
		downloadPath := ""
		apiKeyVal := ""
		dstPath := ""
		if f == certPath {
			fmt.Printf("Enter your user certficate filename: ")
			fmt.Scanf("%s", &downloadPath)
			dstPath = certPath
		} else if f == keyPath {
			fmt.Printf("Enter your user key filename: ")
			fmt.Scanf("%s", &downloadPath)
			dstPath = keyPath
		} else {
			apiKeyVal, err = getNewApiKey()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to create new api key: %v\n", err)
				os.Exit(1)
			}
			dstPath = apiKeyPath
		}

		if downloadPath != "" {
			err = util.CopyFile(downloadPath, dstPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Could not install %v: %v\n", dstPath, err)
				os.Exit(1)
			}
		} else {
			_ = os.Remove(dstPath)
			err = ioutil.WriteFile(dstPath, []byte(apiKeyVal), 0400)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Could not install %v: %v\n", dstPath, err)
				os.Exit(1)
			}
		}
	}

	for _, f := range []string{certPath, keyPath, apiKeyPath} {
		_, err = os.Stat(f)
		if os.IsNotExist(err) {
			haveExisting = false
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "Could not read %v: %v", f, err)
		}
	}

	upgradeBuildContainer([]string{})
}
