/* Copyright © 2022-2024 Bopmatic, LLC. All Rights Reserved.
 *
 * See LICENSE file at the root of this package for license terms
 */
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"io/ioutil"
	"net/http"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	_ "embed"

	"github.com/araddon/dateparse"

	"github.com/docker/docker/api/types/image"
	dockerClient "github.com/docker/docker/client"

	bopsdk "github.com/bopmatic/sdk/golang"
	"github.com/bopmatic/sdk/golang/pb"
	"github.com/bopmatic/sdk/golang/util"
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

var pkgSubCommandTab = map[string]func(args []string){
	"build":    pkgBuildMain,
	"deploy":   pkgDeployMain,
	"list":     pkgListMain,
	"delete":   pkgDeleteMain,
	"describe": pkgDescribeMain,
	"help":     pkgHelpMain,
}

var deploySubCommandTab = map[string]func(args []string){
	"list":     deployListMain,
	"describe": deployDescribeMain,
	"help":     deployHelpMain,
}

var projSubCommandTab = map[string]func(args []string){
	"create":     projCreateMain,
	"destroy":    projDestroyMain,
	"deactivate": projDeactivateMain,
	"list":       projListMain,
	"help":       projHelpMain,
	"describe":   projDescribeMain,
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

func pkgMain(args []string) {
	exitStatus := 0

	pkgSubCommandName := "help"
	if len(args) == 0 {
		exitStatus = 1
	} else {
		pkgSubCommandName = args[0]
	}

	pkgSubCommand, ok := pkgSubCommandTab[pkgSubCommandName]
	if !ok {
		exitStatus = 1
		pkgSubCommand = pkgHelpMain
	}

	if len(args) > 0 {
		args = args[1:]
	}

	pkgSubCommand(args)

	os.Exit(exitStatus)
}

func pkgBuildMain(args []string) {
	type buildOpts struct {
		common commonOpts
	}

	var opts buildOpts

	f := flag.NewFlagSet("bopmatic package build", flag.ExitOnError)
	setCommonFlags(f, &opts.common)

	err := f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	proj, err := bopsdk.NewProject(opts.common.projectFilename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if proj.Desc.BuildCmd == "" {
		fmt.Printf("Project %v is a static site only; no build required\n",
			proj.Desc.Name)
		os.Exit(0)
	}

	err = proj.Build(os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to build %v: %v\n", proj.Desc.Name, err)
		os.Exit(1)
	}

	err = proj.RemoveStalePackages()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to remove stale packages: %v\n", err)
		os.Exit(1)
	}

	pkg, err := proj.NewPackageCreate("", os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to package %v: %v\n", proj.Desc.Name, err)
		os.Exit(1)
	}

	fmt.Printf("Successfully built pkgId:%v (%v)\n", pkg.Id,
		pkg.AbsTarballPath())
	fmt.Printf("To deploy your package, next run:\n\t'bopmatic package deploy'\n")
}

func pkgDeployMain(args []string) {
	httpClient, err := getHttpClientFromCreds()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	type deployOpts struct {
		common commonOpts
	}

	var opts deployOpts

	f := flag.NewFlagSet("bopmatic package deploy", flag.ExitOnError)
	setCommonFlags(f, &opts.common)

	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	proj, err := bopsdk.NewProject(opts.common.projectFilename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	pkg, err := proj.NewPackageExisting("")
	if err != nil {
		_ = proj.RemoveStalePackages()

		pkg, err = proj.NewPackageCreate("", os.Stdout, os.Stderr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to package %v: %v\n", proj.Desc.Name, err)
			os.Exit(1)
		}
	}

	validateNoConflicts(httpClient, pkg)

	fmt.Printf("Deploying pkgId:%v (%v)...", pkg.Id, pkg.AbsTarballPath())
	// @todo specify envId
	deployId, err := pkg.Deploy("", bopsdk.DeployOptHttpClient(httpClient))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Started\nDeploying takes about 10 minutes. You can check deploy progress with:\n\t'bopmatic deploy describe --deployid %v'\n",
		deployId)
}

func validateNoConflicts(httpClient *http.Client, pkg *bopsdk.Package) {
	// @todo for UX purposes consider evaluating conflicts client-side here
	// rather than just relying on server-side conflict checks
}

func pkgListMain(args []string) {
	httpClient, err := getHttpClientFromCreds()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	type listOpts struct {
		common commonOpts
	}

	var opts listOpts

	f := flag.NewFlagSet("bopmatic package list", flag.ExitOnError)
	setCommonFlags(f, &opts.common)

	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if opts.common.projectId == "" {
		proj, err := bopsdk.NewProject(opts.common.projectFilename)
		if err == nil {
			opts.common.projectId = proj.Desc.Id
		}
	}

	// @todo project filter not yet implemented
	//if opts.common.projectName == "" {
	fmt.Printf("Listing packages for all projects...")
	//} else {
	//	fmt.Printf("Listing packages for project %v...",
	//		opts.common.projectName)
	//}

	pkgs, err := bopsdk.ListPackages(opts.common.projectId,
		bopsdk.DeployOptHttpClient(httpClient))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if len(pkgs) == 0 {
		fmt.Printf("\nNo currently deployed packages\n")
	} else {
		fmt.Printf("\nProject\t\tPackageId\n")

		for _, pkg := range pkgs {
			fmt.Printf("%v\t\t%v\n", pkg.ProjId, pkg.PackageId)
		}
	}
}

//go:embed pkgHelp.txt
var pkgHelpText string

func pkgHelpMain(args []string) {
	fmt.Printf(pkgHelpText)
}

//go:embed deployHelp.txt
var deployHelpText string

func deployHelpMain(args []string) {
	fmt.Printf(deployHelpText)
}

func pkgDescribeMain(args []string) {
	httpClient, err := getHttpClientFromCreds()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	type describeOpts struct {
		common commonOpts
	}

	var opts describeOpts

	f := flag.NewFlagSet("bopmatic package describe", flag.ExitOnError)
	setCommonFlags(f, &opts.common)

	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if opts.common.packageId == "" {
		fmt.Fprintf(os.Stderr, "Please specify package id with --pkgid. If you don't know this, try 'bopmatic package list'\n")
		os.Exit(1)
	}

	fmt.Printf("Describing pkgId:%v...", opts.common.packageId)
	pkgDesc, err := bopsdk.Describe(opts.common.packageId,
		bopsdk.DeployOptHttpClient(httpClient))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nProject:%v PackageId:%v State:%v Size:%v UploadTime:%v\n",
		pkgDesc.ProjId, pkgDesc.PackageId, pkgDesc.State, pkgDesc.PackageSize,
		pkgDesc.UploadTime)

	switch pkgDesc.State {
	case pb.PackageState_UPLOADING:
		fmt.Printf("\nYour project is being uploaded to Bopmatic ServiceRunner\n")
	case pb.PackageState_UPLOADED:
		fmt.Printf("\nYour project package was uploaded Bopmatic ServiceRunner and will next be validated\n")
	case pb.PackageState_PKG_VALIDATING:
		fmt.Printf("\nBopmatic ServiceRunner is validating your project package\n")
	case pb.PackageState_INVALID:
		fmt.Printf("\nSomething is wrong with your project package and it cannot be deployed. Please delete it with:\n\t'bopmatic destroy --pkgid %v\n",
			pkgDesc.PackageId)
	case pb.PackageState_PKG_BUILDING:
		fmt.Printf("\nBopmatic ServiceRunner is building infrastructure for your project package\n")
	case pb.PackageState_BUILT:
		fmt.Printf("\nBopmatic ServiceRunner has built your project.\n\n")
	case pb.PackageState_PKG_DELETED:
		fmt.Printf("\nBopmatic ServiceRunner has deleted your project package\n")
	case pb.PackageState_PKG_SUPPORT_NEEDED:
		fallthrough
	case pb.PackageState_UNKNOWN_PKG_STATE:
		fallthrough
	default:
		fmt.Printf("\nAn error occurred within Bopmatic ServiceRunner and a support staff member needs to examine the situation.\n")
	}
}

