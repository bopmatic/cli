package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "embed"

	"github.com/docker/docker/api/types"
	dockerClient "github.com/docker/docker/client"

	bopsdk "github.com/bopmatic/sdk/golang"
	"github.com/bopmatic/sdk/golang/pb"
	"github.com/bopmatic/sdk/golang/util"
)

type commonOpts struct {
	projectFilename string
	projectName     string
	packageId       string
}

var pkgSubCommandTab = map[string]func(args []string){
	"build":    pkgBuildMain,
	"deploy":   pkgDeployMain,
	"list":     pkgListMain,
	"destroy":  pkgDestroyMain,
	"describe": pkgDescribeMain,
	"help":     pkgHelpMain,
}

var subCommandTab = map[string]func(args []string){
	"package":  pkgMain,
	"describe": describeMain,
	"help":     helpMain,
	"config":   configMain,
	"new":      newMain,
	"version":  versionMain,
	"upgrade":  upgradeMain,
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
	err = pkg.Deploy(bopsdk.DeployOptHttpClient(httpClient))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Started\nDeploying takes about 10 minutes. You can check deploy progress with:\n\t'bopmatic package describe --pkgid %v'\n",
		pkg.Id)
}

func validateNoConflicts(httpClient *http.Client, pkg *bopsdk.Package) {
	fmt.Printf("Checking for project %v conflicts...", pkg.Proj.Desc.Name)

	pkgs, err := bopsdk.List("", bopsdk.DeployOptHttpClient(httpClient))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	for _, existingPkg := range pkgs {
		if pkg.Proj.Desc.Name != existingPkg.ProjectName {
			continue
		}

		descReply, err := bopsdk.Describe(existingPkg.PackageId,
			bopsdk.DeployOptHttpClient(httpClient))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		switch descReply.PackageState {
		case pb.PackageState_INVALID:
			fallthrough
		case pb.PackageState_PRODUCTION:
			continue
		default:
			fmt.Fprintf(os.Stderr, "\nExisting pkgid:%v for project %v is currently transitioning; please wait until this completes before attempting to deploy a new package for %v\n",
				existingPkg.PackageId, existingPkg.ProjectName,
				existingPkg.ProjectName)
			fmt.Fprintf(os.Stderr, "You can monitor progress with:\n\t'bopmatic package describe --pkgid %v'\n",
				existingPkg.PackageId)
			os.Exit(1)
		}
	}
}

func pkgDestroyMain(args []string) {
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

	f := flag.NewFlagSet("bopmatic package destroy", flag.ExitOnError)
	setCommonFlags(f, &opts.common)

	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if opts.common.packageId == "" {
		fmt.Fprintf(os.Stderr, "Please specify package id with --pkgid. If you don't know this, try 'bopmatic list'\n")
		os.Exit(1)
	}

	fmt.Printf("Checking for existing packages...")
	pkgs, err := bopsdk.List("", bopsdk.DeployOptHttpClient(httpClient))
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
		fmt.Printf("\nPackage id %v no longer exists; you can upload a new one with:\n\t'bopmatic package deploy'\n",
			opts.common.packageId)
		os.Exit(1)
	}

	fmt.Printf("ok. Checking existing pkgId:%v status...", opts.common.packageId)
	descReply, err := bopsdk.Describe(opts.common.packageId,
		bopsdk.DeployOptHttpClient(httpClient))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	switch descReply.PackageState {
	case pb.PackageState_UPLOADING:
		fallthrough
	case pb.PackageState_UPLOADED:
		fallthrough
	case pb.PackageState_VALIDATING:
		fallthrough
	case pb.PackageState_BUILDING:
		fallthrough
	case pb.PackageState_DEPLOYING:
		fmt.Printf("ok.\nBopmatic ServiceRunner is currently deploying pkgid:%v; please try deleting again later once this has completed.\n",
			opts.common.packageId)
		os.Exit(1)
	case pb.PackageState_INVALID:
		fallthrough
	case pb.PackageState_PRODUCTION:
		fmt.Printf("ok.\nDestroying pkgId:%v...", opts.common.packageId)
		err = bopsdk.Delete(opts.common.packageId,
			bopsdk.DeployOptHttpClient(httpClient))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\nSuccessfully started destroying pkgId:%v. Teardown takes about 10 minutes. You can check progress with:\n\t'bopmatic package describe --pkgid %v'\n",
			opts.common.packageId, opts.common.packageId)
	case pb.PackageState_DEACTIVATING:
		fallthrough
	case pb.PackageState_DELETING:
		fmt.Printf("ok.\nBopmatic ServiceRunner is already destroying pkgid:%v. You can check progress with:\n\t'bopmatic package describe --pkgid %v'\n",
			opts.common.packageId, opts.common.packageId)
		os.Exit(1)
	case pb.PackageState_DELETED:
		fmt.Printf("ok.\nBopmatic ServiceRunner has already destroyed pkgid:%v\n",
			opts.common.packageId)
		os.Exit(1)
	case pb.PackageState_SUPPORT_NEEDED:
		fallthrough
	case pb.PackageState_UNKNOWN_PKG_STATE:
		fallthrough
	default:
		fmt.Printf("\nAn error occurred within Bopmatic ServiceRunner and a support staff member needs to examine the situation.\n")
	}
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
	if opts.common.projectName == "" {
		proj, err := bopsdk.NewProject(opts.common.projectFilename)
		if err == nil {
			opts.common.projectName = proj.Desc.Name
		}
	}

	// @todo project filter not yet implemented
	//if opts.common.projectName == "" {
	fmt.Printf("Listing packages for all projects...")
	//} else {
	//	fmt.Printf("Listing packages for project %v...",
	//		opts.common.projectName)
	//}

	pkgs, err := bopsdk.List(opts.common.projectName,
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
			fmt.Printf("%v\t\t%v\n", pkg.ProjectName, pkg.PackageId)
		}
	}
}

