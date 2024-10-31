/* Copyright © 2022-2024 Bopmatic, LLC. All Rights Reserved.
 *
 * See LICENSE file at the root of this package for license terms
 */
package main

import (
	"fmt"
	"os"
	"path/filepath"

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
	certPath := filepath.Join(configPath, "user.cert.pem")
	keyPath := filepath.Join(configPath, "user.key.pem")

	for _, f := range []string{certPath, keyPath} {
		_, err = os.Stat(f)
		if os.IsNotExist(err) {
			haveExisting = false
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "Could not read %v: %v", f, err)
		}
	}

	if haveExisting {
		fmt.Printf("Your user cert & key are already installed\n")
	} else {
		var downloadedCertPath string
		var downloadedKeyPath string

		fmt.Printf("Enter your user certficate filename: ")
		fmt.Scanf("%s", &downloadedCertPath)
		fmt.Printf("Enter your user key filename: ")
		fmt.Scanf("%s", &downloadedKeyPath)

		err = util.CopyFile(downloadedCertPath, certPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not install cert: %v\n", err)
			os.Exit(1)
		}
		err = util.CopyFile(downloadedKeyPath, keyPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not install key: %v\n", err)
			os.Exit(1)
		}
	}

	upgradeBuildContainer([]string{})
}