func pkgDeleteMain(args []string) {
	httpClient, err := getHttpClientFromCreds()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	type deleteOpts struct {
		common commonOpts
	}

	var opts deleteOpts

	f := flag.NewFlagSet("bopmatic package delete", flag.ExitOnError)
	setCommonFlags(f, &opts.common)

	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if opts.common.packageId == "" {
		fmt.Fprintf(os.Stderr, "Please specify package id with --pkgid. If you don't know this, try 'bopmatic package list'\n")
		os.Exit(1)
	}

	fmt.Printf("Listing packages...")
	pkgs, err := bopsdk.ListPackages(opts.common.projectId,
		bopsdk.DeployOptHttpClient(httpClient))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	found := false
	for _, pkg := range pkgs {
		if pkg.PackageId == opts.common.packageId {
			found = true
		}
	}

	if !found {
		fmt.Printf("\nPackage id %v no longer exists\n", opts.common.packageId)
		os.Exit(1)
	}

	fmt.Printf("Deleting pkgId:%v...", opts.common.packageId)
	err = bopsdk.DeletePackage(opts.common.packageId,
		bopsdk.DeployOptHttpClient(httpClient))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nDeleted pkgId:%v", opts.common.packageId)
}

func printExampleCurl(descReply *pb.DescribePackageReply) {
	// @todo re-implement w/ ListServices() && DescribeService()
}