//go:embed pkgHelp.txt
var pkgHelpText string

func pkgHelpMain(args []string) {
	fmt.Printf(pkgHelpText)
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
		fmt.Fprintf(os.Stderr, "Please specify package id with --pkgid. If you don't know this, try 'bopmatic list'\n")
		os.Exit(1)
	}

	fmt.Printf("Listing packages...")
	pkgs, err := bopsdk.List(opts.common.projectName,
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
		fmt.Printf("\nPackage id %v no longer exists; you can upload a new one with:\n\t'bopmatic package deploy'\n",
			opts.common.packageId)
		os.Exit(1)
	}

	fmt.Printf("Describing pkgId:%v...", opts.common.packageId)
	descReply, err := bopsdk.Describe(opts.common.packageId,
		bopsdk.DeployOptHttpClient(httpClient))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nProject:%v PackageId:%v State:%v\n",
		descReply.Desc.ProjectName, descReply.Desc.PackageId,
		descReply.PackageState)
	switch descReply.PackageState {
	case pb.PackageState_UPLOADING:
		fmt.Printf("\nYour project is being uploaded to Bopmatic ServiceRunner\n")
	case pb.PackageState_UPLOADED:
		fmt.Printf("\nYour project package was uploaded Bopmatic ServiceRunner and will next be validated\n")
	case pb.PackageState_VALIDATING:
		fmt.Printf("\nBopmatic ServiceRunner is validating your project package\n")
	case pb.PackageState_INVALID:
		fmt.Printf("\nSomething is wrong with your project package and it cannot be deployed. Please delete it with:\n\t'bopmatic destroy --pkgid %v\n",
			descReply.Desc.PackageId)
	case pb.PackageState_BUILDING:
		fmt.Printf("\nBopmatic ServiceRunner is building infrastructure for your project package\n")
	case pb.PackageState_DEPLOYING:
		fmt.Printf("\nBopmatic ServiceRunner is deploying infrastructure for your project package\n")
	case pb.PackageState_PRODUCTION:
		fmt.Printf("\nBopmatic ServiceRunner has deployed your project. Try it out:\n\n")
		fmt.Printf("\tWebsite: %v\n", descReply.SiteEndpoint)
		printExampleCurl(descReply)
	case pb.PackageState_DEACTIVATING:
		fmt.Printf("\nBopmatic ServiceRunner is currently removing your project package from production.\n")
	case pb.PackageState_DELETING:
		fmt.Printf("\nBopmatic ServiceRunner is currently deleting your project package\n")
	case pb.PackageState_DELETED:
		fmt.Printf("\nBopmatic ServiceRunner has deleted your project package\n")
	case pb.PackageState_SUPPORT_NEEDED:
		fallthrough
	case pb.PackageState_UNKNOWN_PKG_STATE:
		fallthrough
	default:
		fmt.Printf("\nAn error occurred within Bopmatic ServiceRunner and a support staff member needs to examine the situation.\n")
	}
}

