/* Copyright © 2022-2024 Bopmatic, LLC. All Rights Reserved.
 *
 * See LICENSE file at the root of this package for license terms
 */
package main

import (
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	_ "embed"

	bopsdk "github.com/bopmatic/sdk/golang"
	"github.com/bopmatic/sdk/golang/pb"
)

type commonOpts struct {
	projectFilename string
	projectId       string
	packageId       string
	deployId        string
	serviceName     string
	startTime       string
	endTime         string
}

var subCommandTab = map[string]func(args []string){
	"project": projMain,
	"package": pkgMain,
	"deploy":  deployMain,
	"help":    helpMain,
	"config":  configMain,
	"version": versionMain,
	"upgrade": upgradeMain,
	"logs":    logsMain,
}

const (
	ExamplesDir          = "/bopmatic/examples"
	DefaultTemplate      = "golang/helloworld"
	ClientTemplateSubdir = "client"
	SiteAssetsSubdir     = "site_assets"
	// update Makefile brewversion target if changing this value
	BrewVersionSuffix = "b"
)

func printExampleCurl(descReply *pb.DescribePackageReply) {
	// @todo re-implement w/ ListServices() && DescribeService()
}

func unixTime2Local(secs uint64) time.Time {
	return time.Unix(int64(secs), 0)
}

func unixTime2Utc(secs uint64) time.Time {
	return unixTime2Local(secs).UTC()
}

//go:embed help.txt
var helpText string

func helpMain(args []string) {
	fmt.Printf(helpText)
}

func setCommonFlags(f *flag.FlagSet, o *commonOpts) {
	f.StringVar(&o.projectFilename, "projfile", bopsdk.DefaultProjectFilename,
		"Bopmatic project filename")
	f.StringVar(&o.projectId, "projid", "", "Bopmatic project id")
	f.StringVar(&o.packageId, "pkgid", "",
		"Bopmatic project package identifier")
	f.StringVar(&o.deployId, "deployid", "",
		"Bopmatic deployment identifier")
	f.StringVar(&o.serviceName, "svcname", "",
		"Name of a service within your Bopmatic project")
	f.StringVar(&o.startTime, "starttime", "",
		"The starting time in UTC to query; defaults to 48 hours ago.")
	f.StringVar(&o.endTime, "endtime", "",
		"The ending time in UTC to query; defaults to now.")
}

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

func checkAndPrintArchWarning() bool {
	if runtime.GOARCH != "amd64" {
		if runtime.GOOS == "darwin" {
			fmt.Fprintf(os.Stderr, "*WARN*: bopmatic's build container is known not to run well on M1 based Macs; please try on a 64-bit Intel/AMD based system if possible.\n")
		} else {
			fmt.Fprintf(os.Stderr, "*WARN*: bopmatic's build container has not been tested on your CPU (%v); please try on a 64-bit Intel/AMD based system if possible.\n",
				runtime.GOARCH)
		}
		return true
	}

	return false
}

func main() {
	versionText = strings.Split(versionText, "\n")[0]
	exitStatus := 0

	printedUpgradeCLIWarning := checkAndPrintUpgradeCLIWarning()
	printedUpgradeContainerWarning := checkAndPrintUpgradeContainerWarning()
	printedArchWarning := checkAndPrintArchWarning()
	if printedUpgradeCLIWarning || printedUpgradeContainerWarning || printedArchWarning {
		fmt.Fprintf(os.Stderr, "\n")
	}

	subCommandName := "help"
	if len(os.Args) > 1 {
		subCommandName = os.Args[1]
	} else {
		exitStatus = 1
	}

	subCommand, ok := subCommandTab[subCommandName]
	if !ok {
		subCommand = helpMain
		exitStatus = 1
	}

	var args []string
	if len(os.Args) > 2 {
		args = os.Args[2:]
	}

	subCommand(args)

	os.Exit(exitStatus)
}