func projDescribeMain(args []string) {
	httpClient, err := getHttpClientFromCreds()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	type describeOpts struct {
		common commonOpts
	}

	var opts describeOpts

	f := flag.NewFlagSet("bopmatic project describe", flag.ExitOnError)
	setCommonFlags(f, &opts.common)
	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if opts.common.projectId == "" {
		proj, err := bopsdk.NewProject(opts.common.projectFilename)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				fmt.Fprintf(os.Stderr, "Could not find project. Please specify --projid, --projfile, run from within a Bopmatic project directory.\n")
			} else {
				fmt.Fprintf(os.Stderr, "%v\n", err)
			}
			os.Exit(1)
		}
		opts.common.projectId = proj.Desc.Id
	}

	projDesc, err := bopsdk.DescribeProject(opts.common.projectId,
		bopsdk.DeployOptHttpClient(httpClient))

	fmt.Printf("Name: %v\n", projDesc.Header.Name)
	fmt.Printf("Id: %v\n", projDesc.Id)
	fmt.Printf("DnsPrefix: %v\n", projDesc.Header.DnsPrefix)
	fmt.Printf("DnsDomain: %v\n", projDesc.Header.DnsDomain)
	fmt.Printf("Created: %v\n", projDesc.CreateTime)
	fmt.Printf("State: %v\n", projDesc.State)
	fmt.Printf("Active deployments: %v\n", projDesc.ActiveDeployIds)
	fmt.Printf("Pending deployments: %v\n", projDesc.PendingDeployIds)
}

//go:embed logsHelp.txt
var logsHelpText string

func logsMain(args []string) {
	httpClient, err := getHttpClientFromCreds()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	type logsOpts struct {
		common commonOpts
	}

	var opts logsOpts

	f := flag.NewFlagSet("bopmatic logs", flag.ExitOnError)
	setCommonFlags(f, &opts.common)
	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		fmt.Fprintf(os.Stderr, "%v\n", logsHelpText)
		os.Exit(1)
	}

	projId := opts.common.projectId
	var proj *bopsdk.Project
	if projId == "" {
		proj, err = bopsdk.NewProject(opts.common.projectFilename)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				fmt.Fprintf(os.Stderr, "Please specify --projid or run from within a Bopmatic project directory.\n")
				fmt.Fprintf(os.Stderr, "%v\n", logsHelpText)
			} else {
				fmt.Fprintf(os.Stderr, "%v\n", err)
			}
			os.Exit(1)
		}
		projId = proj.Desc.Id
	}
	svcName := opts.common.serviceName
	if svcName == "" {
		if proj != nil {
			if len(proj.Desc.Services) == 1 {
				svcName = proj.Desc.Services[0].Name
			} else {
				svcList := make([]string, 0)
				for _, svc := range proj.Desc.Services {
					svcList = append(svcList, svc.Name)
				}

				fmt.Fprintf(os.Stderr, "Please specify --svcname. Project %v currently has %v services: %v\n",
					projId, len(svcList), svcList)
				os.Exit(1)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Please specify --svcname.")
			os.Exit(1)
		}
	}

	var startTime, endTime time.Time
	const DefaultLogWindow = 48 * time.Hour
	if opts.common.startTime == "" {
		startTime = time.Now().UTC().Add(-DefaultLogWindow)
	} else {
		startTime, err = dateparse.ParseAny(opts.common.startTime)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not parse start time(%v): %v\n",
				opts.common.startTime, err)
			os.Exit(1)
		}
	}
	if opts.common.endTime == "" {
		endTime = time.Now().UTC()
	} else {
		endTime, err = dateparse.ParseAny(opts.common.endTime)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not parse end time(%v): %v\n",
				opts.common.endTime, err)
			os.Exit(1)
		}
	}
	if !endTime.After(startTime) {
		fmt.Fprintf(os.Stderr, "End time(%v) <= start time(%v). Please specify an end time that occurs later than start time.\n",
			endTime, startTime)
		os.Exit(1)
	}

	// @todo specify environment id
	err = bopsdk.GetLogs(projId, "", svcName, startTime, endTime,
		bopsdk.GetLogsOptHttpClient(httpClient))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

//go:embed help.txt
var helpText string

func helpMain(args []string) {
	fmt.Printf(helpText)
}

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

func getLatestVersion() (string, error) {
	const LatestReleaseUrl = "https://api.github.com/repos/bopmatic/cli/releases/latest"

	client := http.Client{
		Timeout: time.Second * 30,
	}

	resp, err := client.Get(LatestReleaseUrl)
	if err != nil {
		return "", err
	}

	releaseJsonDoc, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var releaseDoc map[string]any
	err = json.Unmarshal(releaseJsonDoc, &releaseDoc)
	if err != nil {
		return "", err
	}

	latestRelease, ok := releaseDoc["tag_name"].(string)
	if !ok {
		return "", fmt.Errorf("Could not parse %v", LatestReleaseUrl)
	}

	if isBrewVersion() {
		latestRelease += BrewVersionSuffix
	}
	return latestRelease, nil
}

