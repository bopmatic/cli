/* Copyright © 2022-2024 Bopmatic, LLC. All Rights Reserved.
 *
 * See LICENSE file at the root of this package for license terms
 */
package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	_ "embed"

	bopsdk "github.com/bopmatic/sdk/golang"
)

//go:embed truststore.pem
var bopmaticCaCert []byte

func getHttpClientFromCreds() (*http.Client, error) {
	certPath, err := getConfigCertPath()
	if err != nil {
		return nil, err
	}
	keyPath, err := getConfigKeyPath()
	if err != nil {
		return nil, err
	}
	caCertPool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("Failed to get system cert pool: %w", err)
	}
	caCertPool.AppendCertsFromPEM(bopmaticCaCert)

	clientCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to read user keypair: %w", err)
	}

	return &http.Client{
		Timeout: time.Second * 30,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:      caCertPool,
				Certificates: []tls.Certificate{clientCert},
			},
		},
	}, nil
}

func getApiKey() (string, error) {
	keyPath, err := getConfigApiKeyPath()
	if err != nil {
		return "", err
	}
	apiKey, err := ioutil.ReadFile(keyPath)
	if err != nil {
		return "", err
	}

	return string(apiKey), nil
}

func getAuthSdkOpts() ([]bopsdk.DeployOption, error) {
	opts := make([]bopsdk.DeployOption, 0)

	httpClient, err := getHttpClientFromCreds()
	if err != nil {
		return opts, err
	}
	opts = append(opts, bopsdk.DeployOptHttpClient(httpClient))

	apiKey, err := getApiKey()
	if err != nil {
		// treat as warning for now
		fmt.Fprintf(os.Stderr,
			"Warning: no api key is set; please run 'bopmatic config'\n")
	} else {
		opts = append(opts, bopsdk.DeployOptApiKey(apiKey))
	}

	return opts, nil
}
