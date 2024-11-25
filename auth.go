/* Copyright © 2022-2024 Bopmatic, LLC. All Rights Reserved.
 *
 * See LICENSE file at the root of this package for license terms
 */
package main

import (
	"context"
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

	httpClient := &http.Client{
		Timeout: time.Second * 30,
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

func getHostName() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "localhost"
	}

	return hostname
}

func getNewApiKey() (string, error) {
	sdkOpts := make([]bopsdk.DeployOption, 0)

	httpClient := &http.Client{
		Timeout: time.Second * 30,
	}
	sdkOpts = append(sdkOpts, bopsdk.DeployOptHttpClient(httpClient))

	var sb strings.Builder
	sb.WriteString("How would you like to setup your api key?\n")
	sb.WriteString("1. Paste key data from one you already created at https://console.bopmatic.com/api-keys\n")
	sb.WriteString("2. Login here with your Bopmatic user/password and have bopmatic CLI create one for you\n")
	sb.WriteString("3. I don't have an account with Bopmatic yet and would like to request access\n")
	sb.WriteString("Answer (1, 2, or 3) [1]: ")
	fmt.Printf("%v", sb.String())
	var answer string
	fmt.Scanf("%s", &answer)
	answer = strings.TrimSpace(answer)
	if answer == "" {
		answer = "1"
	}

	switch answer {
	case "1":
		return getKeyDataViaUser()
	case "2":
		bearerOpt, err := login(context.Background())
		if err != nil {
			return "", err
		}
		sdkOpts = append(sdkOpts, bearerOpt)
		apiKeyResp, err := bopsdk.CreateApiKey(
			fmt.Sprintf("%v_cli_key", getHostName()),
			fmt.Sprintf("api key for bopmatic cli on %v", getHostName()),
			time.Unix(0, 0).UTC(), sdkOpts...)
		if err != nil {
			return "", err
		}

		fmt.Fprintf(os.Stderr, "Created new api key %v\n", apiKeyResp.KeyId)

		return string(apiKeyResp.KeyData), nil
	case "3":
		return "", requestAccess()
	default:
	}

	return "", fmt.Errorf("Invalid response; please enter 1, 2, or 3")
}

func getKeyDataViaUser() (string, error) {
	var keyData string
	fmt.Printf("Paste the key data you copied to the clipboard and press enter:	")
	fmt.Scanf("%s", &keyData)
	keyData = strings.TrimSpace(keyData)
	if len(keyData) == 0 || keyData[len(keyData)-1] != '=' {
		return "", fmt.Errorf("invalid key data")
	}

	return keyData, nil
}

func requestAccess() error {
	type promptEntry struct {
		key   string
		value *string
	}

	var firstName, lastName, email, userName string
	prompts := []promptEntry{
		{key: "First Name", value: &firstName},
		{key: "Last Name", value: &lastName},
		{key: "Email address", value: &email},
		{key: "Desired Bopmatic username", value: &userName},
	}

	for _, p := range prompts {
		for {
			fmt.Printf("%v: ", p.key)
			fmt.Scanf("%s", p.value)
			*p.value = strings.TrimSpace(*p.value)
			if len(*p.value) > 0 {
				break
			}
		}
	}
	for _, p := range prompts {
		fmt.Fprintf(os.Stderr, "%v: %v\n", p.key, *p.value)
	}

	httpClient := &http.Client{
		Timeout: time.Second * 30,
	}
	err := bopsdk.RequestAccess(userName, firstName, lastName, email, "", "",
		bopsdk.DeployOptHttpClient(httpClient))
	if err == nil {
		err = fmt.Errorf("A request for an account on bopmatic.com was submitted on your behalf. A representative will respond to you shortly via email.")
	}

	return err
}