func upgradeMain(args []string) {
	upgradeBuildContainer(args)
	upgradeCLI(args)
}

func upgradeCLI(args []string) {
	if versionText == DevVersionText {
		fmt.Fprintf(os.Stderr, "Skipping CLI upgrade on development version\n")
		return
	}
	latestVer, err := getLatestVersion()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not determine latest version: %v\n", err)
		os.Exit(1)
	}
	if latestVer == versionText {
		fmt.Printf("Bopmatic CLI %v is already the latest version\n",
			versionText)
		return
	}

	fmt.Printf("A new version of the Bopmatic CLI is available (%v). Upgrade? (Y/N) [Y]: ",
		latestVer)
	shouldUpgrade := "Y"
	fmt.Scanf("%s", &shouldUpgrade)
	shouldUpgrade = strings.ToUpper(strings.TrimSpace(shouldUpgrade))

	if shouldUpgrade[0] != 'Y' {
		return
	}

	fmt.Printf("Upgrading bopmatic cli from %v to %v...\n", versionText,
		latestVer)

	if isBrewVersion() {
		upgradeCLIViaBrew()
	} else {
		upgradeCLIViaGithub(latestVer)
	}
}

func upgradeBuildContainer(args []string) {
	haveBuildImg, err := util.HasBopmaticBuildImage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if haveBuildImg {
		needUpgrade, err :=
			util.DoesLocalImageNeedUpdate(util.BopmaticImageRepo,
				util.BopmaticImageTag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		if needUpgrade == false {
			fmt.Printf("Bopmatic Build container is up to date\n")
			return
		}

		fmt.Printf("Update Bopmatic Build Image? (Y/N) [Y]: ")
	} else {
		fmt.Printf("Bopmatic needs to download the Bopmatic Build Image in order to build projects. It is roughly 975MiB(compressed) in size.\n")
		fmt.Printf("Download Bopmatic Build Image? (Y/N) [Y]: ")
	}
	shouldDownload := "Y"
	fmt.Scanf("%s", &shouldDownload)
	shouldDownload = strings.TrimSpace(shouldDownload)

	if strings.ToUpper(shouldDownload)[0] == 'Y' {
		pullBopmaticImage()

		if !haveBuildImg {
			fmt.Printf("To create a bopmatic project, next run:\n\t'bopmatic new'\n")
		}
	}
}

func upgradeCLIViaBrew() {
	ctx := context.Background()
	err := util.RunHostCommand(ctx, []string{"brew", "update"}, os.Stdout,
		os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to update brew formulae: %v\n", err)
		os.Exit(1)
	}
	err = util.RunHostCommand(ctx, []string{"brew", "install",
		"bopmatic/macos/cli"}, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to upgrade bopmatic: %v\n", err)
		os.Exit(1)
	}
}

func upgradeCLIViaGithub(latestVer string) {
	const LatestDownloadFmt = "https://github.com/bopmatic/cli/releases/download/%v/bopmatic"

	client := http.Client{
		Timeout: time.Second * 30,
	}

	resp, err := client.Get(fmt.Sprintf(LatestDownloadFmt, latestVer))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to download version %v: %v\n",
			versionText, err)
		os.Exit(1)
	}

	tmpFile, err := os.CreateTemp("", "bopmatic-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create temp file: %v", err)
	}
	binaryContent, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to download version %v: %v\n",
			versionText, err)
		os.Exit(1)
	}

	_, err = tmpFile.Write(binaryContent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to download version %v: %v\n",
			versionText, err)
		os.Exit(1)
	}
	err = tmpFile.Chmod(0755)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to download version %v: %v\n",
			versionText, err)
		os.Exit(1)
	}
	err = tmpFile.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to download version %v: %v\n",
			versionText, err)
		os.Exit(1)
	}
	myBinaryPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not determine path to bopmatic CLI: %v\n",
			err)
		os.Exit(1)
	}
	myBinaryPath, err = filepath.EvalSymlinks(myBinaryPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not determine path to bopmatic CLI: %v\n",
			err)
		os.Exit(1)
	}

	myBinaryPathBak := myBinaryPath + ".bak"
	err = os.Rename(myBinaryPath, myBinaryPathBak)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not replace existing %v; do you need to be root?: %v\n",
			myBinaryPath, err)
		os.Exit(1)
	}
	err = os.Rename(tmpFile.Name(), myBinaryPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not replace existing %v; do you need to be root?: %v\n",
			myBinaryPath, err)
		_ = os.Rename(myBinaryPathBak, myBinaryPath)
		os.Exit(1)
	}
	_ = os.Remove(myBinaryPathBak)

	fmt.Printf("Upgrade %v to %v complete\n", myBinaryPath, latestVer)
}