func printExampleCurl(descReply *pb.DescribePackageReply) {
	if len(descReply.RpcEndpoints) == 0 {
		return
	}

	fmt.Printf("\tAPI Endpoints(%v):\n", len(descReply.RpcEndpoints))
	for _, rpc := range descReply.RpcEndpoints {
		fmt.Printf("\t\t%v\n", rpc)
	}
	firstApiUrl := descReply.RpcEndpoints[0]

	fmt.Printf("\nYou can invoke your API directly from your shell via:\n")

	if !strings.Contains(firstApiUrl, "SayHello") {
		fmt.Printf("\tcurl -X POST -H \"Content-Type: application/json\" --data	<req> %v\n",
			firstApiUrl)
	} else {
		// @todo temporary hack to provide cut/pasteable curl calls for helloworld
		fmt.Printf("\tcurl -X POST -H \"Content-Type: application/json\" --data	'{\"name\": \"somename\"}' %v\n",
			firstApiUrl)
	}
}

func describeMain(args []string) {
	type describeOpts struct {
		common commonOpts
	}

	var opts describeOpts

	f := flag.NewFlagSet("bopmatic describe", flag.ExitOnError)
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

	fmt.Printf("%v", proj)
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

	haveBuildImg, err := util.HasBopmaticBuildImage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if haveBuildImg {
		fmt.Printf("Update Bopmatic Build Image? (Y/N) [Y]: ")
	} else {
		fmt.Printf("Bopmatic needs to download the Bopmatic Build Image in order to build projects. It is roughly 620MiB(compressed) in size.\n")
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
	json.Unmarshal(releaseJsonDoc, &releaseDoc)

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
	if versionText == DevVersionText {
		fmt.Fprintf(os.Stderr, "Skipping upgrade on development version\n")
		os.Exit(1)
	}
	latestVer, err := getLatestVersion()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not determine latest version: %v\n", err)
		os.Exit(1)
	}
	if latestVer == versionText {
		fmt.Printf("%v is already the latest version\n", versionText)
		os.Exit(0)
	}

	fmt.Printf("A new version of the Bopmatic CLI is available (%v). Upgrade? (Y/N) [Y]: ",
		latestVer)
	shouldUpgrade := "Y"
	fmt.Scanf("%s", &shouldUpgrade)
	shouldUpgrade = strings.ToUpper(strings.TrimSpace(shouldUpgrade))

	if shouldUpgrade[0] != 'Y' {
		os.Exit(0)
	}

	fmt.Printf("Upgrading bopmatic cli from %v to %v...\n", versionText,
		latestVer)

	if isBrewVersion() {
		upgradeCLIViaBrew()
	} else {
		upgradeCLIViaGithub(latestVer)
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
		util.BopmaticBuildImageName, types.ImagePullOptions{})
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
			fmt.Fprintf(os.Stderr, "Failed to retrieve list of %v templates: %v\n",
				subdir, err)
			os.Exit(1)
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

func newMain(args []string) {
	haveBuildImg, err := util.HasBopmaticBuildImage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if !haveBuildImg {
		fmt.Fprintf(os.Stderr, "Could not find Bopmatic Build Image; please run:\n\n\tbopmatic config\n")
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

	fmt.Printf("Successfully created .%v%v:\n%v", string(os.PathSeparator),
		projectDir, proj.String())

	fmt.Printf("\nTo build your new project next run:\n\t'cd %v; bopmatic package build'\n",
		projectDir)
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
	f.StringVar(&o.projectName, "projname", "", "Bopmatic project name")
	f.StringVar(&o.packageId, "pkgid", "", "Bopmatic project package identifier")
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

func main() {
	versionText = strings.Split(versionText, "\n")[0]
	exitStatus := 0

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
