/* Copyright © 2022-2024 Bopmatic, LLC. All Rights Reserved.
 *
 * See LICENSE file at the root of this package for license terms
 */
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	_ "embed"

	bopsdk "github.com/bopmatic/sdk/golang"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
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

func login(ctx context.Context) (bopsdk.DeployOption, error) {
	const clientId = "79qsr4af7jrrsm8f6lfi12aqlv"
	const region = "us-east-2"

	fmt.Printf("Bopmatic username: ")
	var username string
	fmt.Scanf("%s", &username)
	username = strings.TrimSpace(username)
	fmt.Printf("         password: ")
	var passwd string
	fmt.Scanf("%s", &passwd)

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}

	cip := cognitoidentityprovider.NewFromConfig(cfg)
	input := &cognitoidentityprovider.InitiateAuthInput{
		AuthFlow: "USER_PASSWORD_AUTH",
		AuthParameters: map[string]string{
			"USERNAME": username,
			"PASSWORD": passwd,
		},
		ClientId: aws.String(clientId),
	}

	result, err := cip.InitiateAuth(ctx, input)
	if err != nil {
		return nil, err
	}

	return bopsdk.DeployOptBearerToken(*result.AuthenticationResult.AccessToken), nil
}

func getNewApiKey() (string, error) {
	sdkOpts := make([]bopsdk.DeployOption, 0)

	httpClient, err := getHttpClientFromCreds()
	if err != nil {
		return "", err
	}
	sdkOpts = append(sdkOpts, bopsdk.DeployOptHttpClient(httpClient))
	bearerOpt, err := login(context.Background())
	if err != nil {
		return "", err
	}
	sdkOpts = append(sdkOpts, bearerOpt)
	apiKeyResp, err := bopsdk.CreateApiKey("bopmatic_cli_key",
		"api key for bopmatic cli", time.Unix(0, 0).UTC(), sdkOpts...)
	if err != nil {
		return "", err
	}

	fmt.Fprintf(os.Stderr, "Created new api key %v\n", apiKeyResp.KeyId)

	return string(apiKeyResp.KeyData), nil
}