func pullBopmaticImage() {
	cli, err := dockerClient.NewClientWithOpts(dockerClient.FromEnv,

		dockerClient.WithAPIVersionNegotiation())
	if err != nil {
		err := fmt.Errorf(util.DockerInstallErrMsg, err)
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	reader, err := cli.ImagePull(context.Background(),
		util.BopmaticBuildImageName, image.PullOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to pull image: %v", err)
		os.Exit(1)
	}

	defer reader.Close()

	// cli.ImagePull() returns newline separated JSON documents; parse
	// them so we can show more human friendly output to the user
	type ProgressDetail struct {
		Current uint64 `json:"current"`
		Total   uint64 `json:"total"`
	}
	type DockerStatus struct {
		Status string         `json:"status"`
		Id     string         `json:"id"`
		Detail ProgressDetail `json:"progressDetail"`
	}

	var dockerStatus DockerStatus
	progressScanner := bufio.NewScanner(reader)
	for progressScanner.Scan() {
		err = json.Unmarshal(progressScanner.Bytes(), &dockerStatus)
		if err != nil {
			continue
		}

		var progressPct uint64
		progressPct = 100
		if dockerStatus.Detail.Total != 0 {
			progressPct =
				(dockerStatus.Detail.Current * 100) / dockerStatus.Detail.Total
		}

		fmt.Printf("\t%v id:%v progress:%v%%\n", dockerStatus.Status,
			dockerStatus.Id, progressPct)
	}

	err = progressScanner.Err()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to pull image: %v", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully pulled %v\n", util.BopmaticBuildImageName)
}

type ProjTemplate struct {
	name    string
	srcPath string
}

func selectProjectTemplateKey(tmplNameIn string,
	templateMap map[string]ProjTemplate) string {

	if tmplNameIn == "" {
		return ""
	}

	if _, ok := templateMap[tmplNameIn]; ok {
		return tmplNameIn
	}

	fmt.Fprintf(os.Stderr, "%v is not a valid project template\n", tmplNameIn)
	return ""
}

func readContainerDir(dir string) (dirEntries []string, err error) {

	ctx := context.Background()
	tmpBuf := new(bytes.Buffer)

	err = util.RunContainerCommand(ctx, []string{"ls", dir}, tmpBuf, os.Stderr)
	if err != nil {
		return nil, err
	}

	dirEntries = make([]string, 0)
	for _, entry := range strings.Split(tmpBuf.String(), "\n") {
		if entry != "" {
			dirEntries = append(dirEntries, entry)
		}
	}

	return dirEntries, nil
}

func fetchTemplateSet(subdirs []string) map[string]ProjTemplate {

	tmplSet := make(map[string]ProjTemplate)

	for _, subdir := range subdirs {
		dir := fmt.Sprintf("%v/%v", ExamplesDir, subdir)
		dirEntries, err := readContainerDir(dir)
		if err != nil {
			// can occur if user has an older build container'
			fmt.Fprintf(os.Stderr, "Failed to retrieve list of %v templates: %v. Skipping.\n",
				subdir, err)
			continue
		}

		for _, tmpl := range dirEntries {
			nameKey := subdir + "/" + tmpl
			tmplSet[nameKey] = ProjTemplate{
				name:    nameKey,
				srcPath: ExamplesDir + "/" + subdir + "/" + tmpl,
			}
		}
	}

	return tmplSet
}

func fetchTemplates() (serviceTemplates, clientTemplates map[string]ProjTemplate) {

	supportedLanguages := []string{"golang", "java", "python"}

	serviceTemplates = fetchTemplateSet(supportedLanguages)

	serviceTemplates["staticsite"] = ProjTemplate{
		name:    "staticsite",
		srcPath: ExamplesDir + "/staticsite",
	}

	clientTemplates = fetchTemplateSet([]string{ClientTemplateSubdir})

	return serviceTemplates, clientTemplates
}

func getUserInputsForNewPkg(serviceTemplates map[string]ProjTemplate) (
	selectedTmplKey, projectName string) {

	user, err := user.Current()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to determine your username: %v", err)
		os.Exit(1)
	}

	fmt.Printf("Available project templates:\n")
	var templateKeysSorted []string
	for key, _ := range serviceTemplates {
		templateKeysSorted = append(templateKeysSorted, key)
	}

	sort.Strings(templateKeysSorted)
	for _, key := range templateKeysSorted {
		fmt.Printf("\t%v\n", key)
	}

	var templateName string
	for selectedTmplKey = ""; selectedTmplKey == ""; selectedTmplKey = selectProjectTemplateKey(templateName, serviceTemplates) {

		const defaultTemplateName = DefaultTemplate
		templateName = defaultTemplateName

		fmt.Printf("Enter Bopmatic Project Template [%v]: ", defaultTemplateName)
		fmt.Scanf("%s", &templateName)
		templateName = strings.TrimSpace(templateName)
	}

	for {
		projectName = user.Username + path.Base(templateName)
		fmt.Printf("Enter Bopmatic Project Name [%v]: ", projectName)
		fmt.Scanf("%s", &projectName)
		projectName = strings.TrimSpace(projectName)
		isGoodName, reason := bopsdk.IsGoodProjectName(projectName)
		if isGoodName {
			break
		} else {
			fmt.Fprintf(os.Stderr, "%v\n", reason)
		}
	}

	return selectedTmplKey, projectName
}

func replaceTemplateKeywordInFile(filename, existingText, replaceText string,
	ignoreIfNotExist bool) {

	fileContentBytes, err := ioutil.ReadFile(filename)
	if err != nil {
		if ignoreIfNotExist && os.IsNotExist(err) {
			return
		}
		fmt.Fprintf(os.Stderr, "Failed to set replace %v with %v in %v: %v",
			existingText, replaceText, filename, err)
		os.Exit(1)
	}
	fileContent := string(fileContentBytes)

	fileContent = strings.ReplaceAll(fileContent,
		strings.ToLower(existingText), strings.ToLower(replaceText))

	hasUpperCase := (strings.ToLower(existingText) != existingText)
	if hasUpperCase {
		fileContent = strings.ReplaceAll(fileContent, existingText, replaceText)
	}

	err = ioutil.WriteFile(filename, []byte(fileContent), 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to update %v: %v", filename, err)
		os.Exit(1)
	}
}

func createProjectFromTemplate(serviceTemplates, clientTemplates map[string]ProjTemplate,
	selectedTmplKey, projectName string) (projectDir, projectFile string) {

	ctx := context.Background()

	// copy project from template
	err := util.RunContainerCommand(ctx, []string{"cp", "-r",
		serviceTemplates[selectedTmplKey].srcPath, "./" + projectName},
		os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create project %v: %v", projectName,
			err)
		os.Exit(1)
	}

	// if there's a matching client template, replace site_assets with it
	tmplBase := path.Base(selectedTmplKey)
	clientTmplKey := ClientTemplateSubdir + "/" + tmplBase
	clientTmpl, ok := clientTemplates[clientTmplKey]
	if ok {
		siteAssetsDir := "./" + projectName + "/" + SiteAssetsSubdir
		err := util.RunContainerCommand(ctx, []string{"rm", "-rf",
			siteAssetsDir}, os.Stdout, os.Stderr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to remove %v: %v", siteAssetsDir, err)
			os.Exit(1)
		}

		clientDir := "./" + projectName + "/" + ClientTemplateSubdir
		err = util.RunContainerCommand(ctx, []string{"cp", "-r",
			clientTmpl.srcPath, clientDir}, os.Stdout, os.Stderr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to copy client assets into %v: %v",
				siteAssetsDir, err)
			os.Exit(1)
		}
	}

	// set the created project's name
	// @todo find a cleaner way to replace the project name
	projectDir = filepath.Join(".", projectName)
	projectFile = filepath.Join(projectDir, "Bopmatic.yaml")
	projectMakefile := filepath.Join(projectDir, "Makefile")
	clientMakefile := filepath.Join(projectDir, ClientTemplateSubdir, "Makefile")
	templateToken := filepath.Join(projectDir, "template_replace_keyword")

	templateKeyword, err := ioutil.ReadFile(templateToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set project name %v: %v", projectName,
			err)
		os.Exit(1)
	}

	replaceTemplateKeywordInFile(projectFile, string(templateKeyword),
		projectName, false)
	replaceTemplateKeywordInFile(projectMakefile, string(templateKeyword),
		projectName, true)
	if ok {
		replaceTemplateKeywordInFile(clientMakefile, string(templateKeyword),
			projectName, true)
	}

	_ = os.Remove(templateToken)

	return projectDir, projectFile
}

func projCreateMain(args []string) {
	// @todo get project id via sr's CreateProject() primitive
	haveBuildImg, err := util.HasBopmaticBuildImage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if !haveBuildImg {
		fmt.Fprintf(os.Stderr, "Could not find Bopmatic Build Image; please run:\n\n\tbopmatic config\n")
		os.Exit(1)
	}

	httpClient, err := getHttpClientFromCreds()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	serviceTemplates, clientTemplates := fetchTemplates()

	selectedTmplKey, projectName := getUserInputsForNewPkg(serviceTemplates)

	projectDir, projectFile := createProjectFromTemplate(serviceTemplates,
		clientTemplates, selectedTmplKey, projectName)

	// validate everything worked
	proj, err := bopsdk.NewProject(projectFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Created project %v but it fails to parse: %v",
			projectDir, err)
		os.Exit(1)
	}

	err = proj.Register(bopsdk.DeployOptHttpClient(httpClient))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Created project %v but it failed to register: %v",
			projectDir, err)
		os.Exit(1)
	}

	fmt.Printf("Successfully created .%v%v:\n%v", string(os.PathSeparator),
		projectDir, proj.String())

	fmt.Printf("\nTo build your new project next run:\n\t'cd %v; bopmatic package build'\n",
		projectDir)
}

func projDestroyMain(args []string) {
	httpClient, err := getHttpClientFromCreds()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	type destroyOpts struct {
		common commonOpts
	}

	var opts destroyOpts
	f := flag.NewFlagSet("bopmatic project destroy", flag.ExitOnError)
	setCommonFlags(f, &opts.common)
	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if opts.common.projectId == "" {
		proj, err := bopsdk.NewProject(opts.common.projectFilename)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				fmt.Fprintf(os.Stderr, "Could not find project. Please specify --projid, --projfile, run from within a Bopmatic project directory.\n")
			} else {
				fmt.Fprintf(os.Stderr, "%v\n", err)
			}
			os.Exit(1)
		}
		opts.common.projectId = proj.Desc.Id
	}

	fmt.Printf("Destroying projectId:%v...", opts.common.projectId)
	err = bopsdk.UnregisterProject(opts.common.projectId,
		bopsdk.DeployOptHttpClient(httpClient))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to destroy project: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("done.\nProject %v was successfully deleted\n",
		opts.common.projectId)
}

func projDeactivateMain(args []string) {
	httpClient, err := getHttpClientFromCreds()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	type deactivateOpts struct {
		common commonOpts
	}

	var opts deactivateOpts
	f := flag.NewFlagSet("bopmatic project deactivate", flag.ExitOnError)
	setCommonFlags(f, &opts.common)
	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if opts.common.projectId == "" {
		proj, err := bopsdk.NewProject(opts.common.projectFilename)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				fmt.Fprintf(os.Stderr, "Could not find project. Please specify --projid, --projfile, run from within a Bopmatic project directory.\n")
			} else {
				fmt.Fprintf(os.Stderr, "%v\n", err)
			}
			os.Exit(1)
		}
		opts.common.projectId = proj.Desc.Id
	}

	// @todo implement environment ids
	fmt.Printf("Deactivating projId:%v...", opts.common.projectId)
	deployId, err := bopsdk.DeactivateProject(opts.common.projectId, "",
		bopsdk.DeployOptHttpClient(httpClient))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to deactivate project: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Started\nDeactivationg takes about 10 minutes. You can check progress with:\n\t'bopmatic deploy describe --deployid %v'\n",
		deployId)
}

//go:embed projHelp.txt
var projHelpText string

func projHelpMain(args []string) {
	fmt.Printf(projHelpText)
}

//go:embed version.txt
var versionText string

const DevVersionText = "v0.devbuild"

func versionMain(args []string) {
	fmt.Printf("bopmatic-cli-%v\n", versionText)
}

func isBrewVersion() bool {
	if versionText[len(versionText)-1] == BrewVersionSuffix[0] {
		return true
	}

	return false
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

func checkAndPrintUpgradeCLIWarning() bool {
	if versionText == DevVersionText {
		return false
	}
	latestVer, err := getLatestVersion()
	if err != nil {
		return false
	}
	if latestVer == versionText {
		return false
	}

	fmt.Fprintf(os.Stderr, "*WARN*: A new version of the Bopmatic CLI is available (%v). Please upgrade via 'bopmatic upgrade'.\n",
		latestVer)

	return true
}

func checkAndPrintUpgradeContainerWarning() bool {
	haveBuildImg, err := util.HasBopmaticBuildImage()
	if err != nil || !haveBuildImg {
		return false
	}

	needUpgrade, err := util.DoesLocalImageNeedUpdate(util.BopmaticImageRepo,
		util.BopmaticImageTag)
	if err != nil || needUpgrade == false {
		return false
	}

	fmt.Fprintf(os.Stderr, "*WARN*: A new version of the Bopmatic Build container is available. Please upgrade via 'bopmatic upgrade'.\n")

	return true
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

func deployListMain(args []string) {
	httpClient, err := getHttpClientFromCreds()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	type listOpts struct {
		common commonOpts
	}

	var opts listOpts

	f := flag.NewFlagSet("bopmatic deploy list", flag.ExitOnError)
	setCommonFlags(f, &opts.common)

	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if opts.common.projectId == "" {
		proj, err := bopsdk.NewProject(opts.common.projectFilename)
		if err == nil {
			opts.common.projectId = proj.Desc.Id
		}
	}

	fmt.Printf("Listing deployments for projId:%v...", opts.common.projectId)

	// @todo add envId
	deployments, err := bopsdk.ListDeployments(opts.common.projectId, "",
		bopsdk.DeployOptHttpClient(httpClient))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if len(deployments) == 0 {
		fmt.Printf("\nNo currently deployed packages\n")
	} else {
		fmt.Printf("\nDeployment Id\n")

		for _, deployId := range deployments {
			fmt.Printf("%v\n", deployId)
		}
	}
}

func projListMain(args []string) {
	httpClient, err := getHttpClientFromCreds()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	type listOpts struct {
		common commonOpts
	}

	var opts listOpts

	f := flag.NewFlagSet("bopmatic proj list", flag.ExitOnError)
	setCommonFlags(f, &opts.common)

	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if opts.common.projectId == "" {
		proj, err := bopsdk.NewProject(opts.common.projectFilename)
		if err == nil {
			opts.common.projectId = proj.Desc.Id
		}
	}

	fmt.Printf("Listing projects...")

	// @todo add envId
	projects, err := bopsdk.ListProjects(bopsdk.DeployOptHttpClient(httpClient))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if len(projects) == 0 {
		fmt.Printf("\nNo projects exist; create a new one with 'bopmatic project create'\n")
	} else {
		fmt.Printf("\nProject Id\n")

		for _, projId := range projects {
			fmt.Printf("%v\n", projId)
		}
	}
}

func deployMain(args []string) {
	exitStatus := 0

	deploySubCommandName := "help"
	if len(args) == 0 {
		exitStatus = 1
	} else {
		deploySubCommandName = args[0]
	}

	deploySubCommand, ok := deploySubCommandTab[deploySubCommandName]
	if !ok {
		exitStatus = 1
		deploySubCommand = deployHelpMain
	}

	if len(args) > 0 {
		args = args[1:]
	}

	deploySubCommand(args)

	os.Exit(exitStatus)
}

func projMain(args []string) {
	exitStatus := 0

	projSubCommandName := "help"
	if len(args) == 0 {
		exitStatus = 1
	} else {
		projSubCommandName = args[0]
	}

	projSubCommand, ok := projSubCommandTab[projSubCommandName]
	if !ok {
		exitStatus = 1
		projSubCommand = projHelpMain
	}

	if len(args) > 0 {
		args = args[1:]
	}

	projSubCommand(args)

	os.Exit(exitStatus)
}

func deployDescribeMain(args []string) {
	httpClient, err := getHttpClientFromCreds()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	type describeOpts struct {
		common commonOpts
	}

	var opts describeOpts

	f := flag.NewFlagSet("bopmatic deploy describe", flag.ExitOnError)
	setCommonFlags(f, &opts.common)

	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if opts.common.deployId == "" {
		fmt.Fprintf(os.Stderr, "Please specify deployment id with --deployid. If you don't know this, try 'bopmatic deployment list'\n")
		os.Exit(1)
	}

	fmt.Printf("Describing deployId:%v...", opts.common.deployId)
	deployDesc, err := bopsdk.DescribeDeployment(opts.common.deployId,
		bopsdk.DeployOptHttpClient(httpClient))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nDeployment Id:%v\n\tProject Id:%v\n\tPackage Id:%v\n\tEnvironment Id:%v\n\tType:%v\n\tInitiator:%v\n\tState:%v\n\tDetail:%v\n\tCreate Time:%v\n\tValidation Start Time:%v\n\tBuild Start Time:%v\n\tDeploy Start Time:%v\n\tCompletion Time:%v\n",
		deployDesc.Id, deployDesc.Header.ProjId, deployDesc.Header.PkgId,
		deployDesc.Header.EnvId, deployDesc.Header.Type,
		deployDesc.Header.Initiator, deployDesc.State, deployDesc.StateDetail,
		deployDesc.CreateTime, deployDesc.ValidationStartTime,
		deployDesc.BuildStartTime, deployDesc.DeployStartTime,
		deployDesc.EndTime)

	switch deployDesc.State {
	case pb.DeploymentState_CREATED:
		fmt.Printf("\nYour deployment has been created and will be validated shortly\n")
	case pb.DeploymentState_DPLY_VALIDATING:
		fmt.Printf("\nBopmatic ServiceRunner is validating your project package\n")
	case pb.DeploymentState_DPLY_BUILDING:
		fmt.Printf("\nBopmatic ServiceRunner is building infrastructure for your project package\n")
	case pb.DeploymentState_DEPLOYING:
		fmt.Printf("\nBopmatic ServiceRunner is deploying your package into production\n")
	case pb.DeploymentState_SUCCESS:
		fmt.Printf("\nBopmatic ServiceRunner has successully completed this	deployment of your package\n")
	case pb.DeploymentState_FAILED:
		fallthrough
	case pb.DeploymentState_UNKNOWN_DEPLOY_STATE:
		fmt.Printf("\nAn error occurred within Bopmatic ServiceRunner and a support staff member needs to examine the situation.\n")
	}
}
